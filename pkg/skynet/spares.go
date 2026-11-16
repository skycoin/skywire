// Package skynet pkg/skynet/spares.go c2-app-skynet
package skynet

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/0magnet/yamux"

	"github.com/skycoin/skywire/pkg/logging"
)

// Tunnel roles a forward stamps on its route groups. The router's standby
// pool arbiter reads them, see pkg/router/pool_arbiter.go.
const (
	TunnelRoleActive  = "active"
	TunnelRoleStandby = "standby"
)

// DefaultSpares is the spare route count of a multi-route forward. It sits
// one above the router's pool.min_standby so the pool can always give one up.
const DefaultSpares = 3

// SpareRetry is the wait before a spare that closed or failed is dialed again.
const SpareRetry = 10 * time.Second

// HoldSpares keeps n spare route groups to the far end's mux port open until
// ctx ends. Each holds an idle yamux session so the far end keeps it alive.
// A spare the router takes as a leg closes here and is dialed again.
func HoldSpares(ctx context.Context, log logging.FieldLogger, n int, retry time.Duration, dial func() (net.Conn, error)) {
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				holdSpare(ctx, log, dial)
				select {
				case <-ctx.Done():
				case <-time.After(retry):
				}
			}
		}()
	}
	wg.Wait()
}

// holdSpare dials one spare and returns when it closes or ctx ends.
func holdSpare(ctx context.Context, log logging.FieldLogger, dial func() (net.Conn, error)) {
	conn, err := dial()
	if err != nil {
		log.WithError(err).Debug("Spare route dial failed")
		return
	}
	defer conn.Close() //nolint:errcheck
	sess, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		log.WithError(err).Debug("Spare route session failed")
		return
	}
	defer sess.Close() //nolint:errcheck
	log.Debug("Spare route ready")
	select {
	case <-ctx.Done():
	case <-sess.CloseChan():
		log.Debug("Spare route closed")
	}
}
