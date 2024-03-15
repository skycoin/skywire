// Package httpauth pkg/httpauth/nonce-storer.go
package httpauth

import (
	"context"
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/storeconfig"
)

// Nonce is used to sign requests in order to avoid replay attack
type Nonce uint64

func (n Nonce) String() string { return fmt.Sprintf("%d", n) }

// NonceStore stores Incrementing Security Nonces.
type NonceStore interface {

	// IncrementNonce increments the nonce associated with the specified remote entity.
	// It returns the next expected nonce after it has been incremented and returns error on failure.
	IncrementNonce(ctx context.Context, remotePK cipher.PubKey) (nonce Nonce, err error)

	// Nonce obtains the next expected nonce for a given remote entity (represented by public key).
	// It returns error on failure.
	Nonce(ctx context.Context, remotePK cipher.PubKey) (nonce Nonce, err error)

	// Count obtains the number of entries stored in the underlying database.
	Count(ctx context.Context) (n int, err error)
}

// NewNonceStore returns a new nonce storer of the given kind that connects to given Store's url.
// Nonce count should not be shared between services, so it should be stored in a unique key for every service.
func NewNonceStore(ctx context.Context, config storeconfig.Config, prefix string) (NonceStore, error) {
	switch config.Type {
	case storeconfig.Redis:
		return newRedisStore(ctx, config.URL, config.Password, prefix)
	case storeconfig.Memory:
		return newMemoryStore(), nil
	}

	return nil, fmt.Errorf("kind has to be either redis or memory")
}
