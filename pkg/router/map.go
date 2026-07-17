//go:build !tinygo || (js && wasm)

// Package router pkg/router/map.go c2-net-routing
package router

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// MakeMapTimeout is the hard deadline for MakeMap to dial all visors concurrently.
// This prevents indefinite blocking when multiple visors are unreachable, which was
// the primary cause of goroutine accumulation in the setup-node (113+ stuck goroutines
// observed in production pprof dumps).
const MakeMapTimeout = 60 * time.Second

// dialHopAttempts bounds how many times a single hop's reservation dial is
// tried before giving up. A dmsg-202 ("cannot connect to delegated server")
// during route-id reservation is almost always transient: the hop is online and
// reachable again within seconds — a dmsg server on the path briefly churned
// (dropped/zombied a session). Each re-dial re-runs DialStream's own
// multi-server selection, so after a short backoff it routes around the churn.
// Without this, one transient blip on ONE hop fails the entire reservation and
// trips the far more expensive whole-route-setup retry above it. Bounded by the
// MakeMapTimeout ctx.
const dialHopAttempts = 3

// isRetryableDialErr reports whether a hop dial failed with a transient dmsg
// bridge error (dmsg 202) that is worth re-dialing.
func isRetryableDialErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, dmsg.ErrCannotConnectToDelegated) {
		return true
	}
	// The sentinel can be lost when the error is reconstructed from a wire code
	// or wrapped as a plain string; fall back to the message.
	return strings.Contains(err.Error(), "cannot connect to delegated server")
}

// dialHopWithRetry dials one hop, retrying on a transient dmsg-202 bridge
// failure with a short linear backoff. It stops early on success, on a
// non-retryable error, or when ctx is canceled (e.g. another hop failed
// genuinely, or MakeMapTimeout fired).
func dialHopWithRetry(ctx context.Context, pk cipher.PubKey, dial func(context.Context, cipher.PubKey) (*Client, error)) (*Client, error) {
	var (
		client *Client
		err    error
	)
	for attempt := 0; attempt < dialHopAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * time.Second):
			case <-ctx.Done():
				return nil, err
			}
		}
		if client, err = dial(ctx, pk); err == nil {
			return client, nil
		}
		if !isRetryableDialErr(err) || ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, err
}

// Map is a map of router RPC clients associated with the router's visor PK.
type Map map[cipher.PubKey]*Client

type dialResult struct {
	client *Client
	err    error
}

// MakeMap makes a Map of the router clients, where the key is the router's visor public key.
// It creates these router clients by dialing to them concurrently.
// A hard timeout of MakeMapTimeout is applied to prevent indefinite blocking when visors
// are unreachable. The results channel is buffered so that goroutines completing after
// cancellation do not leak waiting to send.
func MakeMap(ctx context.Context, dialer network.Dialer, pks []cipher.PubKey) (Map, error) {
	if len(pks) == 0 {
		return make(Map), nil
	}

	// Apply MakeMapTimeout unless the parent context already has a shorter deadline.
	ctx, cancel := context.WithTimeout(ctx, MakeMapTimeout)
	defer cancel()

	// Buffered channel: goroutines that complete after cancellation can send without blocking,
	// preventing goroutine leaks. Previously this was unbuffered, causing goroutines to block
	// forever on send when the reader stopped consuming after the first error.
	results := make(chan dialResult, len(pks))

	for _, pk := range pks {
		go func(pk cipher.PubKey) {
			client, err := dialHopWithRetry(ctx, pk, func(c context.Context, pk cipher.PubKey) (*Client, error) {
				return NewClient(c, dialer, pk)
			})
			results <- dialResult{client: client, err: err}
		}(pk)
	}

	rcM := make(Map, len(pks))
	var firstErr error
	received := 0
	for received < len(pks) {
		select {
		case res := <-results:
			received++
			if res.err != nil {
				if firstErr == nil {
					firstErr = res.err
				}
				cancel() // cancel remaining dials on first error
				continue
			}
			if isDone(ctx) {
				// Context was canceled (by error or timeout); close late arrivals.
				if res.client != nil {
					res.client.Close() //nolint:errcheck,gosec
				}
				continue
			}
			rcM[res.client.rPK] = res.client

		case <-ctx.Done():
			// Timeout or cancellation: drain remaining results to close any late clients.
			if firstErr == nil {
				firstErr = ctx.Err()
			}
			// Close what we have and return.
			if closeErrs := rcM.CloseAll(); len(closeErrs) > 0 {
				log.WithError(closeErrs[0]).WithField("count", len(closeErrs)).Warn("MakeMap: errors closing clients after timeout")
			}
			// Spawn a cleanup goroutine to drain and close any late-arriving clients.
			// Each goroutine will eventually complete (bounded by DialTimeout) and
			// send to the buffered channel.
			remaining := len(pks) - received
			go drainResults(results, remaining)
			return rcM, firstErr
		}
	}

	if firstErr != nil {
		if closeErrs := rcM.CloseAll(); len(closeErrs) > 0 {
			log.WithError(closeErrs[0]).WithField("count", len(closeErrs)).Warn("MakeMap: errors closing clients after dial failure")
		}
	}
	return rcM, firstErr
}

