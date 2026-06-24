// Copyright 2023 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package qrlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/accounts"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/common/uint512"
	"github.com/theQRL/go-qrl/consensus"
	"github.com/theQRL/go-qrl/consensus/beacon"
	"github.com/theQRL/go-qrl/core"
	"github.com/theQRL/go-qrl/core/bloombits"
	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/core/state"
	"github.com/theQRL/go-qrl/core/txpool"
	"github.com/theQRL/go-qrl/core/txpool/legacypool"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/event"
	"github.com/theQRL/go-qrl/internal/blocktest"
	"github.com/theQRL/go-qrl/internal/testutil"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/qrldb"
	"github.com/theQRL/go-qrl/rpc"
)

func receiptLogContractCode() []byte {
	return []byte{
		byte(vm.PUSH4), 0x12, 0x34, 0x56, 0x78,
		byte(vm.PUSH1), 0x00,
		byte(vm.MSTORE),
		byte(vm.PUSH1), 0xcc,
		byte(vm.PUSH1), 0xbb,
		byte(vm.PUSH1), byte(uint512.WordBytes),
		byte(vm.PUSH1), 0x00,
		byte(vm.LOG2),
		byte(vm.PUSH1), 0x01,
		byte(vm.PUSH1), 0x00,
		byte(vm.MSTORE),
		byte(vm.PUSH1), byte(uint512.WordBytes),
		byte(vm.PUSH1), 0x00,
		byte(vm.RETURN),
	}
}

func receiptLogCallData(seed byte) []byte {
	data := make([]byte, 4+2*uint512.WordBytes)
	copy(data[:4], []byte{0xa9, 0x05, 0x9c, 0xbb})
	data[4+uint512.WordBytes-1] = seed
	data[4+2*uint512.WordBytes-1] = seed + 1
	return data
}

func testTransactionMarshal(t *testing.T, tests []txData, config *params.ChainConfig) {
	t.Parallel()
	var (
		signer = types.LatestSigner(config)
		wallet = testutil.MustLoadAccount("alice").MustWallet()
	)

	for i, tt := range tests {
		var tx2 types.Transaction
		tx, err := types.SignNewTx(wallet, signer, tt.Tx)
		if err != nil {
			t.Fatalf("test %d: signing failed: %v", i, err)
		}
		// Regular transaction
		if data, err := json.Marshal(tx); err != nil {
			t.Fatalf("test %d: marshalling failed; %v", i, err)
		} else if err = tx2.UnmarshalJSON(data); err != nil {
			t.Fatalf("test %d: sunmarshal failed: %v", i, err)
		} else if want, have := tx.Hash(), tx2.Hash(); want != have {
			t.Fatalf("test %d: stx changed, want %x have %x", i, want, have)
		}

		// rpcTransaction
		rpcTx := newRPCTransaction(tx, common.Hash{}, 0, 0, nil, config)
		if data, err := json.Marshal(rpcTx); err != nil {
			t.Fatalf("test %d: marshalling failed; %v", i, err)
		} else if err = tx2.UnmarshalJSON(data); err != nil {
			t.Fatalf("test %d: unmarshal failed: %v", i, err)
		} else if want, have := tx.Hash(), tx2.Hash(); want != have {
			t.Fatalf("test %d: tx changed, want %x have %x", i, want, have)
		}
	}
}

func TestTransaction_RoundTripRpcJSON(t *testing.T) {
	config := params.AllBeaconProtocolChanges
	testTransactionMarshal(t, allTransactionTypes(common.Address{0xde, 0xad}, config), config)
}

func TestGetProofStorageValue64Quantity(t *testing.T) {
	t.Parallel()

	var (
		addr    = common.BytesToAddress(bytes.Repeat([]byte{0x11}, common.AddressLength))
		slot    = common.HexToHash("0x01")
		value   = common.BytesToStorageValue64(bytes.Repeat([]byte{0x42}, common.StorageValue64Length))
		genesis = &core.Genesis{
			Config: params.TestChainConfig,
			Alloc: core.GenesisAlloc{
				addr: {
					Balance: big.NewInt(1),
					Storage: map[common.Hash]common.StorageValue64{
						slot: value,
					},
				},
			},
		}
		api = NewBlockChainAPI(newTestBackend(t, 1, genesis, beacon.NewFaker(), func(i int, b *core.BlockGen) {}))
	)

	result, err := api.GetProof(t.Context(), addr, []string{slot.Hex()}, rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.StorageProof) != 1 {
		t.Fatalf("storage proof length mismatch: have %d, want 1", len(result.StorageProof))
	}
	if result.StorageProof[0].Value.ToInt().BitLen() <= 256 {
		t.Fatalf("storage proof value does not exercise high 256 bits: %s", result.StorageProof[0].Value)
	}
	got, err := json.Marshal(result.StorageProof[0].Value)
	if err != nil {
		t.Fatalf("marshal storage proof value: %v", err)
	}
	want := `"` + hexutil.EncodeBig(value.Big()) + `"`
	if string(got) != want {
		t.Fatalf("storage proof value JSON mismatch:\nhave %s\nwant %s", got, want)
	}
}

func TestRPCTransactionPreservesExtraParams(t *testing.T) {
	t.Parallel()

	var (
		config  = params.AllBeaconProtocolChanges
		signer  = types.LatestSigner(config)
		to      = common.Address{0xaa}
		paramsB = []byte{0x01, 0x02}
	)
	wallet, err := wallet.Generate(wallet.ML_DSA_87)
	if err != nil {
		t.Fatalf("wallet: %v", err)
	}
	signed, err := types.SignNewTx(wallet, signer, &types.DynamicFeeTx{
		ChainID:   config.ChainID,
		Nonce:     1,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(2),
		Gas:       params.TxGas,
		To:        &to,
		Value:     big.NewInt(3),
	})
	if err != nil {
		t.Fatalf("sign tx: %v", err)
	}
	tampered, err := signed.WithAuthValues(signer, signed.RawSignatureValue(), signed.RawPublicKeyValue(), signed.Descriptor(), paramsB)
	if err != nil {
		t.Fatalf("re-wrap with extra params: %v", err)
	}

	rpcTx := newRPCTransaction(tampered, common.Hash{}, 0, 0, nil, config)
	if got := []byte(rpcTx.ExtraParams); !reflect.DeepEqual(got, paramsB) {
		t.Fatalf("extraParams mismatch: got %x want %x", got, paramsB)
	}

	data, err := json.Marshal(rpcTx)
	if err != nil {
		t.Fatalf("marshal rpc tx: %v", err)
	}
	if !strings.Contains(string(data), `"extraParams":"0x0102"`) {
		t.Fatalf("rpc json missing extraParams: %s", data)
	}

	var roundTripped types.Transaction
	if err := roundTripped.UnmarshalJSON(data); err != nil {
		t.Fatalf("unmarshal tx: %v", err)
	}
	if got := roundTripped.ExtraParams(); !reflect.DeepEqual(got, paramsB) {
		t.Fatalf("round-trip extraParams mismatch: got %x want %x", got, paramsB)
	}
	if got, want := roundTripped.Hash(), tampered.Hash(); got != want {
		t.Fatalf("round-trip tx hash mismatch: got %x want %x", got, want)
	}
}

