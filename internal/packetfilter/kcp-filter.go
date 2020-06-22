package packetfilter

import (
	"encoding/binary"
	"net"
	"sync/atomic"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/xtaci/kcp-go"
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

func (f *KCPConversationFilter) ClaimIncoming(in []byte, addr net.Addr) bool {
	if f.isKCPConversation(in) {
		expectedID := atomic.LoadUint32(&f.id)
		receivedID := binary.LittleEndian.Uint32(in[:4])

		ret := expectedID != 0 && expectedID == receivedID
		f.log.Infof("[KCPConversationFilter ClaimIncoming] addr = %q, isKCP = %v, expected = %v, received = %v, ret = %v",
			addr, f.isKCPConversation(in), atomic.LoadUint32(&f.id), binary.LittleEndian.Uint32(in[:4]), ret)

		return ret
	}
	f.log.Infof("[KCPConversationFilter ClaimIncoming] isKCP = false, ret = false")

	return false
}

func (f *KCPConversationFilter) Outgoing(out []byte, addr net.Addr) {
	if f.isKCPConversation(out) {
		id := binary.LittleEndian.Uint32(out[:4])
		atomic.StoreUint32(&f.id, id)

		f.log.Infof("[KCPConversationFilter Outgoing] isKCP = true, id = %v, addr = %v, ", id, addr)
	}
	f.log.Infof("[KCPConversationFilter Outgoing] isKCP = false, addr = %v", addr)
}

func (f *KCPConversationFilter) isKCPConversation(data []byte) bool {
	f.log.Infof("[isKCPConversation] len = %v, data[4] = %v", len(data), data[4])
	return len(data) >= 5 && data[4] >= kcp.IKCP_CMD_PUSH && data[4] <= kcp.IKCP_CMD_WINS
}
