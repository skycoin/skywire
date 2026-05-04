// Package transport pkg/transport/log.go
//
// Per-transport bandwidth counters that the manager exposes to the rest
// of the visor. The store is in-memory only — historical (per-day)
// bandwidth is served from pkg/visor/stats's bbolt-backed Tracker, not
// from this store. See pkg/visor/stats/tracker.go for the daily rollups
// fed into the visor's CXO publisher and the /stats HTTP endpoints.
package transport

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/logging"
)

// LogEntry represents a logging entry for a given Transport.
// The entry is updated every time a packet is received or sent.
type LogEntry struct {
	// atomic requires 64-bit alignment for struct field access
	RecvBytes *uint64 `csv:"recv"` // Total received bytes.
	SentBytes *uint64 `csv:"sent"` // Total sent bytes.
}

// MakeLogEntry makes a new LogEntry by adding the info from old entry if found
func MakeLogEntry(ls LogStore, tpID uuid.UUID, log *logging.Logger) *LogEntry {
	oldLogEntry, err := ls.Entry(tpID)
	if err != nil {
		log.Warn(err)
		log.Warn(fmt.Errorf("new log entry will create for transport %s", tpID.String()))
	}
	newEntry := NewLogEntry()
	if oldLogEntry != nil {
		newEntry.AddRecv(*oldLogEntry.RecvBytes)
		newEntry.AddSent(*oldLogEntry.SentBytes)
	}
	return newEntry
}

// NewLogEntry creates a new LogEntry
func NewLogEntry() *LogEntry {
	recv := uint64(0)
	sent := uint64(0)
	return &LogEntry{
		RecvBytes: &recv,
		SentBytes: &sent,
	}
}

// AddRecv records read.
func (le *LogEntry) AddRecv(n uint64) {
	atomic.AddUint64(le.RecvBytes, n)
}

// AddSent records write.
func (le *LogEntry) AddSent(n uint64) {
	atomic.AddUint64(le.SentBytes, n)
}

// Reset resets LogEntry.
func (le *LogEntry) Reset() {
	atomic.AddUint64(le.SentBytes, -*le.SentBytes)
	atomic.AddUint64(le.RecvBytes, -*le.RecvBytes)
}

// MarshalJSON implements json.Marshaller
func (le *LogEntry) MarshalJSON() ([]byte, error) {
	var rb uint64
	var sb uint64
	if le.RecvBytes != nil {
		rb = atomic.LoadUint64(le.RecvBytes)
	}
	if le.SentBytes != nil {
		sb = atomic.LoadUint64(le.SentBytes)
	}
	return []byte(`{"recv":` + fmt.Sprint(rb) + `,"sent":` + fmt.Sprint(sb) + `}`), nil
}

// GobEncode implements gob.GobEncoder
func (le *LogEntry) GobEncode() ([]byte, error) {
	var b bytes.Buffer
	enc := gob.NewEncoder(&b)
	if le.RecvBytes != nil {
		rb := atomic.LoadUint64(le.RecvBytes)
		if err := enc.Encode(rb); err != nil {
			return nil, err
		}
	}
	if le.SentBytes != nil {
		sb := atomic.LoadUint64(le.SentBytes)
		if err := enc.Encode(sb); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

// GobDecode implements gob.GobDecoder
func (le *LogEntry) GobDecode(b []byte) error {
	r := bytes.NewReader(b)
	dec := gob.NewDecoder(r)
	var rb uint64
	if err := dec.Decode(&rb); err != nil {
		return err
	}
	var sb uint64
	if err := dec.Decode(&sb); err != nil {
		return err
	}
	// Allocate pointers if nil (happens when decoding into a fresh struct)
	if le.RecvBytes == nil {
		le.RecvBytes = new(uint64)
	}
	atomic.StoreUint64(le.RecvBytes, rb)
	if le.SentBytes == nil {
		le.SentBytes = new(uint64)
	}
	atomic.StoreUint64(le.SentBytes, sb)
	return nil
}

// LogStore stores transport log entries. The only implementation is
// the in-memory store — historical persistence is the stats Tracker's
// job (pkg/visor/stats), not this store's. The store exists so that a
// transport that closes and re-opens within the same visor session
// preserves its cumulative byte counters across the gap; nothing in
// this package persists across visor restarts.
type LogStore interface {
	Entry(id uuid.UUID) (*LogEntry, error)
	Record(id uuid.UUID, entry *LogEntry) error
}

type inMemoryTransportLogStore struct {
	entries map[uuid.UUID]*LogEntry
	mu      sync.Mutex
}

// InMemoryTransportLogStore implements in-memory TransportLogStore.
func InMemoryTransportLogStore() LogStore {
	return &inMemoryTransportLogStore{
		entries: make(map[uuid.UUID]*LogEntry),
	}
}

func (tls *inMemoryTransportLogStore) Entry(id uuid.UUID) (*LogEntry, error) {
	tls.mu.Lock()
	entry, ok := tls.entries[id]
	tls.mu.Unlock()
	if !ok {
		return nil, errors.New("transport log entry not found")
	}

	return entry, nil
}

func (tls *inMemoryTransportLogStore) Record(id uuid.UUID, entry *LogEntry) error {
	tls.mu.Lock()
	if tls.entries == nil {
		tls.entries = make(map[uuid.UUID]*LogEntry)
	}
	tls.entries[id] = entry
	tls.mu.Unlock()
	return nil
}
