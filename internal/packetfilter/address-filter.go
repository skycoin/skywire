// Package packetfilter internal/packetfilter/address-filter.go
package packetfilter

import (
	"net"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

// AddressFilter filters packets from specified address.
type AddressFilter struct {
	log  *logging.Logger
	addr net.Addr
}

// NewAddressFilter returns a new AddressFilter.
func NewAddressFilter(addr net.Addr, mLog *logging.MasterLogger) *AddressFilter {
	return &AddressFilter{
		log:  mLog.PackageLogger("address-filter"),
		addr: addr,
	}
}

// ClaimIncoming implements pfilter.Filter.
func (f *AddressFilter) ClaimIncoming(_ []byte, addr net.Addr) bool {
	return addr.String() == f.addr.String()
}

// Outgoing implements pfilter.Filter.
func (f *AddressFilter) Outgoing(_ []byte, _ net.Addr) {}
