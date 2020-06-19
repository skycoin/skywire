package packetfilter

import (
	"net"

	"github.com/SkycoinProject/skycoin/src/util/logging"
)

var log = logging.MustGetLogger("packetfilter")

type AddressFilter struct {
	addr  net.Addr
	match bool
}

func NewAddressFilter(addr net.Addr, match bool) *AddressFilter {
	return &AddressFilter{
		addr:  addr,
		match: match,
	}
}

func (f *AddressFilter) ClaimIncoming(in []byte, addr net.Addr) bool {
	if f.match {
		ret := addr.String() == f.addr.String()
		log.Infof("[AddressFilter] f.match = %v, addr = %q, f.addr = %q, ret = %v", f.match, addr.String(), f.addr.String(), ret)
		return ret
	}

	ret := addr.String() != f.addr.String()
	log.Infof("[AddressFilter] f.match = %v, addr = %q, f.addr = %q, ret = %v", f.match, addr.String(), f.addr.String(), ret)
	return ret
}

func (f *AddressFilter) Outgoing(out []byte, addr net.Addr) {
	return
}