type txData struct {
	Tx types.TxData
}

func allTransactionTypes(addr common.Address, config *params.ChainConfig) []txData {
	return []txData{
		{
			Tx: &types.DynamicFeeTx{
				ChainID:   config.ChainID,
				Nonce:     5,
				GasTipCap: big.NewInt(6),
				GasFeeCap: big.NewInt(9),
				Gas:       7,
				To:        &addr,
				Value:     big.NewInt(8),
				Data:      []byte{0, 1, 2, 3, 4},
				AccessList: types.AccessList{
					types.AccessTuple{
						Address:     common.Address{0x2},
						StorageKeys: []common.Hash{types.EmptyRootHash},
					},
				},
				Descriptor:  [3]byte{},
				ExtraParams: []byte{},
				Signature:   []byte{},
				PublicKey:   []byte{},
			},
		},
		{
			Tx: &types.DynamicFeeTx{
				ChainID:     config.ChainID,
				Nonce:       5,
				GasTipCap:   big.NewInt(6),
				GasFeeCap:   big.NewInt(9),
				Gas:         7,
				To:          nil,
				Value:       big.NewInt(8),
				Data:        []byte{0, 1, 2, 3, 4},
				AccessList:  types.AccessList{},
				Descriptor:  [3]byte{},
				ExtraParams: []byte{},
				Signature:   []byte{},
				PublicKey:   []byte{},
			},
		},
	}
}

type testBackend struct {
	db      qrldb.Database
	chain   *core.BlockChain
	pending *types.Block
	txpool  *txpool.TxPool
}

func newTestBackend(t *testing.T, n int, gspec *core.Genesis, engine consensus.Engine, generator func(i int, b *core.BlockGen)) *testBackend {
	var (
		cacheConfig = &core.CacheConfig{
			TrieCleanLimit:    256,
			TrieDirtyLimit:    256,
			TrieTimeLimit:     5 * time.Minute,
			SnapshotLimit:     0,
			TrieDirtyDisabled: true, // Archive mode
		}
	)
	// Generate blocks for testing
	db, blocks, _ := core.GenerateChainWithGenesis(gspec, engine, n, generator)
	txlookupLimit := uint64(0)
	chain, err := core.NewBlockChain(db, cacheConfig, gspec, engine, vm.Config{}, &txlookupLimit)
	if err != nil {
		t.Fatalf("failed to create tester chain: %v", err)
	}
	if n, err := chain.InsertChain(blocks); err != nil {
		t.Fatalf("block %d: failed to insert into chain: %v", n, err)
	}

	txconfig := legacypool.DefaultConfig
	txconfig.Journal = ""
	legacyPool := legacypool.New(txconfig, chain)
	txPool, err := txpool.New(new(big.Int).SetUint64(txconfig.PriceLimit), chain, []txpool.SubPool{legacyPool})
	if err != nil {
		t.Fatalf("failed to create txpool: %v", err)
	}

	backend := &testBackend{db: db, chain: chain, txpool: txPool}
	t.Cleanup(func() {
		if backend.txpool != nil {
			backend.txpool.Close()
		}
		backend.chain.Stop()
	})
	return backend
}

func (b *testBackend) setPendingBlock(block *types.Block) {
	b.pending = block
}

func (b testBackend) SyncProgress() qrl.SyncProgress { return qrl.SyncProgress{} }
func (b testBackend) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	return big.NewInt(0), nil
}
func (b testBackend) FeeHistory(ctx context.Context, blockCount uint64, lastBlock rpc.BlockNumber, rewardPercentiles []float64) (*big.Int, [][]*big.Int, []*big.Int, []float64, error) {
	return nil, nil, nil, nil, nil
}
func (b testBackend) ChainDb() qrldb.Database           { return b.db }
func (b testBackend) AccountManager() *accounts.Manager { return nil }
func (b testBackend) ExtRPCEnabled() bool               { return false }
func (b testBackend) RPCGasCap() uint64                 { return 10000000 }
func (b testBackend) RPCQRVMTimeout() time.Duration     { return time.Second }
func (b testBackend) RPCTxFeeCap() float64              { return 0 }
func (b testBackend) SetHead(number uint64)             {}
func (b testBackend) HeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Header, error) {
	if number == rpc.LatestBlockNumber {
		return b.chain.CurrentBlock(), nil
	}
	if number == rpc.PendingBlockNumber && b.pending != nil {
		return b.pending.Header(), nil
	}
	return b.chain.GetHeaderByNumber(uint64(number)), nil
}
func (b testBackend) HeaderByHash(ctx context.Context, hash common.Hash) (*types.Header, error) {
	return b.chain.GetHeaderByHash(hash), nil
}
func (b testBackend) HeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.Header, error) {
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return b.HeaderByNumber(ctx, blockNr)
	}
	if blockHash, ok := blockNrOrHash.Hash(); ok {
		return b.HeaderByHash(ctx, blockHash)
	}
	panic("unknown type rpc.BlockNumberOrHash")
}
func (b testBackend) CurrentHeader() *types.Header { return b.chain.CurrentBlock() }
func (b testBackend) CurrentBlock() *types.Header  { return b.chain.CurrentBlock() }
func (b testBackend) BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Block, error) {
	if number == rpc.LatestBlockNumber {
		head := b.chain.CurrentBlock()
		return b.chain.GetBlock(head.Hash(), head.Number.Uint64()), nil
	}
	if number == rpc.PendingBlockNumber {
		return b.pending, nil
	}
	return b.chain.GetBlockByNumber(uint64(number)), nil
}
func (b testBackend) BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error) {
	return b.chain.GetBlockByHash(hash), nil
}
func (b testBackend) BlockByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*types.Block, error) {
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return b.BlockByNumber(ctx, blockNr)
	}
	if blockHash, ok := blockNrOrHash.Hash(); ok {
		return b.BlockByHash(ctx, blockHash)
	}
	panic("unknown type rpc.BlockNumberOrHash")
}
func (b testBackend) GetBody(ctx context.Context, hash common.Hash, number rpc.BlockNumber) (*types.Body, error) {
	return b.chain.GetBlock(hash, uint64(number.Int64())).Body(), nil
}
func (b testBackend) StateAndHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (*state.StateDB, *types.Header, error) {
	if number == rpc.PendingBlockNumber {
		panic("pending state not implemented")
	}
	header, err := b.HeaderByNumber(ctx, number)
	if err != nil {
		return nil, nil, err
	}
	if header == nil {
		return nil, nil, errors.New("header not found")
	}
	stateDb, err := b.chain.StateAt(header.Root)
	return stateDb, header, err
}
func (b testBackend) StateAndHeaderByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash) (*state.StateDB, *types.Header, error) {
	if blockNr, ok := blockNrOrHash.Number(); ok {
		return b.StateAndHeaderByNumber(ctx, blockNr)
	}
	panic("only implemented for number")
}
func (b testBackend) Pending() (*types.Block, types.Receipts, *state.StateDB) { panic("implement me") }
func (b testBackend) GetReceipts(ctx context.Context, hash common.Hash) (types.Receipts, error) {
	header, err := b.HeaderByHash(ctx, hash)
	if header == nil || err != nil {
		return nil, err
	}
	receipts := rawdb.ReadReceipts(b.db, hash, header.Number.Uint64(), header.Time, b.chain.Config())
	return receipts, nil
}

