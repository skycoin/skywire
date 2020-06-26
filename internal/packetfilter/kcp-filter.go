package packetfilter

import (
	"encoding/binary"
	"net"
	"sync/atomic"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/xtaci/kcp-go"
)

const (
	minPacketLen     = 5
	packetTypeOffset = 4
)

type KCPConversationFilter struct {
	log *logging.Logger
	id  uint32
}

func NewKCPConversationFilter() *KCPConversationFilter {
	return &KCPConversationFilter{
		log: logging.MustGetLogger("kcp-filter"),
	}
}

func (f *KCPConversationFilter) ClaimIncoming(in []byte, _ net.Addr) bool {
	if !f.isKCPConversation(in) {
		return false
	}
	expectedID := atomic.LoadUint32(&f.id)
	receivedID := binary.LittleEndian.Uint32(in[:packetTypeOffset])

	return expectedID != 0 && expectedID == receivedID
}

func (f *KCPConversationFilter) Outgoing(out []byte, _ net.Addr) {
	if f.isKCPConversation(out) && len(out) >= minPacketLen {
		id := binary.LittleEndian.Uint32(out[:packetTypeOffset])
		atomic.StoreUint32(&f.id, id)
	}
}

func (f *KCPConversationFilter) isKCPConversation(data []byte) bool {
	return len(data) >= minPacketLen &&
		data[packetTypeOffset] >= kcp.IKCP_CMD_PUSH &&
		data[packetTypeOffset] <= kcp.IKCP_CMD_WINS
}
