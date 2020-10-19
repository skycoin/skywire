package packetfilter

import (
	"net"

	"github.com/skycoin/skycoin/src/util/logging"
)

// AddressFilter filters packets from specified address.
type AddressFilter struct {
	log  *logging.Logger
	addr net.Addr
}

// NewAddressFilter returns a new AddressFilter.
func NewAddressFilter(addr net.Addr) *AddressFilter {
	return &AddressFilter{
		log:  logging.MustGetLogger("address-filter"),
		addr: addr,
	}
}

// ClaimIncoming implements pfilter.Filter.
func (f *AddressFilter) ClaimIncoming(_ []byte, addr net.Addr) bool {
	return addr.String() == f.addr.String()
}

// Outgoing implements pfilter.Filter.
func (f *AddressFilter) Outgoing(_ []byte, _ net.Addr) {}
