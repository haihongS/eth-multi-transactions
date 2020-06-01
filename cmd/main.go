package main

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/robfig/cron"
	"github.com/syndtr/goleveldb/leveldb"

	emt "github.com/haihongs/eth-multi-transactions"
	"github.com/haihongs/eth-multi-transactions/common/logger"
)

var (
	Wei   = big.NewInt(1)
	GWei  = big.NewInt(1e9)
	Ether = big.NewInt(0).Mul(GWei, GWei) // 1ether = 1e18wei
)

type dest struct {
	addr    string
	percent *big.Int
	amt     *big.Int
}

func main() {
	logger.Init(logger.DebugLevel)

	path := "./db"
	nodeEndpoint := ""
	addr := ""
	var users []*dest

	// register db
	db, err := leveldb.OpenFile(path, nil)
	if err != nil {
		logger.Fatal("failed to init leveldb", "dir", path, "err", err)
	}
	defer db.Close()

	wdDB := emt.NewWithdrawalDB(db)

	// register ethclient
	ethc, err := ethclient.Dial(nodeEndpoint)
	if err != nil {
		logger.Fatal("failed to init ethclient", "err", err)
	}

	// generate withdrawals
	c := cron.New()
	if _, err := c.AddFunc("@every 12h", func() { generateWithdrawals(wdDB, ethc, addr, users) }); err != nil {
		logger.Fatal("failed to init cron", "err", err)
	}
	c.Start()

	// main loop
	for {
		if err := handle(wdDB, ethc, addr); err != nil {
			logger.Error("failed to handle it", "err", err)
		}
		time.Sleep(60 * time.Second)
	}
}

func handle(db *emt.WdDB, ethc *ethclient.Client, addr string) error {
	// check kv
	initValue := emt.ToBigEndianBytes(1)

	if err := db.GetOrSet([]byte("kv-id"), initValue); err != nil {
		return err
	}

	if err := db.GetOrSet([]byte("kv-nonce"), initValue); err != nil {
		return err
	}

	// calibrate nonce
	var dbNonce uint64
	if n, err := db.GetRawDB().Get([]byte("kv-nonce"), nil); err != nil {
		return err
	} else {
		if nn, err1 := emt.FromBigEndianBytes(n); err1 != nil {
			return err1
		} else {
			dbNonce = nn
		}
	}

	var blockchainNonce uint64
	if b, err := ethc.NonceAt(context.Background(), common.HexToAddress(addr), nil); err != nil {
		return err
	} else {
		blockchainNonce = b
	}

	if dbNonce < blockchainNonce {
		return fmt.Errorf("wrong nonce count, db: %v blockchain: %v", dbNonce, blockchainNonce)
	}

	return nil
}

func generateWithdrawals(wdDB *emt.WdDB, ethc *ethclient.Client, addr string, users []*dest) {
	// retry at most 5 times
	for i := 0; i < 5; i++ {
		// get balance
		balance, err := emt.GetBalance(ethc, addr)
		if err != nil {
			logger.Error("failed to get balance", "err", err)
			time.Sleep(1 * time.Second)
			continue
		}

		// keep 1ether to pay the network fee
		if big.NewInt(0).Div(balance, Ether).Uint64() < 1 {
			logger.Info("not enough balance")
			return
		}

		balance.Sub(balance, Ether)

		// generate records
		total := big.NewInt(0)
		for _, u := range users {
			total.Add(total, u.percent)
		}

		// balance * percent / total
		for _, u := range users {
			u.amt = big.NewInt(0).Mul(balance, u.percent)
			u.amt.Div(u.amt, total)
			if err := wdDB.Insert(u.addr, u.amt, 0, 0, "", uint64(time.Now().Unix()), uint64(time.Now().Unix())); err != nil {
				logger.Info("failed to insert db", "err", err)
				continue
			}
		}

		// set nonce
	}
}
