// Package disc pkg/dmsg/disc/recursion.go c1-net-dmsg
package disc

import (
	"context"
	"errors"
	"strings"
)

// ErrCircularDiscovery is returned when answering a discovery request would
// require making one: the client is addressed over dmsg, and a dmsg dial is
// already in progress on this call chain waiting for the very entry being
// asked for.
//
// It is a real configuration outcome, not a theoretical one. A dmsg-addressed
// discovery is reachable only because the dial that reaches it is short-cut by
// a seeded entry for the discovery's own key. Put a `dmsg://` URL in a field
// the code treats as plain HTTP, or lose the seed, and the short-cut is gone:
// the lookup dials, the dial looks up, and nothing bounds it. Observed on an
// Android visor whose config had `dmsg://…` under `dmsg.discovery` instead of
// `dmsg.discovery_dmsg` — it died with "fatal error: stack overflow" about six
// seconds into every start, restarting for as long as anyone left it running.
//
// Failing the lookup turns that into an error the caller can log, retry, or
// surface. It cannot repair the configuration, and does not try to.
var ErrCircularDiscovery = errors.New("dmsg disc: circular lookup — a dmsg-addressed discovery cannot be reached from inside its own dial")

// inFlightKey marks a context chain on which a discovery request is already
// running.
//
// A context value rather than anything goroutine-local because the chain
// already carries one end to end: the request context reaches the dmsg
// transport's RoundTrip, which hands it to DialStream, which hands it to the
// entry lookup that closes the loop. Nothing in between needs to know.
type inFlightKey struct{}

// withRequestInFlight tags ctx as carrying a discovery request.
func withRequestInFlight(ctx context.Context) context.Context {
	return context.WithValue(ctx, inFlightKey{}, struct{}{})
}

// requestInFlight reports whether a discovery request is already running on
// this chain.
func requestInFlight(ctx context.Context) bool {
	return ctx.Value(inFlightKey{}) != nil
}

// overDmsg reports whether this client's requests ride a dmsg transport, and
// so whether a nested lookup through it would re-enter a dial in progress.
//
// Only these need refusing. A nested lookup against a plain-HTTP discovery is
// the intended way OUT of the cycle — it dials TCP, needs no entry, and
// answers the question the dmsg dial is stuck on.
func overDmsg(address string) bool {
	return strings.HasPrefix(strings.ToLower(address), "dmsg://")
}
