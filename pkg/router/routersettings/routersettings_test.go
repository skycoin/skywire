// Package routersettings pkg/router/routersettings/routersettings_test.go
package routersettings

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Every knob must round-trip: Format then Parse is the identity, a set is
// readable back, and Reset restores the compiled default. This is what makes
// the `route settings --json` map a restorable save of the whole surface —
// bench/lib-settings.sh feeds it straight back.
func TestEveryKnobRoundTrips(t *testing.T) {
	t.Cleanup(Reset)
	for _, e := range Snapshot() {
		e := e
		t.Run(e.Name, func(t *testing.T) {
			// Format(default) parses back to exactly the default.
			back, err := Parse(e.Name, e.DefaultStr)
			require.NoError(t, err, "the default must parse back")
			require.Equal(t, e.Def.Default, back, "Format then Parse must be the identity")

			k := Lookup(e.Name)
			require.NotNil(t, k)
			require.False(t, k.IsSet(), "nothing is set before a set")

			raw := bumpedValue(t, k)
			require.NoError(t, Set(e.Name, raw))
			require.True(t, k.IsSet())
			require.Equal(t, raw, FormatKnob(k, k.Raw()), "the set value reads back as written")

			Reset()
			require.Equal(t, e.Def.Default, k.Raw(), "reset restores the compiled default")
			require.False(t, k.IsSet())
		})
	}
}

// bumpedValue returns a legal value DIFFERENT from the knob's default, chosen
// so it also clears the knob's floor and stays under any ceiling.
func bumpedValue(t *testing.T, k *Knob) string {
	t.Helper()
	d := k.Def()
	switch d.Kind {
	case KindBool:
		return FormatKnob(k, 1-d.Default)
	case KindRatio:
		f := math.Float64frombits(uint64(d.Default)) //nolint:gosec
		next := f * 1.5
		if d.MaxRatio > 0 && next >= d.MaxRatio {
			next = (f + d.MinRatio) / 2
			if next <= d.MinRatio {
				next = f / 2
			}
		}
		return FormatKnob(k, RatioBits(next))
	case KindDuration, KindBytes, KindCount:
		return FormatKnob(k, d.Default*2)
	case KindList:
		// The default is the empty list; any tokens differ from it.
		return "aa,bb"
	}
	t.Fatalf("%s: unhandled kind %q", d.Name, d.Kind)
	return ""
}

// A knob's floor is refused, and a refused value leaves the live one alone.
func TestFloorsAreRefused(t *testing.T) {
	t.Cleanup(Reset)
	require.NoError(t, Set(SendWindowRefreshInterval.Name(), "50ms"))
	require.Error(t, Set(SendWindowRefreshInterval.Name(), "1ms"),
		"a cadence below its floor would spin the service loop")
	require.Equal(t, 50*time.Millisecond, SendWindowRefreshInterval.Duration())

	require.Error(t, Set(ForwardSwitchMargin.Name(), "1"), "a margin of 1 could never be cleared")
	require.Error(t, Set(ForwardSwitchMargin.Name(), "0"), "a margin of 0 moves on noise")
	require.Error(t, Set(ReorderWindow.Name(), "0"))
	require.Error(t, Set("no.such.knob", "1"))
}

// A per-app override applies only to that app's view; every other app and the
// visor-wide view keep the global value. Resolution is by VIEW, so a route
// group reads its app's value with one pointer load.
func TestPerAppOverrideResolution(t *testing.T) {
	t.Cleanup(Reset)
	require.NoError(t, Set(LegStarveRatio.Name(), "4"))
	require.NoError(t, SetApp("skysocks-client", LegStarveRatio.Name(), "9"))

	require.Equal(t, 9.0, Resolve("skysocks-client").Ratio(LegStarveRatio))
	require.Equal(t, 4.0, Resolve("skysocks-client-ref").Ratio(LegStarveRatio),
		"an app with no override of its own follows the visor-wide value")
	require.Equal(t, 4.0, Resolve("").Ratio(LegStarveRatio))
	require.Equal(t, 4.0, LegStarveRatio.Ratio(), "the global knob is untouched by an app override")

	// A knob the app did NOT override still follows the global value, and
	// follows it as it moves.
	require.NoError(t, Set(LegProbeBytes.Name(), "32KiB"))
	require.EqualValues(t, 32*1024, Resolve("skysocks-client").Bytes(LegProbeBytes))

	ResetApp("skysocks-client")
	require.Equal(t, 4.0, Resolve("skysocks-client").Ratio(LegStarveRatio),
		"dropping the override falls back to the visor-wide value")
}