func (b testBackend) GetQRVM(ctx context.Context, msg *core.Message, state *state.StateDB, header *types.Header, vmConfig *vm.Config, blockContext *vm.BlockContext) *vm.QRVM {
	if vmConfig == nil {
		vmConfig = b.chain.GetVMConfig()
	}
	txContext := core.NewQRVMTxContext(msg)
	context := core.NewQRVMBlockContext(header, b.chain, nil)
	if blockContext != nil {
		context = *blockContext
	}
	return vm.NewQRVM(context, txContext, state, b.chain.Config(), *vmConfig)
}
func (b testBackend) SubscribeChainEvent(ch chan<- core.ChainEvent) event.Subscription {
	panic("implement me")
}
func (b testBackend) SubscribeChainHeadEvent(ch chan<- core.ChainHeadEvent) event.Subscription {
	panic("implement me")
}
func (b testBackend) SubscribeChainSideEvent(ch chan<- core.ChainSideEvent) event.Subscription {
	panic("implement me")
}
func (b testBackend) SendTx(ctx context.Context, signedTx *types.Transaction) error {
	return b.txpool.Add([]*types.Transaction{signedTx}, true, false)[0]
}
func (b testBackend) GetTransaction(ctx context.Context, txHash common.Hash) (*types.Transaction, common.Hash, uint64, uint64, error) {
	tx, blockHash, blockNumber, index := rawdb.ReadTransaction(b.db, txHash)
	return tx, blockHash, blockNumber, index, nil
}
func (b testBackend) GetPoolTransactions() (types.Transactions, error) {
	pending := b.txpool.Pending(txpool.PendingFilter{})
	var txs types.Transactions
	for _, batch := range pending {
		for _, lazy := range batch {
			if tx := lazy.Resolve(); tx != nil {
				txs = append(txs, tx)
			}
		}
	}
	return txs, nil
}
func (b testBackend) GetPoolTransaction(txHash common.Hash) *types.Transaction {
	return b.txpool.Get(txHash)
}
func (b testBackend) GetPoolNonce(ctx context.Context, addr common.Address) (uint64, error) {
	return b.txpool.Nonce(addr), nil
}
func (b testBackend) Stats() (pending int, queued int) { return b.txpool.Stats() }
func (b testBackend) TxPoolContent() (map[common.Address][]*types.Transaction, map[common.Address][]*types.Transaction) {
	return b.txpool.Content()
}
func (b testBackend) TxPoolContentFrom(addr common.Address) ([]*types.Transaction, []*types.Transaction) {
	return b.txpool.ContentFrom(addr)
}
func (b testBackend) SubscribeNewTxsEvent(events chan<- core.NewTxsEvent) event.Subscription {
	return b.txpool.SubscribeTransactions(events)
}
func (b testBackend) ChainConfig() *params.ChainConfig { return b.chain.Config() }
func (b testBackend) Engine() consensus.Engine         { return b.chain.Engine() }
func (b testBackend) GetLogs(ctx context.Context, blockHash common.Hash, number uint64) ([][]*types.Log, error) {
	panic("implement me")
}
func (b testBackend) SubscribeRemovedLogsEvent(ch chan<- core.RemovedLogsEvent) event.Subscription {
	panic("implement me")
}
func (b testBackend) SubscribeLogsEvent(ch chan<- []*types.Log) event.Subscription {
	panic("implement me")
}
func (b testBackend) BloomStatus() (uint64, uint64) { panic("implement me") }
func (b testBackend) ServiceFilter(ctx context.Context, session *bloombits.MatcherSession) {
	panic("implement me")
}

