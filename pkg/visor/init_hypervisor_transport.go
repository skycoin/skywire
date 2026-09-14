// Package visor pkg/visor/init_hypervisor_transport.go c3-vis-core
//
// Visor → hypervisor transport auto-upgrade.
//
// The visor's `Hypervisors` config field lists hypervisor PKs the
// visor reports to. ServeRPCClient establishes a dmsg session to
// each as the bootstrap RPC channel — that's the "is the hypervisor
// reachable" signal. Once that session is up, the goroutine started
// here attempts a direct transport between the visor and the
// hypervisor too, so subsequent RPC + skypty dials can ride the fast
// p2p path instead of the dmsg relay.
//
// Ordering rationale:
//
//  1. dmsg session up → hypervisor is alive + reachable through the
//     network.
//  2. Attempt every direct carrier this visor can open, in the active
//     preference order (tptypes.PreferenceOrder), stopping at the
//     first that succeeds. dmsg is skipped — it is the relay being
//     escaped, not a destination.
//
// The carrier set is deliberately NOT a fixed pair. It used to be
// stcpr-then-sudph, which quietly excluded every visor that has
// neither: a browser visor opens swtr/swsr/webrtc and nothing else,
// so it never upgraded off the relay at all. Preference is one
// setting, owned by tptypes and settable at runtime; this loop reads
// it rather than restating a subset of it.
//
// The transport persists past first creation; the goroutine
// reconciles every 5 minutes (or after a failed attempt's backoff
// expires) by re-checking whether a fast transport is still present
// and re-trying if not.
//
// Stops cleanly when the visor's lifecycle ctx is canceled.
package visor

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	// hypervisorTransportInitialBackoff is the first retry delay
	// after the goroutine starts; gives ServeRPCClient time to
	// complete its initial dmsg dial.
	hypervisorTransportInitialBackoff = 5 * time.Second
	// hypervisorTransportMaxBackoff caps the failure-path backoff.
	hypervisorTransportMaxBackoff = 5 * time.Minute
	// hypervisorTransportReconcileInterval is the steady-state
	// poll cadence once a fast transport is established.
	hypervisorTransportReconcileInterval = 5 * time.Minute
	// hypervisorTransportProbeTimeout caps the dmsg reachability
	// probe; on slower paths a longer timeout would just delay the
	// next reconciliation pass.
	hypervisorTransportProbeTimeout = 10 * time.Second
)

// autoUpgradeHypervisorTransport runs in a goroutine per configured
// hypervisor PK. See the package doc for the gating + ordering.
func (v *Visor) autoUpgradeHypervisorTransport(ctx context.Context, hvPK cipher.PubKey, log logrus.FieldLogger) {
	backoff := hypervisorTransportInitialBackoff

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		// Transport manager may not be wired yet at very-early init —
		// in practice it always is by the time initHypervisors runs,
		// but defend anyway since we can't recover from a nil panic.
		if v.tpM == nil {
			backoff = hypervisorTransportInitialBackoff
			continue
		}
		// Which direct carriers can this visor actually open? Walk the
		// active preference order rather than a hardcoded pair: a browser
		// visor has neither stcpr nor sudph but does have swtr/swsr/webrtc,
		// and gating on the pair meant it never upgraded off the dmsg relay
		// at all. Re-read every cycle — the order is settable at runtime and
		// the RPC API can flip which networks are known.
		candidates := directCarrierCandidates(tptypes.PreferenceOrder(), v.tpM.IsKnownNetwork)
		if len(candidates) == 0 {
			backoff = hypervisorTransportReconcileInterval
			continue
		}

		// Probe via dmsg first. Without a dmsg session to the
		// hypervisor, the transport-create attempt would just spin
		// against an unresponsive peer; gating on a successful probe
		// keeps the failure-path cheap.
		if v.dmsgC != nil {
			probeCtx, probeCancel := context.WithTimeout(ctx, hypervisorTransportProbeTimeout)
			reachable := v.dmsgC.Probe(probeCtx, hvPK, skyenv.DmsgAwaitSetupPort)
			probeCancel()
			if !reachable {
				if backoff < hypervisorTransportMaxBackoff {
					backoff *= 2
				}
				continue
			}
		}

		// Already have a fast transport to this hypervisor? Then
		// nothing to do this cycle.
		if v.hasFastTransportTo(hvPK) {
			backoff = hypervisorTransportReconcileInterval
			continue
		}

		// Try each carrier in preference order, stopping at the first that
		// takes. Every type is worth attempting: the point is to get off the
		// dmsg relay, and which carrier achieves that is the preference
		// order's decision, not this loop's.
		upgraded := false
		for _, t := range candidates {
			if _, err := v.tpM.SaveTransport(ctx, hvPK, t, transport.LabelAutomatic); err == nil {
				log.WithField("type", string(t)).
					WithField("hypervisor_pk", hvPK).
					Info("Upgraded hypervisor transport")
				backoff = hypervisorTransportReconcileInterval
				upgraded = true
				break
			} else if isContextError(err) {
				return
			} else {
				log.WithField("type", string(t)).WithError(err).
					Debug("transport to hypervisor failed; trying next carrier")
			}
		}
		if upgraded {
			continue
		}

		// Every carrier failed — back off and try again later. dmsg
		// session remains the working channel.
		if backoff < hypervisorTransportMaxBackoff {
			backoff *= 2
		}
	}
}

// hasFastTransportTo reports whether a direct (non-dmsg) transport to
// remotePK exists, whatever its label: an automatic stcpr/sudph one this
// loop created, or the swsr a browser visor opened back to us at the
// page origin. Any of those is the fast path the RPC dialer wants, so
// there is nothing left for this loop to do while one is present.
func (v *Visor) hasFastTransportTo(remotePK cipher.PubKey) bool {
	return hasFastTransportTo(v.tpM, remotePK)
}

// directCarrierCandidates filters the preference order down to the direct
// carriers this visor can actually open, preserving order. DMSG is dropped:
// it is the relay the upgrade exists to escape, so "upgrading" to it would
// be a no-op that also stops the loop from trying anything better.
//
// Taking the order and the predicate as arguments keeps the selection
// testable without standing up a transport manager, and keeps the policy
// (which carriers, in what order) owned by tptypes rather than restated here.
func directCarrierCandidates(order []tptypes.Type, known func(tptypes.Type) bool) []tptypes.Type {
	candidates := make([]tptypes.Type, 0, len(order))
	for _, t := range order {
		if t == tptypes.DMSG {
			continue
		}
		if known(t) {
			candidates = append(candidates, t)
		}
	}
	return candidates
}
