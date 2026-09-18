// Package router pkg/router/settings.go c2-net-routing
//
// Runtime-settable router knobs, so `skywire cli route settings` can move the
// dataplane on a running visor instead of a rebuild and a fleet deploy.
//
// The values live in pkg/router/routersettings — one catalog entry per knob,
// each seeded with the constant this package compiled with, so a visor that
// never sets one behaves byte for byte as it did. This file is the bridge: the
// named accessors the rest of the package and pkg/visor call, plus the
// per-route-group resolution of an app's overrides.
//
// Two shapes of read:
//
//   - a package-level accessor (DeadRouteHold, SBDMinSamples, …) reads the
//     visor-wide value. Used where there is no route group in hand;
//   - a *routersettings.View read through rg.knobs() / m.knobs() carries the
//     owning app's overrides. The view is resolved ONCE when the group is built
//     and re-resolved only when the catalog version moves (the group's
//     send-window service tick notices), never per packet.
package router

import (
	"math"
	"time"

	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
)

// ---------------------------------------------------------------------------
// Send-window shape.

// EcfWindowMargin is the multiplier on SACK-proven delivery-per-RTT that sets a
// leg's send window.
func EcfWindowMargin() float64 { return routersettings.EcfWindowMargin.Ratio() }

// SetEcfWindowMargin installs the send-window margin. Non-positive is refused.
func SetEcfWindowMargin(v float64) bool { return setRatio(routersettings.EcfWindowMargin, v) }

// EcfMinWindowBytes is the floor a leg's send window is clamped to.
func EcfMinWindowBytes() int64 { return routersettings.EcfMinWindowBytes.Bytes() }

// SetEcfMinWindowBytes installs the send-window floor. Non-positive is refused.
func SetEcfMinWindowBytes(v int64) bool { return setInt(routersettings.EcfMinWindowBytes, v) }

// EcfMaxWindowBytes is the ceiling a leg's send window is clamped to — the
// number the client-side chunk and upload sizes are sized against.
func EcfMaxWindowBytes() int64 { return routersettings.EcfMaxWindowBytes.Bytes() }

// SetEcfMaxWindowBytes installs the send-window ceiling. Non-positive is refused.
func SetEcfMaxWindowBytes(v int64) bool { return setInt(routersettings.EcfMaxWindowBytes, v) }

// SendWindowWaitMax bounds how long a writer parks when every ready leg is at
// its window before sending anyway.
func SendWindowWaitMax() time.Duration { return routersettings.SendWindowWaitMax.Duration() }

// SetSendWindowWaitMax installs the send-window wait bound. Non-positive is refused.
func SetSendWindowWaitMax(d time.Duration) bool {
	return setInt(routersettings.SendWindowWaitMax, int64(d))
}

// LegParkMinHold is how long an adaptive park holds before any adaptive
// promoter may re-admit the leg.
func LegParkMinHold() time.Duration { return routersettings.LegParkMinHold.Duration() }

// SetLegParkMinHold installs the adaptive park hold. Non-positive is refused.
func SetLegParkMinHold(d time.Duration) bool {
	return setInt(routersettings.LegParkMinHold, int64(d))
}

// ---------------------------------------------------------------------------
// The outclassed-leg gate.

// LegStarveRatio is the one ratio the outclassed-leg ruling uses at both ends:
// a leg is cut to a probe per window only when its delay basis exceeds the best
// ready leg's by more than this AND its proven delivery rate is under 1/this of
// that leg's — so a leg that is slow but productive keeps its share. At or below
// 1 the gate is off.
func LegStarveRatio() float64 { return routersettings.LegStarveRatio.Ratio() }

// SetLegStarveRatio installs that ratio. Only a finite value above 1 is
// accepted — at or below 1 every leg would outclass every other, including
// itself; use a very large ratio to disable the gate instead.
func SetLegStarveRatio(v float64) bool {
	if v <= 1 {
		return false
	}
	return setRatio(routersettings.LegStarveRatio, v)
}

// LegProbeBytes is how many bytes a leg ruled probe-only may carry per window
// (the window being its own delay basis, floored at leg.probe_min_window).
func LegProbeBytes() int64 { return routersettings.LegProbeBytes.Bytes() }

