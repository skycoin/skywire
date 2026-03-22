package transport

import (
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// NewManagedTransportForTest creates a minimal ManagedTransport for testing.
// Only the transport field and basic channels are initialized.
func NewManagedTransportForTest(conn network.Transport) *ManagedTransport {
	return &ManagedTransport{
		log:         logging.MustGetLogger("tp:test"),
		transport:   conn,
		transportCh: make(chan struct{}, 1),
		done:        make(chan struct{}),
	}
}
