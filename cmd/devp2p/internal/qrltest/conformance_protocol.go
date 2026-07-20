// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.

package qrltest

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/p2p"
	"github.com/theQRL/go-qrl/p2p/qnode"
	"github.com/theQRL/go-qrl/p2p/rlpx"
	"github.com/theQRL/go-qrl/qrl/protocols/qrl"
	"github.com/theQRL/go-qrl/qrl/protocols/snap"
	"github.com/theQRL/go-qrl/rlp"
)

const (
	handshakeMsg = 0x00
	discMsg      = 0x01
	pingMsg      = 0x02
	pongMsg      = 0x03

	baseProtoLen = uint64(16)
	qrlProtoLen  = uint64(qrl.ReceiptsMsg + 1)
	snapProtoLen = uint64(snap.TrieNodesMsg + 1)

	wireTimeout = 10 * time.Second
)

type proto uint8

const (
	baseProto proto = iota
	qrlProto
	snapProto
)

func protoOffset(protocol proto) uint64 {
	switch protocol {
	case baseProto:
		return 0
	case qrlProto:
		return baseProtoLen
	case snapProto:
		return baseProtoLen + qrlProtoLen
	default:
		panic("unknown devp2p protocol")
	}
}

func protocolForCode(code uint64) (proto, uint64, error) {
	switch {
	case code < baseProtoLen:
		return baseProto, code, nil
	case code < baseProtoLen+qrlProtoLen:
		return qrlProto, code - baseProtoLen, nil
	case code < baseProtoLen+qrlProtoLen+snapProtoLen:
		return snapProto, code - baseProtoLen - qrlProtoLen, nil
	default:
		return 0, 0, fmt.Errorf("message code %d is outside negotiated qrl/snap ranges", code)
	}
}

type protoHandshake struct {
	Version    uint64
	Name       string
	Caps       []p2p.Cap
	ListenPort uint64
	ID         []byte
	Rest       []rlp.RawValue `rlp:"tail"`
}

// Hello is exported for the rlpx ping command.
type Hello = protoHandshake

type Conn struct {
	*rlpx.Conn
	key         *ecdsa.PrivateKey
	caps        []p2p.Cap
	qrlVersion  uint
	snapVersion uint
	requireSnap bool
	remoteHello protoHandshake
}

