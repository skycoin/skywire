// Package transport pkg/transport/testing_helpers.go c2-net-transport
package transport

import (
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/logging"
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

// InjectTransportForTest inserts mt into the manager's transport map keyed by
// its Entry.ID. Test-only: it bypasses dialing/discovery so cross-package tests
// can populate a Manager without a live network.
func (tm *Manager) InjectTransportForTest(mt *ManagedTransport) {
	tm.mx.Lock()
	defer tm.mx.Unlock()
	tm.tps[mt.Entry.ID] = mt
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
		LogEntry:      NewLogEntry(),        // so logSent/logRecv don't nil-deref
	}
}
