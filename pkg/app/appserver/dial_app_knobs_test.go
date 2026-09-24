// Package appserver pkg/app/appserver/dial_app_knobs_test.go
package appserver

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// The per-app width is stamped on a dial that asked for nothing, is bounded by
// the per-app cap, and NEVER overrides a count the app asked for itself.
func TestApplyAppMuxKnobsIsPerApp(t *testing.T) {
	m := &procManager{settings: newAppSettings(), procs: map[string]*Proc{}}
	m.SetAppSettings("skysocks-client", map[string]int64{
		skysettings.MuxWidth: 2,
		skysettings.MuxCap:   3,
	}, nil)

	// MuxRoutes 1 is "form a route group", not a width, so the knob applies —
	// this is the skysocks tunnel case the whole knob exists for.
	group := &DialOptionsReq{MuxRoutes: 1}
	applyAppMuxKnobs(m, "skysocks-client", group)
	require.Equal(t, 2, group.MuxRoutes, "the app's width shapes its own group dial")

	// A second app — the paired reference — is untouched, which is the whole
	// reason the pair moved off the process-global atomics.
	other := &DialOptionsReq{MuxRoutes: 1}
	applyAppMuxKnobs(m, "skysocks-client-2", other)
	require.Equal(t, 1, other.MuxRoutes, "another app inherits the visor-wide value")

	// MuxRoutes 0 asks for the AppDirect shortcut, which is what
	// skysocks-client's first-tunnel fallback dials with once the route group
	// could not be built. A width must NOT widen it back into a group dial, or
	// the fallback re-runs the dial that just failed and the session stops
	// instead of degrading.
	shortcut := &DialOptionsReq{}
	applyAppMuxKnobs(m, "skysocks-client", shortcut)
	require.Zero(t, shortcut.MuxRoutes, "the shortcut dial keeps its shortcut")

	// An explicit per-call count above one wins, clamped by the cap.
	explicit := &DialOptionsReq{MuxRoutes: 8}
	applyAppMuxKnobs(m, "skysocks-client", explicit)
	require.Equal(t, 3, explicit.MuxRoutes, "clamped to the app's cap")

	// A cap alone clamps and stamps nothing.
	m.SetAppSettings("capped", map[string]int64{skysettings.MuxCap: 2}, nil)
	capped := &DialOptionsReq{}
	applyAppMuxKnobs(m, "capped", capped)
	require.Zero(t, capped.MuxRoutes)
	capped = &DialOptionsReq{ReverseMuxRoutes: 6}
	applyAppMuxKnobs(m, "capped", capped)
	require.Equal(t, 2, capped.ReverseMuxRoutes)

	// A width above its own cap is pulled down to it rather than refused.
	m.SetAppSettings("narrow", map[string]int64{
		skysettings.MuxWidth: 9,
		skysettings.MuxCap:   4,
	}, nil)
	narrow := &DialOptionsReq{MuxRoutes: 1}
	applyAppMuxKnobs(m, "narrow", narrow)
	require.Equal(t, 4, narrow.MuxRoutes)

	// An app with neither knob set dials exactly as it does today.
	plain := &DialOptionsReq{}
	applyAppMuxKnobs(m, "vpn-client", plain)
	require.Equal(t, DialOptionsReq{}, *plain)
}

// pool.exclude_pks reaches the router as a peer exclusion, and only when it
// parses — a truncated key excludes nothing and must not be smuggled through.
func TestParsePKListDropsUnparseable(t *testing.T) {
	const pk = "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"
	got := parsePKList([]string{pk, "0281a102", ""})
	require.Len(t, got, 1)
	require.Equal(t, pk, got[0].Hex())
	require.Empty(t, parsePKList(nil))
}

// With no requirement, and with no router to ask, the first-hop type check is
// inert — a dial must never fail because a check could not be made.
func TestCheckFirstHopTypeInertWithoutRequirement(t *testing.T) {
	require.NoError(t, checkFirstHopType(nil, "skysocks-client", 49170, nil))
	require.NoError(t, checkFirstHopType(nil, "skysocks-client", 49170, []string{"stcpr"}))
}