func dialNode(dest *qnode.Node, withSnap bool) (*Conn, error) {
	if dest == nil || dest.IP() == nil || dest.TCP() == 0 || dest.Pubkey() == nil {
		return nil, errors.New("destination must contain IP, TCP port, and secp256k1 public key")
	}
	endpoint := net.JoinHostPort(dest.IP().String(), strconv.Itoa(dest.TCP()))
	fd, err := net.DialTimeout("tcp", endpoint, wireTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", endpoint, err)
	}
	key, err := crypto.GenerateKey()
	if err != nil {
		fd.Close()
		return nil, fmt.Errorf("generate RLPx identity: %w", err)
	}
	conn := &Conn{
		Conn:        rlpx.NewConn(fd, dest.Pubkey()),
		key:         key,
		requireSnap: withSnap,
		caps:        []p2p.Cap{{Name: qrl.ProtocolName, Version: qrl.QRL1}},
	}
	if withSnap {
		conn.caps = append(conn.caps, p2p.Cap{Name: snap.ProtocolName, Version: snap.SNAP1})
	}
	conn.SetDeadline(time.Now().Add(wireTimeout))
	if _, err := conn.Handshake(key); err != nil {
		conn.Close()
		return nil, fmt.Errorf("RLPx authentication handshake: %w", err)
	}
	if err := conn.devp2pHandshake(); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (c *Conn) devp2pHandshake() error {
	publicKey := crypto.FromECDSAPub(&c.key.PublicKey)
	if len(publicKey) != 65 {
		return fmt.Errorf("unexpected secp256k1 public key length %d", len(publicKey))
	}
	hello := &protoHandshake{
		Version: 5,
		Name:    "go-qrl-devp2p-conformance",
		Caps:    c.caps,
		ID:      publicKey[1:],
	}
	if err := c.write(baseProto, handshakeMsg, hello); err != nil {
		return fmt.Errorf("send devp2p hello: %w", err)
	}
	code, payload, err := c.readRaw()
	if err != nil {
		return fmt.Errorf("read devp2p hello: %w", err)
	}
	if code == discMsg {
		return decodeDisconnect(payload)
	}
	if code != handshakeMsg {
		return fmt.Errorf("first devp2p message has code %d, want hello code %d", code, handshakeMsg)
	}
	if err := rlp.DecodeBytes(payload, &c.remoteHello); err != nil {
		return fmt.Errorf("decode devp2p hello: %w", err)
	}
	if c.remoteHello.Version < 5 {
		return fmt.Errorf("remote devp2p version %d does not support required snappy framing", c.remoteHello.Version)
	}
	c.SetSnappy(true)
	c.negotiate(c.remoteHello.Caps)
	if c.qrlVersion != qrl.QRL1 {
		return fmt.Errorf("remote caps %v do not negotiate %s/%d", c.remoteHello.Caps, qrl.ProtocolName, qrl.QRL1)
	}
	if c.requireSnap && c.snapVersion != snap.SNAP1 {
		return fmt.Errorf("remote caps %v do not negotiate %s/%d", c.remoteHello.Caps, snap.ProtocolName, snap.SNAP1)
	}
	return nil
}

func (c *Conn) negotiate(remote []p2p.Cap) {
	for _, capability := range remote {
		switch capability.Name {
		case qrl.ProtocolName:
			if capability.Version == qrl.QRL1 {
				c.qrlVersion = capability.Version
			}
		case snap.ProtocolName:
			if c.requireSnap && capability.Version == snap.SNAP1 {
				c.snapVersion = capability.Version
			}
		}
	}
}

func (c *Conn) statusExchange(chain *Chain) error {
	status := &qrl.StatusPacket{
		ProtocolVersion: uint32(c.qrlVersion),
		NetworkID:       chain.Config().ChainID.Uint64(),
		Head:            chain.Head().Hash(),
		Genesis:         chain.Genesis().Hash(),
		ForkID:          chain.ForkID(),
	}
	if err := c.write(qrlProto, qrl.StatusMsg, status); err != nil {
		return fmt.Errorf("send qrl status: %w", err)
	}
	packet, err := c.readQRL()
	if err != nil {
		return fmt.Errorf("read qrl status: %w", err)
	}
	remote, ok := packet.(*qrl.StatusPacket)
	if !ok {
		return fmt.Errorf("first qrl packet is %T, want *qrl.StatusPacket", packet)
	}
	if remote.ProtocolVersion != status.ProtocolVersion {
		return fmt.Errorf("remote protocol version %d, want %d", remote.ProtocolVersion, status.ProtocolVersion)
	}
	if remote.NetworkID != status.NetworkID {
		return fmt.Errorf("remote network ID %d, want %d", remote.NetworkID, status.NetworkID)
	}
	if remote.Genesis != status.Genesis {
		return fmt.Errorf("remote genesis %x, want %x", remote.Genesis, status.Genesis)
	}
	if remote.Head != status.Head {
		return fmt.Errorf("remote head %x, want fixture block %d hash %x", remote.Head, chain.Head().NumberU64(), status.Head)
	}
	if remote.ForkID != status.ForkID {
		return fmt.Errorf("remote fork ID %v, want %v", remote.ForkID, status.ForkID)
	}
	return nil
}

func (c *Conn) write(protocol proto, code uint64, packet any) error {
	c.SetWriteDeadline(time.Now().Add(wireTimeout))
	payload, err := rlp.EncodeToBytes(packet)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}
	if _, err := c.Conn.Write(protoOffset(protocol)+code, payload); err != nil {
		return err
	}
	return nil
}

func (c *Conn) readRaw() (uint64, []byte, error) {
	c.SetReadDeadline(time.Now().Add(wireTimeout))
	code, payload, _, err := c.Conn.Read()
	return code, payload, err
}

