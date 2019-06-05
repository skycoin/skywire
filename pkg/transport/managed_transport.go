package transport

import (
	"math/big"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

// ManagedTransport is a wrapper transport. It stores status and ID of
// the Transport and can notify about network errors.
type ManagedTransport struct {
	Transport
	ID       uuid.UUID
	Public   bool
	Accepted bool
	LogEntry *LogEntry

	doneChan  chan struct{}
	errChan   chan error
	isClosing int32
	mu        sync.RWMutex

	readLogChan  chan int
	writeLogChan chan int
}

func newManagedTransport(id uuid.UUID, tr Transport, public bool, accepted bool) *ManagedTransport {
	return &ManagedTransport{
		ID:           id,
		Transport:    tr,
		Public:       public,
		Accepted:     accepted,
		doneChan:     make(chan struct{}),
		errChan:      make(chan error),
		readLogChan:  make(chan int),
		writeLogChan: make(chan int),
		LogEntry:     &LogEntry{new(big.Int), new(big.Int)},
	}
}

// Read reads using underlying
func (tr *ManagedTransport) Read(p []byte) (n int, err error) {
	tr.mu.RLock()
	n, err = tr.Transport.Read(p) // TODO: data race.
	tr.mu.RUnlock()

	if err != nil {
		tr.errChan <- err
	}

	tr.readLogChan <- n
	return
}

// Write writes to an underlying
func (tr *ManagedTransport) Write(p []byte) (n int, err error) {
	tr.mu.RLock()
	n, err = tr.Transport.Write(p)
	tr.mu.RUnlock()

	if err != nil {
		tr.errChan <- err
		return
	}
	tr.writeLogChan <- n

	return
}

// killWorker sends signal to Manager.manageTransport goroutine to exit
// it's safe to call it multiple times
func (tr *ManagedTransport) killWorker() {
	select {
	case <-tr.doneChan:
		return
	default:
		close(tr.doneChan)
	}
}

// Close closes underlying
func (tr *ManagedTransport) Close() error {

	atomic.StoreInt32(&tr.isClosing, 1)

	tr.mu.RLock()
	err := tr.Transport.Close()
	tr.mu.RUnlock()

	tr.killWorker()

	return err
}

func (tr *ManagedTransport) updateTransport(newTr Transport) {
	tr.mu.Lock()
	tr.Transport = newTr
	tr.mu.Unlock()
}
