//go:build !tinygo || (js && wasm)

package disc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// stubAPIClient records Entry calls and returns a fixed result. Only
// Entry is exercised; the rest satisfy the interface.
type stubAPIClient struct {
	entryCalls int
	entry      *Entry
	err        error
}

func (s *stubAPIClient) Entry(context.Context, cipher.PubKey) (*Entry, error) {
	s.entryCalls++
	return s.entry, s.err
}
func (s *stubAPIClient) AvailableServers(context.Context) ([]*Entry, error) { return nil, nil }
func (s *stubAPIClient) AllServers(context.Context) ([]*Entry, error)       { return nil, nil }
func (s *stubAPIClient) PostEntry(context.Context, *Entry) error            { return nil }
func (s *stubAPIClient) PutEntry(context.Context, cipher.SecKey, *Entry) error {
	return nil
}
func (s *stubAPIClient) DelEntry(context.Context, *Entry) error       { return nil }
func (s *stubAPIClient) AllEntries(context.Context) ([]string, error) { return nil, nil }
func (s *stubAPIClient) AllClientsByServer(context.Context) (map[string][]*Entry, error) {
	return nil, nil
}
func (s *stubAPIClient) ClientsByServer(context.Context, cipher.PubKey) ([]*Entry, error) {
	return nil, nil
}

func clientEntry(pk cipher.PubKey, servers ...cipher.PubKey) *Entry {
	return &Entry{Static: pk, Client: &Client{DelegatedServers: servers}}
}

func TestCXOEntryClient_NilResolverPassesThrough(t *testing.T) {
	stub := &stubAPIClient{}
	got := NewCXOEntryClient(stub, nil, nil)
	if got != APIClient(stub) {
		t.Fatalf("nil resolver should return the primary unchanged")
	}
}

func TestCXOEntryClient_HitServesFromCXO(t *testing.T) {
	var pk, srv cipher.PubKey
	require.NoError(t, pk.Set("02e40731f3ab6d11d31c466429297f4869f299a7821108409c5e36b840253e4ba7"))
	require.NoError(t, srv.Set("02190003862c24f69e2cf47e1cf0efaa3dc1d866ba6a24067de34c363058212c73"))

	resolved := clientEntry(pk, srv)
	stub := &stubAPIClient{entry: nil, err: errors.New("HTTP should not be called")}
	c := NewCXOEntryClient(stub, func(context.Context, cipher.PubKey) (*Entry, bool) {
		return resolved, true
	}, nil)

	got, err := c.Entry(context.Background(), pk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != resolved {
		t.Fatalf("expected CXO-resolved entry, got %v", got)
	}
	if stub.entryCalls != 0 {
		t.Fatalf("wrapped HTTP client must not be called on a CXO hit; got %d calls", stub.entryCalls)
	}
}

func TestCXOEntryClient_MissFallsBackToHTTP(t *testing.T) {
	var pk cipher.PubKey
	require.NoError(t, pk.Set("02e40731f3ab6d11d31c466429297f4869f299a7821108409c5e36b840253e4ba7"))

	httpEntry := clientEntry(pk)
	stub := &stubAPIClient{entry: httpEntry}
	c := NewCXOEntryClient(stub, func(context.Context, cipher.PubKey) (*Entry, bool) {
		return nil, false // miss
	}, nil)

	got, err := c.Entry(context.Background(), pk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != httpEntry {
		t.Fatalf("expected HTTP fallback entry")
	}
	if stub.entryCalls != 1 {
		t.Fatalf("expected exactly one HTTP fallback call, got %d", stub.entryCalls)
	}
}

func TestCXOEntryClient_HitWithoutDelegatedServersFallsBack(t *testing.T) {
	var pk cipher.PubKey
	require.NoError(t, pk.Set("02e40731f3ab6d11d31c466429297f4869f299a7821108409c5e36b840253e4ba7"))

	// Resolver "hits" but the entry has no delegated servers — useless
	// for dialing, so the wrapped client must still be consulted.
	empty := clientEntry(pk) // no servers
	stub := &stubAPIClient{entry: clientEntry(pk)}
	c := NewCXOEntryClient(stub, func(context.Context, cipher.PubKey) (*Entry, bool) {
		return empty, true
	}, nil)

	if _, err := c.Entry(context.Background(), pk); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.entryCalls != 1 {
		t.Fatalf("entry without delegated servers must fall back to HTTP; got %d calls", stub.entryCalls)
	}
}
