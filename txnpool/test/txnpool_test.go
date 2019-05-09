/*
 * Copyright (C) 2018 The ontology Authors
 * This file is part of The ontology library.
 *
 * The ontology is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The ontology is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public License
 * along with The ontology.  If not, see <http://www.gnu.org/licenses/>.
 */

package txnpool

import (
	"sync"
	"testing"
	"time"

	"github.com/ontio/ontology-eventbus/actor"
	"github.com/ontio/ontology/common"
	"github.com/ontio/ontology/common/config"
	"github.com/ontio/ontology/common/log"
	"github.com/ontio/ontology/core/genesis"
	"github.com/ontio/ontology/core/ledger"
	"github.com/ontio/ontology/core/payload"
	"github.com/ontio/ontology/core/types"
	"github.com/ontio/ontology/txnpool"
	tc "github.com/ontio/ontology/txnpool/common"
	tp "github.com/ontio/ontology/txnpool/proc"
	"github.com/ontio/ontology/validator/stateful"
	"github.com/ontio/ontology/validator/stateless"
)

var (
	tx    *types.Transaction
	topic string
)

func init() {
	log.InitLog(log.InfoLog, log.Stdout)
	topic = "TXN"

	mutable := &types.MutableTransaction{
		TxType:  types.Invoke,
		Nonce:   uint32(time.Now().Unix()),
		Payload: &payload.InvokeCode{Code: []byte{}},
	}

	tx, _ = mutable.IntoImmutable()
}

func startActor(obj interface{}) *actor.PID {
	props := actor.FromProducer(func() actor.Actor {
		return obj.(actor.Actor)
	})

	pid := actor.Spawn(props)
	return pid
}

func Test_RCV(t *testing.T) {
	var s *tp.TXPoolServer
	var wg sync.WaitGroup
	var err error
	ledger.DefLedger, err = ledger.NewLedger(config.DEFAULT_DATA_DIR, 0)
	if err != nil {
		t.Error("failed  to new ledger")
		return
	}

	bookKeepers, err := config.DefConfig.GetBookkeepers()
	if err != nil {
		t.Error("failed to get bookkeepers")
		return
	}
	genesisConfig := config.DefConfig.Genesis
	shardConfig := config.DefConfig.Shard
	genesisBlock, err := genesis.BuildGenesisBlock(bookKeepers, genesisConfig, shardConfig)
	if err != nil {
		t.Error("failed to build genesis block", err)
		return
	}
	err = ledger.DefLedger.Init(bookKeepers, genesisBlock)
	if err != nil {
		t.Error("failed to initialize default ledger", err)
		return
	}

	// Start txnpool server to receive msgs from p2p, consensus and valdiators
	shardId := common.NewShardIDUnchecked(config.DEFAULT_SHARD_ID)
	s = tp.NewTxPoolServer(shardId, ledger.DefLedger, tc.MAX_WORKER_NUM, true, false)

	// Initialize an actor to handle the msgs from valdiators
	rspActor := tp.NewVerifyRspActor(s)
	rspPid := startActor(rspActor)
	if rspPid == nil {
		t.Error("Fail to start verify rsp actor")
		return
	}
	s.RegisterActor(tc.VerifyRspActor, rspPid)

	// Initialize an actor to handle the msgs from consensus
	tpa := tp.NewTxPoolActor(s)
	txPoolPid := startActor(tpa)
	if txPoolPid == nil {
		t.Error("Fail to start txnpool actor")
		return
	}
	s.RegisterActor(tc.TxPoolActor, txPoolPid)

	// Initialize an actor to handle the msgs from p2p and api
	mgr, err := txnpool.NewTxnPoolManager(shardId, true, false)
	if err != nil {
		t.Fatalf("Test case: new tx pool server failed")
	}

	ta := txnpool.NewTxActor(mgr)
	txPid := startActor(ta)
	if txPid == nil {
		t.Error("Fail to start txn actor")
		return
	}
	s.RegisterActor(tc.TxActor, txPid)

	// Start stateless validator
	statelessV, err := stateless.NewValidator("stateless")
	if err != nil {
		t.Errorf("failed to new stateless valdiator", err)
		return
	}
	statelessV.Register(rspPid)

	statelessV2, err := stateless.NewValidator("stateless2")
	if err != nil {
		t.Errorf("failed to new stateless valdiator", err)
		return
	}
	statelessV2.Register(rspPid)

	statelessV3, err := stateless.NewValidator("stateless3")
	if err != nil {
		t.Errorf("failed to new stateless valdiator", err)
		return
	}
	statelessV3.Register(rspPid)

	statefulV, err := stateful.NewValidator("stateful", ledger.DefLedger)
	if err != nil {
		t.Errorf("failed to new stateful valdiator", err)
		return
	}
	statefulV.Register(rspPid)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			var j int
			defer wg.Done()
			for {
				j++
				txReq := &tc.TxReq{
					Tx:     tx,
					Sender: tc.NilSender,
				}
				txPid.Tell(txReq)

				if j >= 4 {
					return
				}
			}
		}()
	}

	wg.Wait()
	time.Sleep(1 * time.Second)
	txPoolPid.Tell(&tc.GetTxnPoolReq{ByCount: true})
	txPoolPid.Tell(&tc.GetPendingTxnReq{ByCount: true})
	time.Sleep(2 * time.Second)

	statelessV.UnRegister(rspPid)
	statelessV2.UnRegister(rspPid)
	statelessV3.UnRegister(rspPid)
	statefulV.UnRegister(rspPid)
	s.Stop()
}
