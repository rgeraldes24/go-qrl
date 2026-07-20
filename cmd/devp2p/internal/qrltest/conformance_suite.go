// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.

package qrltest

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/internal/utesting"
	"github.com/theQRL/go-qrl/p2p/qnode"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/qrl/protocols/qrl"
	"github.com/theQRL/go-qrl/rlp"
)

// Suite runs wire-level conformance checks against Dest. fullChain contains
// future fixture blocks, while chain is the verified prefix expected to be
// imported into the node under test.
type Suite struct {
	Dest *qnode.Node

	chain     *Chain
	fullChain *Chain
}

func NewSuite(dest *qnode.Node, chainFile, genesisFile string) (*Suite, error) {
	full, err := loadChain(chainFile, genesisFile)
	if err != nil {
		return nil, err
	}
	target, err := selectTargetChain(full, chainFile, genesisFile)
	if err != nil {
		return nil, err
	}
	return &Suite{Dest: dest, chain: target, fullChain: full}, nil
}

func (s *Suite) QRLTests() []utesting.Test {
	return []utesting.Test{
		{Name: "HandshakeAndStatus", Fn: s.TestHandshakeAndStatus},
		{Name: "BlockHeaderRetrieval", Fn: s.TestBlockHeaderRetrieval},
		{Name: "BlockBodyRetrieval", Fn: s.TestBlockBodyRetrieval},
		{Name: "ReceiptRetrieval", Fn: s.TestReceiptRetrieval},
		{Name: "PooledTransactionPropagation", Fn: s.TestPooledTransactionPropagation},
	}
}