func (c *Conn) readProtocol(want proto) (uint64, []byte, error) {
	for {
		code, payload, err := c.readRaw()
		if err != nil {
			return 0, nil, err
		}
		protocol, subcode, err := protocolForCode(code)
		if err != nil {
			return 0, nil, err
		}
		if protocol == baseProto {
			switch subcode {
			case pingMsg:
				if err := c.write(baseProto, pongMsg, []any{}); err != nil {
					return 0, nil, fmt.Errorf("reply to ping: %w", err)
				}
				continue
			case discMsg:
				return 0, nil, decodeDisconnect(payload)
			default:
				return 0, nil, fmt.Errorf("unexpected base protocol message code %d", subcode)
			}
		}
		if protocol != want {
			continue
		}
		return subcode, payload, nil
	}
}

func decodeDisconnect(payload []byte) error {
	var reasons []p2p.DiscReason
	if err := rlp.DecodeBytes(payload, &reasons); err != nil {
		return fmt.Errorf("decode disconnect: %w", err)
	}
	if len(reasons) == 0 {
		return errors.New("remote sent disconnect without a reason")
	}
	return fmt.Errorf("remote disconnected: %v", reasons[0])
}

func (c *Conn) readQRL() (any, error) {
	code, payload, err := c.readProtocol(qrlProto)
	if err != nil {
		return nil, err
	}
	var packet any
	switch code {
	case qrl.StatusMsg:
		packet = new(qrl.StatusPacket)
	case qrl.TransactionsMsg:
		packet = new(qrl.TransactionsPacket)
	case qrl.GetBlockHeadersMsg:
		packet = new(qrl.GetBlockHeadersPacket)
	case qrl.BlockHeadersMsg:
		packet = new(qrl.BlockHeadersPacket)
	case qrl.GetBlockBodiesMsg:
		packet = new(qrl.GetBlockBodiesPacket)
	case qrl.BlockBodiesMsg:
		packet = new(qrl.BlockBodiesPacket)
	case qrl.NewPooledTransactionHashesMsg:
		packet = new(qrl.NewPooledTransactionHashesPacket)
	case qrl.GetPooledTransactionsMsg:
		packet = new(qrl.GetPooledTransactionsPacket)
	case qrl.PooledTransactionsMsg:
		packet = new(qrl.PooledTransactionsPacket)
	case qrl.GetReceiptsMsg:
		packet = new(qrl.GetReceiptsPacket)
	case qrl.ReceiptsMsg:
		packet = new(qrl.ReceiptsPacket)
	default:
		return nil, fmt.Errorf("unsupported qrl message code %d", code)
	}
	if err := rlp.DecodeBytes(payload, packet); err != nil {
		return nil, fmt.Errorf("decode qrl message code %d: %w", code, err)
	}
	return packet, nil
}

func (c *Conn) readSnap() (any, error) {
	code, payload, err := c.readProtocol(snapProto)
	if err != nil {
		return nil, err
	}
	var packet any
	switch code {
	case snap.GetAccountRangeMsg:
		packet = new(snap.GetAccountRangePacket)
	case snap.AccountRangeMsg:
		packet = new(snap.AccountRangePacket)
	case snap.GetStorageRangesMsg:
		packet = new(snap.GetStorageRangesPacket)
	case snap.StorageRangesMsg:
		packet = new(snap.StorageRangesPacket)
	case snap.GetByteCodesMsg:
		packet = new(snap.GetByteCodesPacket)
	case snap.ByteCodesMsg:
		packet = new(snap.ByteCodesPacket)
	case snap.GetTrieNodesMsg:
		packet = new(snap.GetTrieNodesPacket)
	case snap.TrieNodesMsg:
		packet = new(snap.TrieNodesPacket)
	default:
		return nil, fmt.Errorf("unsupported snap message code %d", code)
	}
	if err := rlp.DecodeBytes(payload, packet); err != nil {
		return nil, fmt.Errorf("decode snap message code %d: %w", code, err)
	}
	return packet, nil
}
