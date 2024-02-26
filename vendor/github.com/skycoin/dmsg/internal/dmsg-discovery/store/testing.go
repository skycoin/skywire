// Package store internal/dmsg-discovery/store/testing.go
package store

import (
	"context"
	"sync"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"

	"github.com/skycoin/dmsg/pkg/disc"
)

// MockStore implements a storer mock
type MockStore struct {
	mLock       sync.RWMutex
	serversLock sync.RWMutex
	m           map[string][]byte
	servers     map[string][]byte
}

func (ms *MockStore) setEntry(staticPubKey string, payload []byte) {
	ms.mLock.Lock()
	defer ms.mLock.Unlock()

	ms.m[staticPubKey] = payload
}

func (ms *MockStore) delEntry(staticPubKey string) {
	ms.mLock.Lock()
	defer ms.mLock.Unlock()
	delete(ms.m, staticPubKey)
}

func (ms *MockStore) entry(staticPubkey string) ([]byte, bool) {
	ms.mLock.RLock()
	defer ms.mLock.RUnlock()

	e, ok := ms.m[staticPubkey]

	return e, ok
}

func (ms *MockStore) setServer(staticPubKey string, payload []byte) {
	ms.serversLock.Lock()
	defer ms.serversLock.Unlock()

	ms.servers[staticPubKey] = payload
}

// NewMock returns a mock storer.
func NewMock() Storer {
	return &MockStore{
		m:       map[string][]byte{},
		servers: map[string][]byte{},
	}
}

// Entry implements Storer Entry method for MockStore
func (ms *MockStore) Entry(_ context.Context, staticPubKey cipher.PubKey) (*disc.Entry, error) {
	payload, ok := ms.entry(staticPubKey.Hex())
	if !ok {
		return nil, disc.ErrKeyNotFound
	}

	var entry disc.Entry

	// Should not be necessary to check for errors since we control the serialization to JSON`
	err := json.Unmarshal(payload, &entry)
	if err != nil {
		return nil, disc.ErrUnexpected
	}

	err = entry.VerifySignature()
	if err != nil {
		return nil, disc.ErrUnauthorized
	}

	return &entry, nil
}

// SetEntry implements Storer SetEntry method for MockStore
func (ms *MockStore) SetEntry(_ context.Context, entry *disc.Entry, _ time.Duration) error {
	payload, err := json.Marshal(entry)
	if err != nil {
		return disc.ErrUnexpected
	}

	ms.setEntry(entry.Static.Hex(), payload)

	if entry.Server != nil {
		ms.setServer(entry.Static.Hex(), payload)
	}

	return nil
}

// DelEntry implements Storer DelEntry method for MockStore
func (ms *MockStore) DelEntry(_ context.Context, staticPubKey cipher.PubKey) error {
	ms.delEntry(staticPubKey.Hex())
	return nil
}

// RemoveOldServerEntries implements Storer RemoveOldServerEntries method for MockStore
func (ms *MockStore) RemoveOldServerEntries(_ context.Context) error {
	return nil
}

// Clear its a mock-only method to clear the mock store data
func (ms *MockStore) Clear() {
	ms.m = map[string][]byte{}
	ms.servers = map[string][]byte{}
}

// AvailableServers implements Storer AvailableServers method for MockStore
func (ms *MockStore) AvailableServers(_ context.Context, _ int) ([]*disc.Entry, error) {
	entries := make([]*disc.Entry, 0)

	ms.serversLock.RLock()
	defer ms.serversLock.RUnlock()

	servers := arrayFromMap(ms.servers)
	for _, entryString := range servers {
		var e disc.Entry

		err := json.Unmarshal(entryString, &e)
		if err != nil {
			return nil, disc.ErrUnexpected
		}

		entries = append(entries, &e)
	}

	return entries, nil
}

// AllServers implements Storer AllServers method for MockStore
func (ms *MockStore) AllServers(_ context.Context) ([]*disc.Entry, error) {
	entries := make([]*disc.Entry, 0)

	ms.serversLock.RLock()
	defer ms.serversLock.RUnlock()

	servers := arrayFromMap(ms.servers)
	for _, entryString := range servers {
		var e disc.Entry

		err := json.Unmarshal(entryString, &e)
		if err != nil {
			return nil, disc.ErrUnexpected
		}

		entries = append(entries, &e)
	}

	return entries, nil
}

// CountEntries implements Storer CountEntries method for MockStore
func (ms *MockStore) CountEntries(_ context.Context) (int64, int64, error) {
	var numberOfServers int64
	var numberOfClients int64
	ms.serversLock.RLock()
	defer ms.serversLock.RUnlock()

	servers := arrayFromMap(ms.servers)
	for _, entryString := range servers {
		var e disc.Entry

		err := json.Unmarshal(entryString, &e)
		if err != nil {
			return numberOfServers, numberOfClients, disc.ErrUnexpected

		}

		if e.Server != nil {
			numberOfServers++
		}
		if e.Client != nil {
			numberOfClients++
		}
	}
	return numberOfServers, numberOfClients, disc.ErrUnexpected
}

func arrayFromMap(m map[string][]byte) [][]byte {
	entries := make([][]byte, 0)

	for _, value := range m {
		buf := make([]byte, len(value))

		copy(buf, value)

		entries = append(entries, buf)
	}

	return entries
}

// AllEntries implements Storer CountEntries method for MockStore
func (ms *MockStore) AllEntries(_ context.Context) ([]string, error) {
	entries := []string{}

	ms.mLock.RLock()
	defer ms.mLock.RUnlock()

	clients := arrayFromMap(ms.m)
	for _, entryString := range clients {
		var e disc.Entry

		err := json.Unmarshal(entryString, &e)
		if err != nil {
			return nil, disc.ErrUnexpected
		}

		entries = append(entries, e.String())
	}
	return entries, nil
}

// AllVisorEntries implements Storer CountEntries method for MockStore
func (ms *MockStore) AllVisorEntries(_ context.Context) ([]string, error) {
	entries := []string{}

	ms.mLock.RLock()
	defer ms.mLock.RUnlock()

	clients := arrayFromMap(ms.m)
	for _, entryString := range clients {
		var e disc.Entry

		err := json.Unmarshal(entryString, &e)
		if err != nil {
			return nil, disc.ErrUnexpected
		}

		entries = append(entries, e.String())
	}
	return entries, nil
}
