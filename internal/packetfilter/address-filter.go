package packetfilter

import (
	"net"

	"github.com/SkycoinProject/skycoin/src/util/logging"
)

type AddressFilter struct {
	log  *logging.Logger
	addr net.Addr
}

func NewAddressFilter(addr net.Addr) *AddressFilter {
	return &AddressFilter{
		log:  logging.MustGetLogger("address-filter"),
		addr: addr,
	}
}

func (f *AddressFilter) ClaimIncoming(_ []byte, addr net.Addr) bool {
	return addr.String() == f.addr.String()
}

func (f *AddressFilter) Outgoing(_ []byte, _ net.Addr) {
	return
}