func (s *Suite) connect(withSnap bool) (*Conn, error) {
	conn, err := dialNode(s.Dest, withSnap)
	if err != nil {
		return nil, err
	}
	if err := conn.statusExchange(s.chain); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (s *Suite) TestHandshakeAndStatus(t *utesting.T) {
	conn, err := s.connect(false)
	if err != nil {
		t.Fatalf("QRL handshake/status failed: %v", err)
	}
	defer conn.Close()
	if conn.qrlVersion != qrl.QRL1 {
		t.Fatalf("negotiated qrl/%d, want qrl/%d", conn.qrlVersion, qrl.QRL1)
	}
	if len(conn.remoteHello.ID) != 64 {
		t.Fatalf("remote devp2p node ID has %d bytes, want 64", len(conn.remoteHello.ID))
	}
	t.Logf("negotiated qrl/%d with %s at fixture head %d", conn.qrlVersion, conn.remoteHello.Name, s.chain.Head().NumberU64())
}

func (s *Suite) TestBlockHeaderRetrieval(t *utesting.T) {
	conn, err := s.connect(false)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	if s.chain.Len() < 4 {
		t.Fatalf("header retrieval fixture has only %d blocks", s.chain.Len())
	}
	head := s.chain.Head().NumberU64()
	forwardOrigin := uint64(1)
	if head > 5 {
		forwardOrigin = head - 5
	}
	requests := []*qrl.GetBlockHeadersPacket{
		{
			RequestId: 0x514c0001,
			GetBlockHeadersRequest: &qrl.GetBlockHeadersRequest{
				Origin: qrl.HashOrNumber{Number: forwardOrigin},
				Amount: 3,
				Skip:   1,
			},
		},
		{
			RequestId: 0x514c0002,
			GetBlockHeadersRequest: &qrl.GetBlockHeadersRequest{
				Origin:  qrl.HashOrNumber{Hash: s.chain.Head().Hash()},
				Amount:  3,
				Reverse: true,
			},
		},
	}
	expected := make(map[uint64][]*types.Header, len(requests))
	for _, request := range requests {
		headers, err := s.chain.GetHeaders(request)
		if err != nil {
			t.Fatalf("compute expected headers: %v", err)
		}
		expected[request.RequestId] = headers
		if err := conn.write(qrlProto, qrl.GetBlockHeadersMsg, request); err != nil {
			t.Fatalf("send header request %d: %v", request.RequestId, err)
		}
	}
	seen := make(map[uint64]bool, len(requests))
	for len(seen) < len(requests) {
		packet, err := conn.readQRL()
		if err != nil {
			t.Fatalf("read header response: %v", err)
		}
		response, ok := packet.(*qrl.BlockHeadersPacket)
		if !ok {
			t.Fatalf("received %T while waiting for block headers", packet)
		}
		want, ok := expected[response.RequestId]
		if !ok {
			t.Fatalf("response has unknown request ID %d", response.RequestId)
		}
		if seen[response.RequestId] {
			t.Fatalf("duplicate response for request ID %d", response.RequestId)
		}
		if err := compareHeaders(want, response.BlockHeadersRequest); err != nil {
			t.Fatalf("request ID %d: %v", response.RequestId, err)
		}
		seen[response.RequestId] = true
	}
}

func compareHeaders(want, got []*types.Header) error {
	if len(got) != len(want) {
		return fmt.Errorf("got %d headers, want %d", len(got), len(want))
	}
	for i := range want {
		wantRLP, err := rlp.EncodeToBytes(want[i])
		if err != nil {
			return err
		}
		gotRLP, err := rlp.EncodeToBytes(got[i])
		if err != nil {
			return err
		}
		if !bytes.Equal(gotRLP, wantRLP) {
			return fmt.Errorf("header %d differs (got hash %x, want %x)", i, got[i].Hash(), want[i].Hash())
		}
		if len(got[i].Coinbase[:]) != common.AddressLength {
			return fmt.Errorf("header %d coinbase has %d bytes, want %d", i, len(got[i].Coinbase[:]), common.AddressLength)
		}
	}
	return nil
}

func (s *Suite) TestBlockBodyRetrieval(t *utesting.T) {
	conn, err := s.connect(false)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	blocks := []*types.Block{s.chain.blocks[1]}
	if head := s.chain.Head(); head.Hash() != blocks[0].Hash() {
		blocks = append(blocks, head)
	}
	hashes := make(qrl.GetBlockBodiesRequest, len(blocks))
	for i, block := range blocks {
		hashes[i] = block.Hash()
	}
	request := &qrl.GetBlockBodiesPacket{RequestId: 0x514c1001, GetBlockBodiesRequest: hashes}
	if err := conn.write(qrlProto, qrl.GetBlockBodiesMsg, request); err != nil {
		t.Fatalf("send body request: %v", err)
	}
	packet, err := conn.readQRL()
	if err != nil {
		t.Fatalf("read body response: %v", err)
	}
	response, ok := packet.(*qrl.BlockBodiesPacket)
	if !ok {
		t.Fatalf("received %T while waiting for block bodies", packet)
	}
	if response.RequestId != request.RequestId {
		t.Fatalf("body response ID %d, want %d", response.RequestId, request.RequestId)
	}
	if len(response.BlockBodiesResponse) != len(blocks) {
		t.Fatalf("received %d block bodies, want %d", len(response.BlockBodiesResponse), len(blocks))
	}
	for i, block := range blocks {
		want := &qrl.BlockBody{Transactions: block.Transactions(), Withdrawals: block.Withdrawals()}
		wantRLP, err := rlp.EncodeToBytes(want)
		if err != nil {
			t.Fatalf("encode expected body %d: %v", i, err)
		}
		gotRLP, err := rlp.EncodeToBytes(response.BlockBodiesResponse[i])
		if err != nil {
			t.Fatalf("encode returned body %d: %v", i, err)
		}
		if !bytes.Equal(gotRLP, wantRLP) {
			t.Fatalf("body %d for block %x differs from fixture", i, block.Hash())
		}
	}
}

func (s *Suite) TestReceiptRetrieval(t *utesting.T) {
	conn, err := s.connect(false)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	blocks := []*types.Block{s.chain.blocks[1]}
	if head := s.chain.Head(); head.Hash() != blocks[0].Hash() {
		blocks = append(blocks, head)
	}
	hashes := make(qrl.GetReceiptsRequest, len(blocks))
	for i, block := range blocks {
		if block.ReceiptHash() != types.EmptyReceiptsHash {
			t.Fatalf("fixture block %d has non-empty receipt root without receipt fixture data", block.NumberU64())
		}
		hashes[i] = block.Hash()
	}
	request := &qrl.GetReceiptsPacket{RequestId: 0x514c1801, GetReceiptsRequest: hashes}
	if err := conn.write(qrlProto, qrl.GetReceiptsMsg, request); err != nil {
		t.Fatalf("send receipt request: %v", err)
	}
	packet, err := conn.readQRL()
	if err != nil {
		t.Fatalf("read receipt response: %v", err)
	}
	response, ok := packet.(*qrl.ReceiptsPacket)
	if !ok {
		t.Fatalf("received %T while waiting for receipts", packet)
	}
	if response.RequestId != request.RequestId {
		t.Fatalf("receipt response ID %d, want %d", response.RequestId, request.RequestId)
	}
	if len(response.ReceiptsResponse) != len(blocks) {
		t.Fatalf("received receipt sets for %d blocks, want %d", len(response.ReceiptsResponse), len(blocks))
	}
	for i, receipts := range response.ReceiptsResponse {
		if len(receipts) != 0 {
			t.Fatalf("block %d returned %d receipts, want empty fixture receipts", blocks[i].NumberU64(), len(receipts))
		}
	}
}

func (s *Suite) TestPooledTransactionPropagation(t *utesting.T) {
	future, err := makeFixtureTransactions(s.chain, 2)
	if err != nil {
		t.Fatalf("create funded fixture transactions: %v", err)
	}
	for i, transaction := range future {
		if err := validateVM64Transaction(s.fullChain.Config(), transaction); err != nil {
			t.Fatalf("future transaction %d: %v", i, err)
		}
	}
	source, err := s.connect(false)
	if err != nil {
		t.Fatalf("connect source peer: %v", err)
	}
	defer source.Close()
	observer, err := s.connect(false)
	if err != nil {
		t.Fatalf("connect observer peer: %v", err)
	}
	defer observer.Close()

	// Full transaction ingress must result in an exact announcement to a
	// different peer.
	if err := source.write(qrlProto, qrl.TransactionsMsg, qrl.TransactionsPacket{future[0]}); err != nil {
		t.Fatalf("send full transaction: %v", err)
	}
	if err := waitForTransactionAnnouncement(observer, future[0]); err != nil {
		t.Fatalf("full transaction was not propagated: %v", err)
	}

	// The announced transaction must be retrievable, with the request ID and
	// complete transaction bytes preserved.
	request := &qrl.GetPooledTransactionsPacket{
		RequestId:                    0x514c2001,
		GetPooledTransactionsRequest: qrl.GetPooledTransactionsRequest{future[0].Hash()},
	}
	if err := observer.write(qrlProto, qrl.GetPooledTransactionsMsg, request); err != nil {
		t.Fatalf("request pooled transaction: %v", err)
	}
	if err := waitForPooledTransaction(observer, request.RequestId, future[0]); err != nil {
		t.Fatalf("pooled transaction retrieval failed: %v", err)
	}

	// Hash-only ingress must trigger an exact GetPooledTransactions request;
	// replying with the body must then propagate the second transaction.
	announcement := &qrl.NewPooledTransactionHashesPacket{
		Types:  []byte{future[1].Type()},
		Sizes:  []uint32{uint32(future[1].Size())},
		Hashes: []common.Hash{future[1].Hash()},
	}
	if err := source.write(qrlProto, qrl.NewPooledTransactionHashesMsg, announcement); err != nil {
		t.Fatalf("announce pooled transaction hash: %v", err)
	}
	bodyRequest, err := waitForPooledTransactionRequest(source, future[1].Hash())
	if err != nil {
		t.Fatalf("node did not request announced transaction: %v", err)
	}
	body := &qrl.PooledTransactionsPacket{
		RequestId:                  bodyRequest.RequestId,
		PooledTransactionsResponse: qrl.PooledTransactionsResponse{future[1]},
	}
	if err := source.write(qrlProto, qrl.PooledTransactionsMsg, body); err != nil {
		t.Fatalf("reply with pooled transaction: %v", err)
	}
	if err := waitForTransactionAnnouncement(observer, future[1]); err != nil {
		t.Fatalf("hash-announced transaction body was not propagated: %v", err)
	}
}

const faucetSeedHex = "010000b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f29100000000000000000000000000000000"

func makeFixtureTransactions(chain *Chain, count int) ([]*types.Transaction, error) {
	if count < 1 {
		return nil, errors.New("transaction count must be positive")
	}
	faucet, err := wallet.RestoreFromSeedHex(faucetSeedHex)
	if err != nil {
		return nil, fmt.Errorf("restore fixture faucet: %w", err)
	}
	faucetAddress := common.Address(faucet.GetAddress())
	allocation, ok := chain.genesis.Alloc[faucetAddress]
	if !ok {
		return nil, fmt.Errorf("fixture faucet %s is not funded by genesis", faucetAddress)
	}
	for _, block := range chain.blocks[1:] {
		if len(block.Transactions()) != 0 {
			return nil, fmt.Errorf("fixture block %d contains transactions; faucet nonce cannot be inferred without executing the chain", block.NumberU64())
		}
	}
	recipient := common.Address{0: 0xa5, common.AddressLength - 1: 0x5a}
	tip := big.NewInt(1)
	feeCap := big.NewInt(1)
	if baseFee := chain.Head().BaseFee(); baseFee != nil {
		feeCap.Mul(baseFee, big.NewInt(2)).Add(feeCap, tip)
	}
	signer := types.LatestSigner(chain.Config())
	transactions := make([]*types.Transaction, count)
	for i := range transactions {
		unsigned := types.NewTx(&types.DynamicFeeTx{
			ChainID:   new(big.Int).Set(chain.Config().ChainID),
			Nonce:     allocation.Nonce + uint64(i),
			GasTipCap: new(big.Int).Set(tip),
			GasFeeCap: new(big.Int).Set(feeCap),
			Gas:       params.TxGas,
			To:        &recipient,
			Value:     big.NewInt(1),
		})
		signed, err := types.SignTx(unsigned, signer, faucet)
		if err != nil {
			return nil, fmt.Errorf("sign fixture transaction %d: %w", i, err)
		}
		transactions[i] = signed
	}
	return transactions, nil
}

func validateVM64Transaction(config *params.ChainConfig, transaction *types.Transaction) error {
	if transaction == nil {
		return errors.New("nil transaction")
	}
	sender, err := types.Sender(types.LatestSigner(config), transaction)
	if err != nil {
		return fmt.Errorf("recover sender: %w", err)
	}
	if len(sender[:]) != common.AddressLength {
		return fmt.Errorf("sender has %d bytes, want %d", len(sender[:]), common.AddressLength)
	}
	wideAddress := false
	for _, b := range sender[:common.AddressLength-common.HashLength] {
		if b != 0 {
			wideAddress = true
			break
		}
	}
	if recipient := transaction.To(); recipient != nil {
		if len(recipient[:]) != common.AddressLength {
			return fmt.Errorf("recipient has %d bytes, want %d", len(recipient[:]), common.AddressLength)
		}
		for _, b := range recipient[:common.AddressLength-common.HashLength] {
			if b != 0 {
				wideAddress = true
				break
			}
		}
	}
	if !wideAddress {
		return errors.New("transaction does not exercise the high 32 bytes of a VM64 sender or recipient")
	}
	hash := transaction.Hash()
	if len(hash[:]) != common.HashLength {
		return fmt.Errorf("transaction hash has %d bytes, want %d", len(hash[:]), common.HashLength)
	}
	return nil
}

func waitForTransactionAnnouncement(conn *Conn, want *types.Transaction) error {
	for range 24 {
		packet, err := conn.readQRL()
		if err != nil {
			return err
		}
		switch message := packet.(type) {
		case *qrl.NewPooledTransactionHashesPacket:
			if len(message.Hashes) != len(message.Types) || len(message.Hashes) != len(message.Sizes) {
				return fmt.Errorf("malformed announcement lengths hashes=%d types=%d sizes=%d", len(message.Hashes), len(message.Types), len(message.Sizes))
			}
			for i, hash := range message.Hashes {
				if hash == want.Hash() {
					if message.Types[i] != want.Type() || message.Sizes[i] != uint32(want.Size()) {
						return fmt.Errorf("announcement metadata type=%d size=%d, want type=%d size=%d", message.Types[i], message.Sizes[i], want.Type(), want.Size())
					}
					return nil
				}
			}
		case *qrl.TransactionsPacket:
			for _, transaction := range *message {
				if transaction.Hash() == want.Hash() {
					return compareTransactions(want, transaction)
				}
			}
		}
	}
	return fmt.Errorf("transaction %x absent after 24 QRL messages", want.Hash())
}

func waitForPooledTransaction(conn *Conn, requestID uint64, want *types.Transaction) error {
	for range 24 {
		packet, err := conn.readQRL()
		if err != nil {
			return err
		}
		response, ok := packet.(*qrl.PooledTransactionsPacket)
		if !ok || response.RequestId != requestID {
			continue
		}
		if len(response.PooledTransactionsResponse) != 1 {
			return fmt.Errorf("response contains %d transactions, want 1", len(response.PooledTransactionsResponse))
		}
		return compareTransactions(want, response.PooledTransactionsResponse[0])
	}
	return fmt.Errorf("no pooled transaction response for request ID %d", requestID)
}

func waitForPooledTransactionRequest(conn *Conn, hash common.Hash) (*qrl.GetPooledTransactionsPacket, error) {
	for range 24 {
		packet, err := conn.readQRL()
		if err != nil {
			return nil, err
		}
		request, ok := packet.(*qrl.GetPooledTransactionsPacket)
		if !ok {
			continue
		}
		if len(request.GetPooledTransactionsRequest) != 1 || request.GetPooledTransactionsRequest[0] != hash {
			return nil, fmt.Errorf("requested hashes %v, want only %x", request.GetPooledTransactionsRequest, hash)
		}
		return request, nil
	}
	return nil, fmt.Errorf("no GetPooledTransactions request for %x", hash)
}

func compareTransactions(want, got *types.Transaction) error {
	wantBytes, err := want.MarshalBinary()
	if err != nil {
		return err
	}
	gotBytes, err := got.MarshalBinary()
	if err != nil {
		return err
	}
	if !bytes.Equal(gotBytes, wantBytes) || got.Hash() != want.Hash() {
		return fmt.Errorf("transaction bytes differ (got %x, want %x)", got.Hash(), want.Hash())
	}
	return nil
}