func TestEstimateGas(t *testing.T) {
	t.Parallel()
	// Initialize test accounts
	var (
		accounts = newAccounts(2)
		genesis  = &core.Genesis{
			Config: params.TestChainConfig,
			Alloc: core.GenesisAlloc{
				accounts[0].addr: {Balance: big.NewInt(params.Quanta)},
				accounts[1].addr: {Balance: big.NewInt(params.Quanta)},
			},
		}
		genBlocks      = 10
		signer         = types.ZondSigner{ChainId: big.NewInt(1)}
		randomAccounts = newAccounts(2)
	)
	api := NewBlockChainAPI(newTestBackend(t, genBlocks, genesis, beacon.NewFaker(), func(i int, b *core.BlockGen) {
		// Transfer from account[0] to account[1]
		//    value: 1000 planck
		//    fee:   0 planck
		tx, _ := types.SignTx(types.NewTx(&types.DynamicFeeTx{Nonce: uint64(i), To: &accounts[1].addr, Value: big.NewInt(1000), Gas: params.TxGas, GasFeeCap: b.BaseFee(), Data: nil}), signer, accounts[0].wallet)
		b.AddTx(tx)
	}))
	var testSuite = []struct {
		blockNumber rpc.BlockNumber
		call        TransactionArgs
		overrides   StateOverride
		expectErr   error
		want        uint64
	}{
		// simple transfer on latest block
		{
			blockNumber: rpc.LatestBlockNumber,
			call: TransactionArgs{
				From:  &accounts[0].addr,
				To:    &accounts[1].addr,
				Value: (*hexutil.Big)(big.NewInt(1000)),
			},
			expectErr: nil,
			want:      21000,
		},
		// simple transfer with insufficient funds on latest block
		{
			blockNumber: rpc.LatestBlockNumber,
			call: TransactionArgs{
				From:  &randomAccounts[0].addr,
				To:    &accounts[1].addr,
				Value: (*hexutil.Big)(big.NewInt(1000)),
			},
			expectErr: core.ErrInsufficientFunds,
			want:      21000,
		},
		// empty create
		{
			blockNumber: rpc.LatestBlockNumber,
			call:        TransactionArgs{},
			expectErr:   nil,
			want:        53000,
		},
		{
			blockNumber: rpc.LatestBlockNumber,
			call:        TransactionArgs{},
			overrides: StateOverride{
				randomAccounts[0].addr: OverrideAccount{Balance: newRPCBalance(new(big.Int).Mul(big.NewInt(1), big.NewInt(params.Quanta)))},
			},
			expectErr: nil,
			want:      53000,
		},
		{
			blockNumber: rpc.LatestBlockNumber,
			call: TransactionArgs{
				From:  &randomAccounts[0].addr,
				To:    &randomAccounts[1].addr,
				Value: (*hexutil.Big)(big.NewInt(1000)),
			},
			overrides: StateOverride{
				randomAccounts[0].addr: OverrideAccount{Balance: newRPCBalance(big.NewInt(0))},
			},
			expectErr: core.ErrInsufficientFunds,
		},
	}
	for i, tc := range testSuite {
		result, err := api.EstimateGas(t.Context(), tc.call, &rpc.BlockNumberOrHash{BlockNumber: &tc.blockNumber}, &tc.overrides)
		if tc.expectErr != nil {
			if err == nil {
				t.Errorf("test %d: want error %v, have nothing", i, tc.expectErr)
				continue
			}
			if !errors.Is(err, tc.expectErr) {
				t.Errorf("test %d: error mismatch, want %v, have %v", i, tc.expectErr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("test %d: want no error, have %v", i, err)
			continue
		}
		if uint64(result) != tc.want {
			t.Errorf("test %d, result mismatch, have\n%v\n, want\n%v\n", i, uint64(result), tc.want)
		}
	}
}

func TestCall(t *testing.T) {
	t.Parallel()
	// Initialize test accounts
	var (
		accounts = newAccounts(3)
		genesis  = &core.Genesis{
			Config: params.TestChainConfig,
			Alloc: core.GenesisAlloc{
				accounts[0].addr: {Balance: big.NewInt(params.Quanta)},
				accounts[1].addr: {Balance: big.NewInt(params.Quanta)},
				accounts[2].addr: {Balance: big.NewInt(params.Quanta)},
			},
		}
		genBlocks = 10
		signer    = types.ZondSigner{ChainId: big.NewInt(1)}
	)
	api := NewBlockChainAPI(newTestBackend(t, genBlocks, genesis, beacon.NewFaker(), func(i int, b *core.BlockGen) {
		// Transfer from account[0] to account[1]
		//    value: 1000 planck
		//    fee:   0 planck
		tx, _ := types.SignTx(types.NewTx(&types.DynamicFeeTx{Nonce: uint64(i), To: &accounts[1].addr, Value: big.NewInt(1000), Gas: params.TxGas, GasFeeCap: b.BaseFee(), Data: nil}), signer, accounts[0].wallet)
		b.AddTx(tx)
	}))
	randomAccounts := newAccounts(3)
	var testSuite = []struct {
		blockNumber    rpc.BlockNumber
		overrides      StateOverride
		call           TransactionArgs
		blockOverrides BlockOverrides
		expectErr      error
		want           string
	}{
		// transfer on genesis
		{
			blockNumber: rpc.BlockNumber(0),
			call: TransactionArgs{
				From:  &accounts[0].addr,
				To:    &accounts[1].addr,
				Value: (*hexutil.Big)(big.NewInt(1000)),
			},
			expectErr: nil,
			want:      "0x",
		},
		// transfer on the head
		{
			blockNumber: rpc.BlockNumber(genBlocks),
			call: TransactionArgs{
				From:  &accounts[0].addr,
				To:    &accounts[1].addr,
				Value: (*hexutil.Big)(big.NewInt(1000)),
			},
			expectErr: nil,
			want:      "0x",
		},
		// transfer on a non-existent block, error expects
		{
			blockNumber: rpc.BlockNumber(genBlocks + 1),
			call: TransactionArgs{
				From:  &accounts[0].addr,
				To:    &accounts[1].addr,
				Value: (*hexutil.Big)(big.NewInt(1000)),
			},
			expectErr: errors.New("header not found"),
		},
		// transfer on the latest block
		{
			blockNumber: rpc.LatestBlockNumber,
			call: TransactionArgs{
				From:  &accounts[0].addr,
				To:    &accounts[1].addr,
				Value: (*hexutil.Big)(big.NewInt(1000)),
			},
			expectErr: nil,
			want:      "0x",
		},
		// Call which can only succeed if state is state overridden
		{
			blockNumber: rpc.LatestBlockNumber,
			call: TransactionArgs{
				From:  &randomAccounts[0].addr,
				To:    &randomAccounts[1].addr,
				Value: (*hexutil.Big)(big.NewInt(1000)),
			},
			overrides: StateOverride{
				randomAccounts[0].addr: OverrideAccount{Balance: newRPCBalance(new(big.Int).Mul(big.NewInt(1), big.NewInt(params.Quanta)))},
			},
			want: "0x",
		},
		// Invalid call without state overriding
		{
			blockNumber: rpc.LatestBlockNumber,
			call: TransactionArgs{
				From:  &randomAccounts[0].addr,
				To:    &randomAccounts[1].addr,
				Value: (*hexutil.Big)(big.NewInt(1000)),
			},
			expectErr: core.ErrInsufficientFunds,
		},
		// Successful simple contract call using only opcodes stable across
		// the 512-bit-word shift. The original test relied on a Solidity
		// storage-slot fixture; the new bytecode loads slot 0 and returns
		// its low 32 bytes.
		//   54       SLOAD   ; push storage[0]
		//   60 00    PUSH1 0 ; memory offset
		//   52       MSTORE  ; write 64 bytes of (0...value) at mem[0]
		//   60 20    PUSH1 32 ; return length
		//   60 20    PUSH1 32 ; return offset 32 (skip leading 32 zero bytes)
		//   f3       RETURN
		{
			blockNumber: rpc.LatestBlockNumber,
			call: TransactionArgs{
				From: &randomAccounts[0].addr,
				To:   &randomAccounts[2].addr,
			},
			overrides: StateOverride{
				randomAccounts[2].addr: OverrideAccount{
					Code:      hex2Bytes("60005460005260206020f3"),
					StateDiff: &map[common.Hash]common.StorageValue64{{}: common.BytesToStorageValue64(common.BigToHash(big.NewInt(123)).Bytes())},
				},
			},
			want: "0x000000000000000000000000000000000000000000000000000000000000007b",
		},
		// Block overrides should work. MSTORE now writes a full 64-byte
		// word, so the RETURN offset moves to 32 to pick up the low half.
		{
			blockNumber: rpc.LatestBlockNumber,
			call: TransactionArgs{
				From: &accounts[1].addr,
				Input: &hexutil.Bytes{
					0x43,             // NUMBER
					0x60, 0x00, 0x52, // PUSH1 0, MSTORE
					0x60, 0x20, 0x60, 0x20, 0xf3, // PUSH1 32, PUSH1 32, RETURN
				},
			},
			blockOverrides: BlockOverrides{Number: (*hexutil.Big)(big.NewInt(11))},
			want:           "0x000000000000000000000000000000000000000000000000000000000000000b",
		},
	}
	for i, tc := range testSuite {
		result, err := api.Call(t.Context(), tc.call, rpc.BlockNumberOrHash{BlockNumber: &tc.blockNumber}, &tc.overrides, &tc.blockOverrides)
		if tc.expectErr != nil {
			if err == nil {
				t.Errorf("test %d: want error %v, have nothing", i, tc.expectErr)
				continue
			}
			if !errors.Is(err, tc.expectErr) {
				// Second try
				if !reflect.DeepEqual(err, tc.expectErr) {
					t.Errorf("test %d: error mismatch, want %v, have %v", i, tc.expectErr, err)
				}
			}
			continue
		}
		if err != nil {
			t.Errorf("test %d: want no error, have %v", i, err)
			continue
		}
		if !reflect.DeepEqual(result.String(), tc.want) {
			t.Errorf("test %d, result mismatch, have\n%v\n, want\n%v\n", i, result.String(), tc.want)
		}
	}
}

type Account struct {
	wallet wallet.Wallet
	addr   common.Address
}

func newAccounts(n int) (accounts []Account) {
	for range n {
		wallet, _ := wallet.Generate(wallet.ML_DSA_87)
		accounts = append(accounts, Account{wallet: wallet, addr: wallet.GetAddress()})
	}
	slices.SortFunc(accounts, func(a, b Account) int { return a.addr.Cmp(b.addr) })
	return accounts
}

func newRPCBalance(balance *big.Int) **hexutil.Big {
	rpcBalance := (*hexutil.Big)(balance)
	return &rpcBalance
}

func hex2Bytes(str string) *hexutil.Bytes {
	rpcBytes := hexutil.Bytes(common.Hex2Bytes(str))
	return &rpcBytes
}

func TestRPCMarshalBlock(t *testing.T) {
	t.Parallel()
	var (
		txs []*types.Transaction
		to  = common.BytesToAddress([]byte{0x11})
	)
	for i := uint64(1); i <= 4; i++ {
		var tx *types.Transaction
		if i%2 == 0 {
			tx = types.NewTx(&types.DynamicFeeTx{
				Nonce:     i,
				GasFeeCap: big.NewInt(11111),
				Gas:       1111,
				To:        &to,
				Value:     big.NewInt(111),
				Data:      []byte{0x11, 0x11, 0x11},
			})
		} else {
			tx = types.NewTx(&types.DynamicFeeTx{
				ChainID:   big.NewInt(1337),
				Nonce:     i,
				GasFeeCap: big.NewInt(11111),
				Gas:       1111,
				To:        &to,
				Value:     big.NewInt(111),
				Data:      []byte{0x11, 0x11, 0x11},
			})
		}
		txs = append(txs, tx)
	}
	block := types.NewBlock(&types.Header{Number: big.NewInt(100)}, &types.Body{Transactions: txs}, nil, blocktest.NewHasher())

	var testSuite = []struct {
		inclTx bool
		fullTx bool
	}{
		// without txs
		{
			inclTx: false,
			fullTx: false,
		},

		// only tx hashes
		{
			inclTx: true,
			fullTx: false,
		},
		// full tx details
		{
			inclTx: true,
			fullTx: true,
		},
	}

	for i, tc := range testSuite {
		resp := RPCMarshalBlock(block, tc.inclTx, tc.fullTx, params.MainnetChainConfig)
		out, err := json.Marshal(resp)
		if err != nil {
			t.Errorf("test %d: json marshal error: %v", i, err)
			continue
		}
		var back map[string]any
		if err := json.Unmarshal(out, &back); err != nil {
			t.Errorf("test %d: json unmarshal error: %v", i, err)
			continue
		}
		if have, want := back["hash"], block.Hash().Hex(); have != want {
			t.Errorf("test %d: block hash mismatch: have %v, want %v", i, have, want)
		}
	}
}

func TestRPCGetBlockOrHeader(t *testing.T) {
	t.Parallel()

	// Initialize test accounts
	var (
		acc1Wallet                = testutil.MustLoadAccount("bob").MustWallet()
		acc2Wallet                = testutil.MustLoadAccount("carol").MustWallet()
		acc1Addr                  = acc1Wallet.GetAddress()
		acc2Addr   common.Address = acc2Wallet.GetAddress()
		genesis                   = &core.Genesis{
			Config: params.TestChainConfig,
			Alloc: core.GenesisAlloc{
				acc1Addr: {Balance: big.NewInt(params.Quanta)},
				acc2Addr: {Balance: big.NewInt(params.Quanta)},
			},
		}
		genBlocks = 10
		signer    = types.ZondSigner{ChainId: big.NewInt(1)}
		tx        = types.NewTx(&types.DynamicFeeTx{
			Nonce:     11,
			GasFeeCap: big.NewInt(params.InitialBaseFee),
			Gas:       1111,
			To:        &acc2Addr,
			Value:     big.NewInt(111),
			Data:      []byte{0x11, 0x11, 0x11},
		})
		withdrawal = &types.Withdrawal{
			Index:     0,
			Validator: 1,
			Address:   common.Address{0x12, 0x34},
			Amount:    10,
		}
		pending = types.NewBlock(&types.Header{Number: big.NewInt(11), Time: 42}, &types.Body{Transactions: types.Transactions{tx}, Withdrawals: types.Withdrawals{withdrawal}}, nil, blocktest.NewHasher())
	)
	backend := newTestBackend(t, genBlocks, genesis, beacon.NewFaker(), func(i int, b *core.BlockGen) {
		// Transfer from account[0] to account[1]
		//    value: 1000 planck
		//    fee:   0 planck
		tx, _ := types.SignTx(types.NewTx(&types.DynamicFeeTx{Nonce: uint64(i), To: &acc2Addr, Value: big.NewInt(1000), Gas: params.TxGas, GasFeeCap: b.BaseFee(), Data: nil}), signer, acc1Wallet)
		b.AddTx(tx)
	})
	backend.setPendingBlock(pending)
	api := NewBlockChainAPI(backend)
	blockHashes := make([]common.Hash, genBlocks+1)
	for i := 0; i <= genBlocks; i++ {
		header, err := backend.HeaderByNumber(t.Context(), rpc.BlockNumber(i))
		if err != nil {
			t.Errorf("failed to get block: %d err: %v", i, err)
		}
		blockHashes[i] = header.Hash()
	}
	pendingHash := pending.Hash()

	var testSuite = []struct {
		blockNumber rpc.BlockNumber
		blockHash   *common.Hash
		fullTx      bool
		reqHeader   bool
		file        string
		expectErr   error
	}{
		// 0. latest header
		{
			blockNumber: rpc.LatestBlockNumber,
			reqHeader:   true,
			file:        "tag-latest",
		},
		// 1. genesis header
		{
			blockNumber: rpc.BlockNumber(0),
			reqHeader:   true,
			file:        "number-0",
		},
		// 2. #1 header
		{
			blockNumber: rpc.BlockNumber(1),
			reqHeader:   true,
			file:        "number-1",
		},
		// 3. latest-1 header
		{
			blockNumber: rpc.BlockNumber(9),
			reqHeader:   true,
			file:        "number-latest-1",
		},
		// 4. latest+1 header
		{
			blockNumber: rpc.BlockNumber(11),
			reqHeader:   true,
			file:        "number-latest+1",
		},
		// 5. pending header
		{
			blockNumber: rpc.PendingBlockNumber,
			reqHeader:   true,
			file:        "tag-pending",
		},
		// 6. latest block
		{
			blockNumber: rpc.LatestBlockNumber,
			file:        "tag-latest",
		},
		// 7. genesis block
		{
			blockNumber: rpc.BlockNumber(0),
			file:        "number-0",
		},
		// 8. #1 block
		{
			blockNumber: rpc.BlockNumber(1),
			file:        "number-1",
		},
		// 9. latest-1 block
		{
			blockNumber: rpc.BlockNumber(9),
			fullTx:      true,
			file:        "number-latest-1",
		},
		// 10. latest+1 block
		{
			blockNumber: rpc.BlockNumber(11),
			fullTx:      true,
			file:        "number-latest+1",
		},
		// 11. pending block
		{
			blockNumber: rpc.PendingBlockNumber,
			file:        "tag-pending",
		},
		// 12. pending block + fullTx
		{
			blockNumber: rpc.PendingBlockNumber,
			fullTx:      true,
			file:        "tag-pending-fullTx",
		},
		// 13. latest header by hash
		{
			blockHash: &blockHashes[len(blockHashes)-1],
			reqHeader: true,
			file:      "hash-latest",
		},
		// 14. genesis header by hash
		{
			blockHash: &blockHashes[0],
			reqHeader: true,
			file:      "hash-0",
		},
		// 15. #1 header
		{
			blockHash: &blockHashes[1],
			reqHeader: true,
			file:      "hash-1",
		},
		// 16. latest-1 header
		{
			blockHash: &blockHashes[len(blockHashes)-2],
			reqHeader: true,
			file:      "hash-latest-1",
		},
		// 17. empty hash
		{
			blockHash: &common.Hash{},
			reqHeader: true,
			file:      "hash-empty",
		},
		// 18. pending hash
		{
			blockHash: &pendingHash,
			reqHeader: true,
			file:      `hash-pending`,
		},
		// 19. latest block
		{
			blockHash: &blockHashes[len(blockHashes)-1],
			file:      "hash-latest",
		},
		// 20. genesis block
		{
			blockHash: &blockHashes[0],
			file:      "hash-genesis",
		},
		// 21. #1 block
		{
			blockHash: &blockHashes[1],
			file:      "hash-1",
		},
		// 22. latest-1 block
		{
			blockHash: &blockHashes[len(blockHashes)-2],
			fullTx:    true,
			file:      "hash-latest-1-fullTx",
		},
		// 23. empty hash + body
		{
			blockHash: &common.Hash{},
			fullTx:    true,
			file:      "hash-empty-fullTx",
		},
		// 24. pending block
		{
			blockHash: &pendingHash,
			file:      `hash-pending`,
		},
		// 25. pending block + fullTx
		{
			blockHash: &pendingHash,
			fullTx:    true,
			file:      "hash-pending-fullTx",
		},
	}

	for i, tt := range testSuite {
		var (
			result map[string]any
			err    error
			rpc    string
		)
		if tt.blockHash != nil {
			if tt.reqHeader {
				result = api.GetHeaderByHash(t.Context(), *tt.blockHash)
				rpc = "qrl_getHeaderByHash"
			} else {
				result, err = api.GetBlockByHash(t.Context(), *tt.blockHash, tt.fullTx)
				rpc = "qrl_getBlockByHash"
			}
		} else {
			if tt.reqHeader {
				result, err = api.GetHeaderByNumber(t.Context(), tt.blockNumber)
				rpc = "qrl_getHeaderByNumber"
			} else {
				result, err = api.GetBlockByNumber(t.Context(), tt.blockNumber, tt.fullTx)
				rpc = "qrl_getBlockByNumber"
			}
		}
		if tt.expectErr != nil {
			if err == nil {
				t.Errorf("test %d: want error %v, have nothing", i, tt.expectErr)
				continue
			}
			if !errors.Is(err, tt.expectErr) {
				t.Errorf("test %d: error mismatch, want %v, have %v", i, tt.expectErr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("test %d: want no error, have %v", i, err)
			continue
		}

		testRPCResponseWithFile(t, i, result, rpc, tt.file)
	}
}

func setupReceiptBackend(t *testing.T, genBlocks int) (*testBackend, []common.Hash) {
	config := *params.TestChainConfig
	var (
		acc1Wallet                = testutil.MustLoadAccount("bob").MustWallet()
		acc2Wallet                = testutil.MustLoadAccount("carol").MustWallet()
		acc1Addr                  = acc1Wallet.GetAddress()
		acc2Addr   common.Address = acc2Wallet.GetAddress()
		contract                  = common.MustParseAddress("Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000031ec7")
		genesis                   = &core.Genesis{
			Config: &config,
			Alloc: core.GenesisAlloc{
				acc1Addr: {Balance: big.NewInt(params.Quanta)},
				acc2Addr: {Balance: big.NewInt(params.Quanta)},
				// VM64 test log contract: every call emits LOG2 with two
				// 64-byte topics and one 64-byte data word.
				contract: {Balance: big.NewInt(params.Quanta), Code: receiptLogContractCode()},
			},
		}
		signer   = types.LatestSignerForChainID(params.TestChainConfig.ChainID)
		txHashes = make([]common.Hash, genBlocks)
	)

	backend := newTestBackend(t, genBlocks, genesis, beacon.New(), func(i int, b *core.BlockGen) {
		var (
			tx  *types.Transaction
			err error
		)
		switch i {
		case 0:
			// transfer 1000planck
			tx, err = types.SignTx(types.NewTx(&types.DynamicFeeTx{Nonce: uint64(i), To: &acc2Addr, Value: big.NewInt(1000), Gas: params.TxGas, GasFeeCap: b.BaseFee(), Data: nil}), types.ZondSigner{ChainId: big.NewInt(1)}, acc1Wallet)
		case 1:
			// create contract
			tx, err = types.SignTx(types.NewTx(&types.DynamicFeeTx{Nonce: uint64(i), To: nil, Gas: 53100, GasFeeCap: b.BaseFee(), Data: common.FromHex("0x60806040")}), signer, acc1Wallet)
		case 2:
			// with logs
			tx, err = types.SignTx(types.NewTx(&types.DynamicFeeTx{Nonce: uint64(i), To: &contract, Gas: 60000, GasFeeCap: b.BaseFee(), Data: receiptLogCallData(byte(i + 11))}), signer, acc1Wallet)
		case 3:
			// dynamic fee with logs
			fee := big.NewInt(500)
			fee.Add(fee, b.BaseFee())
			tx, err = types.SignTx(types.NewTx(&types.DynamicFeeTx{Nonce: uint64(i), To: &contract, Gas: 60000, Value: big.NewInt(1), GasTipCap: big.NewInt(500), GasFeeCap: fee, Data: receiptLogCallData(byte(i + 11))}), signer, acc1Wallet)
		case 4:
			// access list with contract create
			accessList := types.AccessList{{
				Address:     contract,
				StorageKeys: []common.Hash{{0}},
			}}
			tx, err = types.SignTx(types.NewTx(&types.DynamicFeeTx{Nonce: uint64(i), To: nil, Gas: 58100, GasFeeCap: b.BaseFee(), Data: common.FromHex("0x60806040"), AccessList: accessList}), signer, acc1Wallet)
		case 5:
			// dynamic fee tx
			fee := big.NewInt(500)
			fee.Add(fee, b.BaseFee())
			tx, err = types.SignTx(types.NewTx(&types.DynamicFeeTx{
				Nonce:     uint64(i),
				GasTipCap: big.NewInt(1),
				GasFeeCap: fee,
				Gas:       params.TxGas,
				To:        &acc2Addr,
				Value:     big.NewInt(0),
			}), signer, acc1Wallet)
		}
		if err != nil {
			t.Errorf("failed to sign tx: %v", err)
		}
		if tx != nil {
			b.AddTx(tx)
			txHashes[i] = tx.Hash()
		}
	})
	return backend, txHashes
}

func TestRPCGetTransactionReceipt(t *testing.T) {
	t.Parallel()

	var (
		backend, txHashes = setupReceiptBackend(t, 6)
		api               = NewTransactionAPI(backend, new(AddrLocker))
	)

	var testSuite = []struct {
		txHash common.Hash
		file   string
	}{
		// 0. normal success
		{
			txHash: txHashes[0],
			file:   "normal-transfer-tx",
		},
		// 1. create contract
		{
			txHash: txHashes[1],
			file:   "create-contract-tx",
		},
		// 2. with logs success
		{
			txHash: txHashes[2],
			file:   "with-logs",
		},
		// 3. dynamic tx with logs success
		{
			txHash: txHashes[3],
			file:   `dynamic-tx-with-logs`,
		},
		// 4. access list tx with create contract
		{
			txHash: txHashes[4],
			file:   "create-contract-with-access-list",
		},
		// 5. txhash empty
		{
			txHash: common.Hash{},
			file:   "txhash-empty",
		},
		// 6. txhash not found
		{
			txHash: common.HexToHash("deadbeef"),
			file:   "txhash-notfound",
		},
	}

	for i, tt := range testSuite {
		var (
			result any
			err    error
		)
		result, err = api.GetTransactionReceipt(t.Context(), tt.txHash)
		if err != nil {
			t.Errorf("test %d: want no error, have %v", i, err)
			continue
		}
		testRPCResponseWithFile(t, i, result, "qrl_getTransactionReceipt", tt.file)
	}
}

func TestSendRawTransactionRejectsNonEmptyExtraParams(t *testing.T) {
	t.Parallel()

	wallet, err := wallet.Generate(wallet.ML_DSA_87)
	if err != nil {
		t.Fatalf("wallet: %v", err)
	}
	addr := common.Address(wallet.GetAddress())
	genesis := &core.Genesis{
		Config: params.TestChainConfig,
		Alloc: core.GenesisAlloc{
			addr: {Balance: big.NewInt(params.Quanta)},
		},
	}
	backend := newTestBackend(t, 0, genesis, beacon.NewFaker(), nil)
	api := NewTransactionAPI(backend, new(AddrLocker))
	signer := types.LatestSignerForChainID(params.TestChainConfig.ChainID)

	tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{
		Nonce:     0,
		GasTipCap: big.NewInt(0),
		GasFeeCap: big.NewInt(params.InitialBaseFee),
		Gas:       params.TxGas,
		To:        &common.Address{},
		Value:     big.NewInt(0),
	}), signer, wallet)
	if err != nil {
		t.Fatalf("sign tx: %v", err)
	}
	tampered, err := tx.WithAuthValues(signer, tx.RawSignatureValue(), tx.RawPublicKeyValue(), tx.Descriptor(), []byte{0x01})
	if err != nil {
		t.Fatalf("re-wrap with extra params: %v", err)
	}
	raw, err := tampered.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal tx: %v", err)
	}

	_, err = api.SendRawTransaction(t.Context(), raw)
	if !errors.Is(err, txpool.ErrInvalidSender) {
		t.Fatalf("expected invalid sender, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "non-empty extraParams not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRPCGetBlockReceipts(t *testing.T) {
	t.Parallel()

	var (
		genBlocks  = 6
		backend, _ = setupReceiptBackend(t, genBlocks)
		api        = NewBlockChainAPI(backend)
	)
	blockHashes := make([]common.Hash, genBlocks+1)
	for i := 0; i <= genBlocks; i++ {
		header, err := backend.HeaderByNumber(t.Context(), rpc.BlockNumber(i))
		if err != nil {
			t.Errorf("failed to get block: %d err: %v", i, err)
		}
		blockHashes[i] = header.Hash()
	}

	var testSuite = []struct {
		test rpc.BlockNumberOrHash
		file string
	}{
		// 0. block without any txs(hash)
		{
			test: rpc.BlockNumberOrHashWithHash(blockHashes[0], false),
			file: "number-0",
		},
		// 1. block without any txs(number)
		{
			test: rpc.BlockNumberOrHashWithNumber(0),
			file: "number-1",
		},
		// 2. earliest tag
		{
			test: rpc.BlockNumberOrHashWithNumber(rpc.EarliestBlockNumber),
			file: "tag-earliest",
		},
		// 3. latest tag
		{
			test: rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber),
			file: "tag-latest",
		},
		// 4. block with transfer tx(hash)
		{
			test: rpc.BlockNumberOrHashWithHash(blockHashes[1], false),
			file: "block-with-transfer-tx",
		},
		// 5. block with contract create tx(number)
		{
			test: rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(2)),
			file: "block-with-contract-create-tx",
		},
		// 6. block with contract call tx(hash)
		{
			test: rpc.BlockNumberOrHashWithHash(blockHashes[3], false),
			file: "block-with-contract-call-tx",
		},
		// 7. block with dynamic fee tx(number)
		{
			test: rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(4)),
			file: "block-with-dynamic-fee-tx",
		},
		// 8. block is empty
		{
			test: rpc.BlockNumberOrHashWithHash(common.Hash{}, false),
			file: "hash-empty",
		},
		// 9. block is not found
		{
			test: rpc.BlockNumberOrHashWithHash(common.HexToHash("deadbeef"), false),
			file: "hash-notfound",
		},
		// 10. block is not found
		{
			test: rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(genBlocks + 1)),
			file: "block-notfound",
		},
	}

	for i, tt := range testSuite {
		var (
			result any
			err    error
		)
		result, err = api.GetBlockReceipts(t.Context(), tt.test)
		if err != nil {
			t.Errorf("test %d: want no error, have %v", i, err)
			continue
		}
		testRPCResponseWithFile(t, i, result, "qrl_getBlockReceipts", tt.file)
	}
}

