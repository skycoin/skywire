// Package router pkg/router/map.go
package router

import (
	"context"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// MakeMapTimeout is the hard deadline for MakeMap to dial all visors concurrently.
// This prevents indefinite blocking when multiple visors are unreachable, which was
// the primary cause of goroutine accumulation in the setup-node (113+ stuck goroutines
// observed in production pprof dumps).
const MakeMapTimeout = 60 * time.Second

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
			client, err := NewClient(ctx, dialer, pk)
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
				// Context was cancelled (by error or timeout); close late arrivals.
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

func isDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