// drainResults reads remaining dial results from a buffered channel and closes any
// successfully dialed clients. This prevents goroutine leaks when MakeMap returns
// early due to timeout or error.
func drainResults(results <-chan dialResult, remaining int) {
	for i := 0; i < remaining; i++ {
		res := <-results
		if res.client != nil {
			res.client.Close() //nolint:errcheck,gosec
		}
	}
}

// Client returns a router client of given public key.
func (cm Map) Client(rPK cipher.PubKey) *Client {
	return cm[rPK]
}

// CloseAll closes all contained router clients.
func (cm Map) CloseAll() (errs []error) {
	for k, c := range cm {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
		delete(cm, k)
	}
	return errs
}

// MakePooledMap is like MakeMap but borrows connections from a ClientPool
// instead of dialing fresh ones. Clients that were successfully used should
// be returned to the pool via ReturnToPool; broken ones via DiscardFromPool.
func MakePooledMap(ctx context.Context, pool *ClientPool, pks []cipher.PubKey) (Map, error) {
	if len(pks) == 0 {
		return make(Map), nil
	}

	ctx, cancel := context.WithTimeout(ctx, MakeMapTimeout)
	defer cancel()

	results := make(chan dialResult, len(pks))

	for _, pk := range pks {
		go func(pk cipher.PubKey) {
			client, err := dialHopWithRetry(ctx, pk, func(c context.Context, pk cipher.PubKey) (*Client, error) {
				return pool.Get(c, pk)
			})
			results <- dialResult{client: client, err: err}
		}(pk)
	}

	rcM := make(Map, len(pks))
	var firstErr error
	received := 0
	for received < len(pks) {
		select {
		case res := <-results:
			received++
			if res.err != nil {
				if firstErr == nil {
					firstErr = res.err
				}
				cancel()
				continue
			}
			if isDone(ctx) {
				if res.client != nil {
					pool.Discard(res.client)
				}
				continue
			}
			rcM[res.client.rPK] = res.client

		case <-ctx.Done():
			if firstErr == nil {
				firstErr = ctx.Err()
			}
			// Discard what we have — don't return broken clients to pool.
			for _, c := range rcM {
				pool.Discard(c)
			}
			remaining := len(pks) - received
			go func() {
				for i := 0; i < remaining; i++ {
					res := <-results
					if res.client != nil {
						pool.Discard(res.client)
					}
				}
			}()
			return make(Map), firstErr
		}
	}

	if firstErr != nil {
		for _, c := range rcM {
			pool.Discard(c)
		}
		return make(Map), firstErr
	}
	return rcM, nil
}

// ReturnToPool returns all clients in the Map to the given pool for reuse.
func (cm Map) ReturnToPool(pool *ClientPool) {
	for k, c := range cm {
		pool.Put(c)
		delete(cm, k)
	}
}

// DiscardFromPool closes all clients without pooling them.
func (cm Map) DiscardFromPool(pool *ClientPool) {
	for k, c := range cm {
		pool.Discard(c)
		delete(cm, k)
	}
}

func isDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
