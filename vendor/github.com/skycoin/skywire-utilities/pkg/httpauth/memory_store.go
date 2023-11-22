// Package httpauth pkg/httpauth/memory_store.go
package httpauth

import (
	"context"
	"sync"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

type memStore struct {
	nonces map[cipher.PubKey]Nonce

	err error
	mu  sync.Mutex
}

func newMemoryStore() *memStore {
	return &memStore{
		nonces: make(map[cipher.PubKey]Nonce),
	}
}

func (s *memStore) SetError(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func (s *memStore) Nonce(ctx context.Context, pk cipher.PubKey) (Nonce, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return 0, s.err
	}

	return s.nonces[pk], nil
}

func (s *memStore) IncrementNonce(ctx context.Context, pk cipher.PubKey) (Nonce, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return 0, s.err
	}

	s.nonces[pk]++
	return s.nonces[pk], nil
}

func (s *memStore) Count(ctx context.Context) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return 0, s.err
	}

	return len(s.nonces), nil
}
