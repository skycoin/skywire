package packetfilter

import (
	"net"

	"github.com/SkycoinProject/skycoin/src/util/logging"
)

type AddressFilter struct {
	log   *logging.Logger
	addr  net.Addr
	match bool // TODO: remove
}

func NewAddressFilter(addr net.Addr, match bool) *AddressFilter {
	return &AddressFilter{
		log:   logging.MustGetLogger("address-filter"),
		addr:  addr,
		match: match,
	}
}

func (f *AddressFilter) ClaimIncoming(in []byte, addr net.Addr) bool {
	if f.match {
		ret := addr.String() == f.addr.String()
		f.log.Infof("[AddressFilter] f.match = %v, addr = %q, f.addr = %q, ret = %v", f.match, addr.String(), f.addr.String(), ret)
		return ret
	}

	ret := addr.String() != f.addr.String()
	f.log.Infof("[AddressFilter] f.match = %v, addr = %q, f.addr = %q, ret = %v", f.match, addr.String(), f.addr.String(), ret)
	return ret
}

func (f *AddressFilter) Outgoing(out []byte, addr net.Addr) {
	return
}
