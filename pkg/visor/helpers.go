// Package visor pkg/visor/helpers.go
// Common helper functions to reduce code duplication across the visor package.
package visor

import (
	"context"
	crand "crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// ErrDmsgNotReady is returned when the DMSG client is not ready within the timeout.
var ErrDmsgNotReady = errors.New("dmsg client not ready")

// waitDmsgReady waits for a DMSG client to be ready with a timeout.
// Returns ErrDmsgNotReady if the timeout expires.
func waitDmsgReady(ctx context.Context, dmsgC *dmsg.Client, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-ctx.Done():
		return ErrDmsgNotReady
	case <-dmsgC.Ready():
		return nil
	}
}

// mustWaitDmsgReady is a convenience wrapper that uses context.Background
// and the standard 30-second timeout.
func (v *Visor) mustWaitDmsgReady() error {
	if v.dmsgC == nil {
		return fmt.Errorf("dmsg client is nil")
	}
	return waitDmsgReady(context.Background(), v.dmsgC, 30*time.Second)
}

// shufflePubKeys shuffles the given public keys in place using a
// cryptographically-secure Fisher-Yates shuffle and returns the slice. It
// mirrors shuffleServers (init_dmsg.go) and is used to randomize public-key
// ordering so callers don't deterministically favor the same peer.
func shufflePubKeys(in []cipher.PubKey) []cipher.PubKey {
	for i := len(in) - 1; i > 0; i-- {
		jBig, err := crand.Int(crand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			panic(err)
		}
		j := int(jBig.Int64())
		in[i], in[j] = in[j], in[i]
	}
	return in
}
