// Package skysocksc cmd/skywire-cli/commands/proxy/route_pin.go c4-app-proxy
package skysocksc

import (
	"fmt"
	"io"
	"time"
)

// legPinner is the slice of visor.API that installing a pin needs. Narrow so
// the cold path can be tested against a stub instead of the whole API.
type legPinner interface {
	muxLegAPI
}

// routePinBudget bounds the whole `proxy start --route` pin install — the wait
// for the route group to become queryable AND the leg installs themselves.
//
// It used to be bounded only on the first half: 30 x 500 ms of retries while
// the group was not yet queryable, and then an UNBOUNDED reconcileLegs. On a
// cold start a leg install is a route setup through the setup node, which the
// router gives up on only at its own ceiling, so the command could sit in
// AddMuxRoute for minutes with nothing printed since "Running!". Measured on
// the rig 2026-09-18 (bench/2026-09-18/2c3d313f9-smoke, chain AU): the first,
// cold set printed "Running!" and nothing else — the bench's own `timeout 240`
// killed the CLI mid-install — and the reference client then ran with NO legs
// at all, on a route the operator had not pinned.
const routePinBudget = 90 * time.Second

// routePinPoll is how often the group is re-checked while it is not yet there.
const routePinPoll = 500 * time.Millisecond

// pinRoutes installs the operator's pinned route(s) on app's route group and
// reports what it did. It is the `--route` half of `proxy start`, split out so
// the cold path — no queryable route group yet, then a leg that does not
// install — is testable.
//
// Three things make it louder than the loop it replaces:
//
//   - it is bounded as a whole, so a blocking leg install ends in a named
//     failure instead of an indefinite silence;
//   - it says it is waiting, once, so a start killed from outside is never
//     mistaken for one that finished;
//   - it VERIFIES the pin landed. reconcileLegs logs a failed AddMuxRoute to
//     stderr and carries on, returning no error, so "route pinned: 0 leg(s)
//     added" was printed as success for a session running on the auto route.
//     A pin the operator gave is not advisory: if it is not on the group when
//     this returns, that is an error.
func pinRoutes(rpc legPinner, app string, targets []routePair, budget time.Duration, out io.Writer) (legReconcile, error) {
	var (
		res      legReconcile
		err      error
		deadline = time.Now().Add(budget)
		said     bool
	)
	for {
		res, err = reconcileLegs(rpc, app, 0, targets, true)
		if err == nil {
			break
		}
		if !said {
			fmt.Fprintf(out, "waiting for %s's route group before pinning %d route(s)...\n", app, len(targets)) //nolint:errcheck,gosec
			said = true
		}
		if time.Now().After(deadline) {
			return res, fmt.Errorf("no route group to pin after %v: %w", budget, err)
		}
		time.Sleep(routePinPoll)
	}
	if got := len(res.added) + res.existing; got < len(targets) {
		return res, fmt.Errorf("%d of %d pinned route(s) could not be installed on %s's route group (see the add-leg errors above); the session is NOT running on the pinned route",
			len(targets)-got, len(targets), app)
	}
	return res, nil
}
