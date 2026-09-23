// Package routersettings pkg/router/routersettings/shape_test.go
package routersettings

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The mux.shape knob takes auto, <k>x<n> and a comma list, and refuses
// anything else — a refused value leaves the live one alone, so a typo in
// `route settings` cannot blank the shape target.
func TestShapeKnobRejectsGarbage(t *testing.T) {
	t.Cleanup(Reset)
	require.Equal(t, ShapeAuto, MuxShape.Text(), "the compiled default is auto")
	require.False(t, MuxShape.IsSet())

	for _, ok := range []string{"auto", "2x2", "4x1", "1x4", "1,3", "2,1,1", " 2X2 ", "16x1"} {
		require.NoError(t, Set(MuxShape.Name(), ok), "%q must be accepted", ok)
	}
	require.Equal(t, "16x1", MuxShape.Text())

	for _, bad := range []string{"0x2", "2x0", "2x", "x2", "foo", "2x2x2", "-1x2", "1,0", "1,,2", "2.5x1", "1 3"} {
		require.Error(t, Set(MuxShape.Name(), bad), "%q must be refused", bad)
		require.Equal(t, "16x1", MuxShape.Text(), "a refused value changes nothing")
	}

	// An empty value is the default, the way `route settings mux.shape=` reads.
	require.NoError(t, Set(MuxShape.Name(), ""))
	require.Equal(t, ShapeAuto, MuxShape.Text())

	// Set, read back and reset — the round trip the whole catalog promises.
	require.NoError(t, Set(MuxShape.Name(), "2x2"))
	require.True(t, MuxShape.IsSet())
	for _, e := range Snapshot() {
		if e.Name != MuxShape.Name() {
			continue
		}
		require.Equal(t, "2x2", e.Formatted, "the snapshot carries the live value")
		require.Equal(t, ShapeAuto, e.DefaultStr, "and the compiled default as text")
		require.True(t, e.Set)
	}
	Reset()
	require.Equal(t, ShapeAuto, MuxShape.Text())
	require.False(t, MuxShape.IsSet())
}

// A string knob resolves per app, exactly like a numeric one: the app's own
// value for its route groups, the visor-wide value for everything else.
func TestShapeKnobPerAppOverride(t *testing.T) {
	t.Cleanup(Reset)
	require.NoError(t, Set(MuxShape.Name(), "2x2"))
	require.NoError(t, SetApp("skysocks-client", MuxShape.Name(), "4x1"))
	require.Error(t, SetApp("skysocks-client", MuxShape.Name(), "0x1"))

	require.Equal(t, "4x1", Resolve("skysocks-client").Text(MuxShape))
	require.Equal(t, "2x2", Resolve("other-app").Text(MuxShape))
	require.Equal(t, "2x2", Resolve("").Text(MuxShape))
	require.Equal(t, "4x1", AppOverrides()["skysocks-client"][MuxShape.Name()])

	ResetApp("skysocks-client")
	require.Equal(t, "2x2", Resolve("skysocks-client").Text(MuxShape))
}
