package transport

import (
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// CloseForTest closes the ManagedTransport's done channel, marking it as closed.
// This is for tests that need to simulate a closed transport without going
// through the full close() path which requires a discovery client.
func (mt *ManagedTransport) CloseForTest() {
	select {
	case <-mt.done:
	default:
		close(mt.done)
	}
}

// NewManagedTransportForTest creates a minimal ManagedTransport for testing.
// Only the transport field and basic channels are initialized.
// Uses a no-op queueDeletion to avoid nil pointer on close.
func NewManagedTransportForTest(conn network.Transport) *ManagedTransport {
	return &ManagedTransport{
		log:           logging.MustGetLogger("tp:test"),
		transport:     conn,
		transportCh:   make(chan struct{}, 1),
		done:          make(chan struct{}),
		queueDeletion: func(_ uuid.UUID) {}, // no-op for tests
	}
}