func testRPCResponseWithFile(t *testing.T, testid int, result any, rpc string, file string) {
	data, err := json.Marshal(result)
	if err != nil {
		t.Errorf("test %d: json marshal error", testid)
		return
	}
	var normalizedResult any
	if err := json.Unmarshal(data, &normalizedResult); err != nil {
		t.Errorf("test %d: json unmarshal error", testid)
		return
	}
	normalizedResult = normalizeRPCSnapshot(normalizedResult)
	data, err = json.MarshalIndent(normalizedResult, "", "  ")
	if err != nil {
		t.Errorf("test %d: json marshal error", testid)
		return
	}
	outputFile := filepath.Join("testdata", fmt.Sprintf("%s-%s.json", rpc, file))
	if os.Getenv("WRITE_TEST_FILES") != "" {
		os.WriteFile(outputFile, data, 0644)
	}
	want, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("error reading expected test file: %s output: %v", outputFile, err)
	}
	require.JSONEqf(t, string(want), string(data), "test %d: json not match, want: %s, have: %s", testid, string(want), string(data))
}

func normalizeRPCSnapshot(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			if isVolatileRPCSnapshotHash(k) {
				out[k] = "<volatile-hash>"
				continue
			}
			if k == "transactions" {
				out[k] = normalizeRPCSnapshotTransactions(v)
				continue
			}
			out[k] = normalizeRPCSnapshot(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = normalizeRPCSnapshot(v)
		}
		return out
	default:
		return v
	}
}

func normalizeRPCSnapshotTransactions(v any) any {
	txs, ok := v.([]any)
	if !ok {
		return normalizeRPCSnapshot(v)
	}
	out := make([]any, len(txs))
	for i, tx := range txs {
		if _, ok := tx.(string); ok {
			out[i] = "<volatile-hash>"
			continue
		}
		out[i] = normalizeRPCSnapshot(tx)
	}
	return out
}

func isVolatileRPCSnapshotHash(k string) bool {
	switch k {
	case "hash", "blockHash", "parentHash", "transactionHash", "transactionsRoot", "raw", "signature":
		return true
	default:
		return false
	}
}
