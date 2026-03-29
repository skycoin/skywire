// Package visor pkg/visor/helpers.go
// Common helper functions to reduce code duplication across the visor package.
package visor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"
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
