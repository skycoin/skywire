// Package disc pkg/disc/testing.go
package disc

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// MockClient is an APIClient mock. The mock doesn't reply with the same errors as the
// real client, and it mimics it's functionality not being 100% accurate.
type mockClient struct {
	entries map[cipher.PubKey]Entry
	mx      sync.RWMutex

	timeout time.Duration
}

// NewMock constructs  a new mock APIClient.
func NewMock(timeout time.Duration) APIClient {
	return &mockClient{
		entries: make(map[cipher.PubKey]Entry),
		timeout: timeout,
	}
}

func (m *mockClient) entry(pk cipher.PubKey) (Entry, bool) {
	m.mx.RLock()
	defer m.mx.RUnlock()

	e, ok := m.entries[pk]
	return e, ok
}

func (m *mockClient) delEntry(pk cipher.PubKey) {
	m.mx.Lock()
	defer m.mx.Unlock()
	delete(m.entries, pk)
}

func (m *mockClient) setEntry(entry Entry) {
	m.mx.Lock()
	defer m.mx.Unlock()

	// timeout trigger
	if m.timeout != 0 {
		go func(pk cipher.PubKey) {
			<-time.After(m.timeout)

			m.mx.Lock()
			defer m.mx.Unlock()

			if entry, ok := m.entries[pk]; ok {
				if ts := time.Unix(0, entry.Timestamp); time.Since(ts) > m.timeout {
					delete(m.entries, entry.Static)
				}
			}
		}(entry.Static)
	}

	m.entries[entry.Static] = entry
}

// Entry returns the mock client static public key associated entry
func (m *mockClient) Entry(_ context.Context, pk cipher.PubKey) (*Entry, error) {
	entry, ok := m.entry(pk)
	if !ok {
		return nil, errors.New(HTTPMessage{ErrKeyNotFound.Error(), http.StatusNotFound}.String())
	}
	res := &Entry{}
	Copy(res, &entry)
	return res, nil
}

// PostEntry sets an entry on the APIClient mock
func (m *mockClient) PostEntry(_ context.Context, entry *Entry) error {
	previousEntry, ok := m.entry(entry.Static)
	if ok {
		err := previousEntry.ValidateIteration(entry)
		if err != nil {
			return err
		}
		err = entry.VerifySignature()
		if err != nil {
			return err
		}
	}

	m.setEntry(*entry)
	return nil
}

// DelEntry returns the mock client static public key associated entry
func (m *mockClient) DelEntry(_ context.Context, entry *Entry) error {
	m.delEntry(entry.Static)
	return nil
}

// PutEntry updates a previously set entry
func (m *mockClient) PutEntry(ctx context.Context, sk cipher.SecKey, e *Entry) error {
	e.Sequence++
	e.Timestamp = time.Now().UnixNano()

	for {
		err := e.Sign(sk)
		if err != nil {
			return err
		}
		err = m.PostEntry(ctx, e)
		if err == nil {
			return nil
		}
		if err != ErrValidationWrongSequence {
			e.Sequence--
			return err
		}
		rE, entryErr := m.Entry(ctx, e.Static)
		if entryErr != nil {
			return err
		}
		if rE.Timestamp > e.Timestamp { // If there is a more up to date entry drop update
			e.Sequence = rE.Sequence
			return nil
		}
		e.Sequence = rE.Sequence + 1
	}
}

// AvailableServers returns available servers that the APIClient mock has
func (m *mockClient) AvailableServers(_ context.Context) ([]*Entry, error) {
	m.mx.RLock()
	defer m.mx.RUnlock()
	list := make([]*Entry, 0, len(m.entries))
	for _, e := range m.entries {
		if e := e; e.Server != nil {
			list = append(list, &e)
		}
	}
	return list, nil
}

// AllServers returns all servers that the APIClient mock has
func (m *mockClient) AllServers(_ context.Context) ([]*Entry, error) {
	m.mx.RLock()
	defer m.mx.RUnlock()
	list := make([]*Entry, 0, len(m.entries))
	for _, e := range m.entries {
		if e := e; e.Server != nil {
			list = append(list, &e)
		}
	}
	return list, nil
}

// AllEntries returns all entries that the APIClient mock has
func (m *mockClient) AllEntries(_ context.Context) ([]string, error) {
	m.mx.RLock()
	defer m.mx.RUnlock()
	list := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		list = append(list, e.Static.Hex())
	}
	return list, nil
}