// SetLegProbeBytes installs that budget. Non-positive is refused: a budget of
// zero would silence the leg completely, and the point of the probe is that the
// leg keeps being measured so the ruling can lift.
func SetLegProbeBytes(v int64) bool { return setInt(routersettings.LegProbeBytes, v) }

// LegDelivAlpha is the weight the newest sample carries in a leg's
// SACK-proven delivery EWMA — the reading the goodput half of the
// outclassed-leg gate judges on.
func LegDelivAlpha() float64 { return routersettings.LegDelivAlpha.Ratio() }

// SetLegDelivAlpha installs that weight. Only a value in (0,1] is accepted.
func SetLegDelivAlpha(v float64) bool { return setRatio(routersettings.LegDelivAlpha, v) }

// ---------------------------------------------------------------------------
// Dead-route exclusion.

// DeadRouteHold is the first exclusion window a route that died young is kept
// out of the diversify search for.
func DeadRouteHold() time.Duration { return routersettings.DeadRouteHold.Duration() }

// SetDeadRouteHold installs the first exclusion window. Non-positive is refused.
func SetDeadRouteHold(d time.Duration) bool { return setInt(routersettings.DeadRouteHold, int64(d)) }

// DeadRouteHoldMax caps the doubling applied on each repeat death.
func DeadRouteHoldMax() time.Duration { return routersettings.DeadRouteHoldMax.Duration() }

// SetDeadRouteHoldMax installs the exclusion ceiling. Non-positive is refused.
func SetDeadRouteHoldMax(d time.Duration) bool {
	return setInt(routersettings.DeadRouteHoldMax, int64(d))
}

// ---------------------------------------------------------------------------
// Shared-bottleneck detection.

// SBDEnabled reports whether shared-bottleneck detection runs at all. Off stops
// the sampling as well as the rulings — the explicit switch the rig used to
// fake with an unreachable sbd.min_samples.
func SBDEnabled() bool { return routersettings.SBDEnabled.Bool() }

// SetSBDEnabled turns shared-bottleneck detection on or off. Always accepted.
func SetSBDEnabled(on bool) bool { setBool(routersettings.SBDEnabled, on); return true }

// SBDMinSamples is how many delay samples a leg needs before its statistics are
// trusted for shared-bottleneck grouping.
func SBDMinSamples() int { return routersettings.SBDMinSamples.Int() }

// SetSBDMinSamples installs the shared-bottleneck sample floor. Non-positive is
// refused.
func SetSBDMinSamples(n int) bool { return setInt(routersettings.SBDMinSamples, int64(n)) }

// SBDSampleInterval is the minimum spacing between two per-SACK delay samples
// folded into one leg's window.
func SBDSampleInterval() time.Duration { return routersettings.SBDSampleInterval.Duration() }

// SetSBDSampleInterval installs that spacing. Non-positive is refused.
func SetSBDSampleInterval(d time.Duration) bool {
	return setInt(routersettings.SBDSampleInterval, int64(d))
}

// SBDTrialWindow is how long a shared-bottleneck park is held as a trial before
// the aggregate goodput is re-read.
func SBDTrialWindow() time.Duration { return routersettings.SBDTrialWindow.Duration() }

// SetSBDTrialWindow installs the trial window. Non-positive is refused.
func SetSBDTrialWindow(d time.Duration) bool {
	return setInt(routersettings.SBDTrialWindow, int64(d))
}

// SBDTrialLoss is the fraction of aggregate goodput a park may cost before it
// is undone.
func SBDTrialLoss() float64 { return routersettings.SBDTrialLoss.Ratio() }

// SetSBDTrialLoss installs that fraction. Non-positive is refused.
func SetSBDTrialLoss(v float64) bool { return setRatio(routersettings.SBDTrialLoss, v) }

// SBDBackoff is how long a pair whose park trial failed is exempt from
// shared-bottleneck merging.
func SBDBackoff() time.Duration { return routersettings.SBDBackoff.Duration() }

// SetSBDBackoff installs that exemption. Non-positive is refused.
func SetSBDBackoff(d time.Duration) bool { return setInt(routersettings.SBDBackoff, int64(d)) }

// SBDMinEvidenceRate is the aggregate goodput a group must be carrying before a
// shared-bottleneck ruling may park one of its legs.
func SBDMinEvidenceRate() int64 { return routersettings.SBDMinEvidenceRate.Bytes() }

