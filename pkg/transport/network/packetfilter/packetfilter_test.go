// Package packetfilter pkg/transport/network/packetfilter/packetfilter_test.go:
// unit tests for the address and KCP-conversation pfilter implementations.
package packetfilter

import (
	"encoding/binary"
	"net"
	"testing"

	"github.com/xtaci/kcp-go"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/logging"
)

// kcpPacket builds a minimal KCP packet carrying conversation id and command.
func kcpPacket(id uint32, cmd byte) []byte {
	pkt := make([]byte, minPacketLen)
	binary.LittleEndian.PutUint32(pkt[:packetTypeOffset], id)
	pkt[packetTypeOffset] = cmd
	return pkt
}

// TestAddressFilterClaimIncoming verifies an incoming packet is only claimed
// when its source address matches the configured one.
func TestAddressFilterClaimIncoming(t *testing.T) {
	mLog := logging.NewMasterLogger()
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
	f := NewAddressFilter(addr, mLog)

	t.Run("match", func(t *testing.T) {
		same := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
		assert.True(t, f.ClaimIncoming(nil, same))
	})

	t.Run("no match", func(t *testing.T) {
		other := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4321}
		assert.False(t, f.ClaimIncoming(nil, other))
	})

	// Outgoing is a no-op; just exercise it.
	f.Outgoing(nil, addr)
}

// TestKCPConversationFilterOutgoingLearnsID verifies Outgoing records the
// conversation id from a valid KCP packet so matching incoming packets are
// later claimed.
func TestKCPConversationFilterOutgoingLearnsID(t *testing.T) {
	mLog := logging.NewMasterLogger()
	f := NewKCPConversationFilter(mLog)

	const convID = uint32(42)

	// Before any outgoing packet, the learned id is 0 and nothing is claimed.
	assert.False(t, f.ClaimIncoming(kcpPacket(convID, kcp.IKCP_CMD_PUSH), nil))

	// Learn the id from an outgoing packet.
	f.Outgoing(kcpPacket(convID, kcp.IKCP_CMD_PUSH), nil)

	// Incoming with the same id is claimed.
	assert.True(t, f.ClaimIncoming(kcpPacket(convID, kcp.IKCP_CMD_ACK), nil))

	// Incoming with a different id is not.
	assert.False(t, f.ClaimIncoming(kcpPacket(convID+1, kcp.IKCP_CMD_ACK), nil))
}

// TestKCPConversationFilterClaimIncomingNonKCP verifies packets that are not
// KCP conversations are never claimed.
func TestKCPConversationFilterClaimIncomingNonKCP(t *testing.T) {
	mLog := logging.NewMasterLogger()
	f := NewKCPConversationFilter(mLog)

	f.Outgoing(kcpPacket(7, kcp.IKCP_CMD_PUSH), nil)

	t.Run("too short", func(t *testing.T) {
		assert.False(t, f.ClaimIncoming([]byte{1, 2, 3}, nil))
	})

	t.Run("command out of range low", func(t *testing.T) {
		assert.False(t, f.ClaimIncoming(kcpPacket(7, kcp.IKCP_CMD_PUSH-1), nil))
	})

	t.Run("command out of range high", func(t *testing.T) {
		assert.False(t, f.ClaimIncoming(kcpPacket(7, kcp.IKCP_CMD_WINS+1), nil))
	})
}

// TestKCPConversationFilterOutgoingIgnoresNonKCP verifies Outgoing does not
// learn an id from a non-KCP packet.
func TestKCPConversationFilterOutgoingIgnoresNonKCP(t *testing.T) {
	mLog := logging.NewMasterLogger()
	f := NewKCPConversationFilter(mLog)

	// A non-KCP outgoing packet must not set the conversation id.
	f.Outgoing([]byte{1, 2, 3}, nil)
	assert.False(t, f.ClaimIncoming(kcpPacket(0, kcp.IKCP_CMD_PUSH), nil))

	// A valid command but too-short packet is also ignored.
	f.Outgoing([]byte{0, 0, 0, 0}, nil)
	assert.False(t, f.ClaimIncoming(kcpPacket(0, kcp.IKCP_CMD_PUSH), nil))
}

// TestKCPConversationFilterBoundaryCommands verifies the inclusive command
// range bounds are accepted.
func TestKCPConversationFilterBoundaryCommands(t *testing.T) {
	mLog := logging.NewMasterLogger()

	for _, cmd := range []byte{kcp.IKCP_CMD_PUSH, kcp.IKCP_CMD_WINS} {
		f := NewKCPConversationFilter(mLog)
		const convID = uint32(99)
		f.Outgoing(kcpPacket(convID, cmd), nil)
		assert.True(t, f.ClaimIncoming(kcpPacket(convID, cmd), nil),
			"command %d should be treated as a KCP conversation", cmd)
	}
}