// A nil view reads the live globals, so a route group built before its holder
// was attached still behaves.
func TestNilViewReadsGlobals(t *testing.T) {
	t.Cleanup(Reset)
	require.NoError(t, Set(RackCeil.Name(), "900ms"))
	var v *View
	require.Equal(t, 900*time.Millisecond, v.Duration(RackCeil))
	require.Equal(t, "", v.App())
}

// A Holder resolves once and re-resolves only when the catalog version moves —
// the contract that keeps resolution off the per-packet path.
func TestHolderRefreshesOnlyOnChange(t *testing.T) {
	t.Cleanup(Reset)
	h := NewHolder("vpn-client")
	first := h.View()
	require.False(t, h.Refresh(), "nothing changed, nothing to re-resolve")
	require.Same(t, first, h.View())

	require.NoError(t, SetApp("vpn-client", RackFloor.Name(), "90ms"))
	require.True(t, h.Refresh())
	require.Equal(t, 90*time.Millisecond, h.View().Duration(RackFloor))
	require.NotSame(t, first, h.View())

	// Re-pointing at another app picks that app's values up at once.
	h.SetApp("skysocks-client")
	require.Equal(t, RackFloor.Duration(), h.View().Duration(RackFloor))
}

// The persistence maps encode only what was explicitly SET and decode back to
// the same live values — a knob left alone keeps following the binary's own
// default, including one a later release changes.
func TestPersistenceMapEncodeDecode(t *testing.T) {
	t.Cleanup(Reset)
	require.Empty(t, Overrides(), "an untouched visor persists nothing")

	require.NoError(t, Set(EcfMaxWindowBytes.Name(), "16MiB"))
	require.NoError(t, Set(MuxSACK.Name(), "false"))
	require.NoError(t, SetApp("skysocks-client", SBDEnabled.Name(), "false"))

	over, apps := Overrides(), AppOverrides()
	require.Equal(t, map[string]string{
		EcfMaxWindowBytes.Name(): "16MiB",
		MuxSACK.Name():           "false",
	}, over)
	require.Equal(t, map[string]map[string]string{
		"skysocks-client": {SBDEnabled.Name(): "false"},
	}, apps)

	// A restart: everything back to defaults, then the maps replayed.
	Reset()
	require.EqualValues(t, 8*1024*1024, EcfMaxWindowBytes.Bytes())
	require.True(t, MuxSACK.Bool())

	require.NoError(t, Apply(over))
	for app, vals := range apps {
		require.NoError(t, ApplyApp(app, vals))
	}
	require.EqualValues(t, 16*1024*1024, EcfMaxWindowBytes.Bytes())
	require.False(t, MuxSACK.Bool())
	require.False(t, Resolve("skysocks-client").Bool(SBDEnabled))
	require.True(t, Resolve("").Bool(SBDEnabled))

	// A map naming a knob this binary no longer accepts is an error, not a
	// silent success: the CLI must not report a typo as applied.
	require.Error(t, Apply(map[string]string{"gone.knob": "1"}))
}

// Apply is PARTIAL: a knob the map does not name is left alone, which is what
// lets a sweep move one value without restating the other eighty.
func TestApplyIsPartial(t *testing.T) {
	t.Cleanup(Reset)
	require.NoError(t, Set(RackCeil.Name(), "800ms"))
	require.NoError(t, Apply(map[string]string{RackFloor.Name(): "70ms"}))
	require.Equal(t, 800*time.Millisecond, RackCeil.Duration())
	require.Equal(t, 70*time.Millisecond, RackFloor.Duration())
}

// Names are stable and unique — the map key a bench runner saves must not move.
func TestCatalogNamesAreUniqueAndDotted(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Catalog() {
		require.False(t, seen[d.Name], "duplicate knob %s", d.Name)
		seen[d.Name] = true
		require.Contains(t, d.Name, ".", "%s should be group.name", d.Name)
		require.NotEmpty(t, d.Doc, "%s needs a doc line — it is the CLI's help", d.Name)
	}
	require.Greater(t, len(seen), 60, "the catalog should cover the whole dataplane")
}