// SetSBDMinEvidenceRate installs that floor. Non-positive is refused.
func SetSBDMinEvidenceRate(v int64) bool { return setInt(routersettings.SBDMinEvidenceRate, v) }

// SBDDemote reports whether a shared-bottleneck ruling may PARK a leg at all.
func SBDDemote() bool { return routersettings.SBDDemote.Bool() }

// SetSBDDemote turns shared-bottleneck demotion on or off. Always accepted; the
// bool return keeps the shape of the other setters.
func SetSBDDemote(on bool) bool { setBool(routersettings.SBDDemote, on); return true }

// ---------------------------------------------------------------------------
// FORWARD-direction confinement.

// ForwardSpill reports whether a FORWARD frame may leave its confined leg when
// that leg is at its send window.
func ForwardSpill() bool { return routersettings.ForwardSpill.Bool() }

// SetForwardSpill turns forward spill on or off. Always accepted; the bool
// return keeps the shape of the other setters.
func SetForwardSpill(on bool) bool { setBool(routersettings.ForwardSpill, on); return true }

// ForwardSwitchMargin is how much lower a challenger leg must measure before
// the forward direction moves to it.
func ForwardSwitchMargin() float64 { return routersettings.ForwardSwitchMargin.Ratio() }

// SetForwardSwitchMargin installs that margin. Non-positive is refused.
func SetForwardSwitchMargin(v float64) bool {
	return setRatio(routersettings.ForwardSwitchMargin, v)
}

// ForwardSwitchSamples is how many consecutive refreshes a challenger must
// clear the margin for before the direction moves.
func ForwardSwitchSamples() int { return routersettings.ForwardSwitchSamples.Int() }

// ---------------------------------------------------------------------------
// Negotiated capabilities. Each is read when a route group is BUILT, so a group
// already running keeps what it was born with and turning one off never breaks
// a live session.

// PerFrameNoiseEnabled reports whether CapPerFrameNoise is advertised on new
// route groups.
func PerFrameNoiseEnabled() bool { return routersettings.MuxPerFrameNoise.Bool() }

// SACKAdvertised reports whether CapSACK is advertised on new route groups.
func SACKAdvertised() bool { return routersettings.MuxSACK.Bool() }

// HOLRetxAdvertised reports whether CapHOLRetx is advertised on new route
// groups. Implies SACK: the mechanism reuses the SACK feedback, so it is never
// advertised on its own.
func HOLRetxAdvertised() bool {
	return routersettings.MuxHOLRetx.Bool() && routersettings.MuxSACK.Bool()
}

// SetMuxFEC turns FEC advertisement on mux route groups on or off for route
// groups created from now on. Live in the sense the rest of this file is not:
// FEC is negotiated when a group is built, so a group already running keeps the
// setting it was born with.
func (r *router) SetMuxFEC(on bool) {
	r.muxFEC.Store(on)
	setBool(routersettings.FECEnabled, on)
}

// GetMuxFEC reports whether new mux route groups advertise FEC.
func (r *router) GetMuxFEC() bool { return r.muxFEC.Load() }

// ---------------------------------------------------------------------------
// Setter helpers. Each keeps the old bool-returning contract of the per-knob
// setters pkg/visor calls: false means the value was refused.

func setInt(k *routersettings.Knob, v int64) bool {
	return routersettings.SetValue(k.Name(), v) == nil
}

func setRatio(k *routersettings.Knob, v float64) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return false
	}
	return routersettings.SetValue(k.Name(), routersettings.RatioBits(v)) == nil
}

func setBool(k *routersettings.Knob, v bool) {
	_ = routersettings.SetValue(k.Name(), routersettings.BoolBits(v)) //nolint:errcheck // a bool is always in range
}

// ---------------------------------------------------------------------------

// muxHandshakeCaps is the mux capability set this visor advertises: the full
// mask, minus anything its toggle has turned off. SACK and HoL retransmit drop
// out when their knob is off, and HoL is never sent without SACK because it
// reuses the SACK wire message.
//
// It is read when a handshake packet is BUILT, so a group already running keeps
// what it negotiated: turning a capability off degrades new groups only.
func muxHandshakeCaps() uint16 {
	caps := routing.CapMux | routing.CapSACK | routing.CapHOLRetx
	if !SACKAdvertised() {
		caps &^= routing.CapSACK
	}
	if !HOLRetxAdvertised() {
		caps &^= routing.CapHOLRetx
	}
	return caps
}
