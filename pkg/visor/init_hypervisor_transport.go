// Package visor pkg/visor/init_hypervisor_transport.go
//
// Visor → hypervisor transport auto-upgrade.
//
// The visor's `Hypervisors` config field lists hypervisor PKs the
// visor reports to. ServeRPCClient establishes a dmsg session to
// each as the bootstrap RPC channel — that's the "is the hypervisor
// reachable" signal. Once that session is up, the goroutine started
// here attempts to establish a stcpr / sudph transport between the
// visor and the hypervisor too, so subsequent RPC + skypty dials
// can ride the fast p2p path instead of the dmsg relay.
//
// Ordering rationale:
//
//  1. dmsg session up  → hypervisor is alive + reachable through the
//     network.
//  2. Attempt stcpr (no dmsg needed) — direct TCP via address
//     resolver bind. Falls through quickly when the visor or
//     hypervisor lacks a public stcpr endpoint.
//  3. Attempt sudph (uses dmsg for signaling) — direct UDP via
//     address resolver, hole-punched through NAT.
//
// Both transports persist past first creation; the goroutine
// reconciles every 5 minutes (or after a failed attempt's backoff
// expires) by re-checking whether the fast transport is still
// present and re-trying if not.
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

		// Skip the work if neither fast transport type is available
		// locally — common on visors that disable stcpr/sudph or that
		// run pure-dmsg builds. Re-check every cycle in case config
		// changes at runtime (RPC API can flip these).
		localSTCPR := v.tpM.IsKnownNetwork(tptypes.STCPR)
		localSUDPH := v.tpM.IsKnownNetwork(tptypes.SUDPH)
		if !localSTCPR && !localSUDPH {
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

		// Try stcpr first — direct TCP, no dmsg signaling needed.
		if localSTCPR {
			if _, err := v.tpM.SaveTransport(ctx, hvPK, tptypes.STCPR, transport.LabelAutomatic); err == nil {
				log.WithField("type", string(tptypes.STCPR)).
					WithField("hypervisor_pk", hvPK).
					Info("Upgraded hypervisor transport")
				backoff = hypervisorTransportReconcileInterval
				continue
			} else if isContextError(err) {
				return
			} else {
				log.WithField("type", string(tptypes.STCPR)).WithError(err).
					Debug("stcpr transport to hypervisor failed; trying sudph")
			}
		}

		// stcpr unavailable or failed — try sudph (UDP, NAT-hole-punch
		// via address resolver signaling).
		if localSUDPH {
			if _, err := v.tpM.SaveTransport(ctx, hvPK, tptypes.SUDPH, transport.LabelAutomatic); err == nil {
				log.WithField("type", string(tptypes.SUDPH)).
					WithField("hypervisor_pk", hvPK).
					Info("Upgraded hypervisor transport")
				backoff = hypervisorTransportReconcileInterval
				continue
			} else if isContextError(err) {
				return
			} else {
				log.WithField("type", string(tptypes.SUDPH)).WithError(err).
					Debug("sudph transport to hypervisor failed")
			}
		}

		// Both attempts failed — back off and try again later. dmsg
		// session remains the working channel.
		if backoff < hypervisorTransportMaxBackoff {
			backoff *= 2
		}
	}
}

// hasFastTransportTo returns true when the transport manager has a
// stcpr or sudph automatic-label transport to the given peer.
func (v *Visor) hasFastTransportTo(remotePK cipher.PubKey) bool {
	if v.tpM == nil {
		return false
	}
	for _, tp := range v.tpM.GetTransportsByLabel(transport.LabelAutomatic) {
		if tp.Remote() != remotePK {
			continue
		}
		switch tp.Type() {
		case tptypes.STCPR, tptypes.SUDPH:
			return true
		}
	}
	return false
}
