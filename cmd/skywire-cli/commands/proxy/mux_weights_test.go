// Package skysocksc cmd/skywire-cli/commands/proxy/mux_weights_test.go c4-vis-cli
package skysocksc

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/router"
)

func TestParseLegWeights(t *testing.T) {
	got, err := parseLegWeights("55d43098-bae7-029e-bd8e-b228f7208930=3,9f20c611-0d4a-4b45-8f88-1f6b1b6a6e51=1")
	require.NoError(t, err)
	require.Equal(t, map[string]float64{
		"55d43098-bae7-029e-bd8e-b228f7208930": 3,
		"9f20c611-0d4a-4b45-8f88-1f6b1b6a6e51": 1,
	}, got)

	// Fractions, spaces and a trailing comma are all accepted — an operator
	// should not have to hunt for a whitespace error.
	got, err = parseLegWeights(" a=0.667 , b=0.333 ,")
	require.NoError(t, err)
	require.Equal(t, map[string]float64{"a": 0.667, "b": 0.333}, got)

	// Zero is legal (the minimum share); negative and non-assignments are not.
	got, err = parseLegWeights("a=0,b=1")
	require.NoError(t, err)
	require.Equal(t, 0.0, got["a"])

	_, err = parseLegWeights("a=-1")
	require.ErrorContains(t, err, "negative")
	_, err = parseLegWeights("a")
	require.ErrorContains(t, err, "not <tp-id>=<weight>")
	_, err = parseLegWeights("a=fast")
	require.ErrorContains(t, err, "not a number")
	_, err = parseLegWeights(",,")
	require.ErrorContains(t, err, "no weights given")
}

func TestMuxWeightsOut(t *testing.T) {
	out := muxWeightsOut("skysocks-client", router.MuxWeightsView{
		DstPort: 49170, SrcPort: 3, Mode: "weighted",
		Weights: []router.LegWeight{{TransportID: "a", Weight: 2}},
	})
	require.Equal(t, "skysocks-client", out.App)
	require.Equal(t, uint16(49170), out.DstPort)
	require.Equal(t, "weighted", out.Mode)
	require.Len(t, out.Weights, 1)
	require.Equal(t, 2.0, out.Weights[0].Weight)
}

// The forward-only pin is a flag on the EXISTING `mux add`, and the default
// (unset) path must still be the original full-duplex one.
func TestMuxAddForwardOnlyFlagIsRegistered(t *testing.T) {
	f := muxAddCmd.Flags().Lookup("forward-only")
	require.NotNil(t, f, "`proxy mux add --forward-only` must exist")
	require.Equal(t, "false", f.DefValue, "full duplex stays the default")
	require.NotNil(t, muxAddCmd.Run)
	require.False(t, muxAddForwardOnly)
}

// `proxy mux negotiated` is registered as a mux subcommand.
func TestMuxNegotiatedCommandIsRegistered(t *testing.T) {
	var found bool
	for _, c := range muxCmd.Commands() {
		if c.Name() == "negotiated" {
			found = true
			break
		}
	}
	require.True(t, found, "`proxy mux negotiated` must be registered")
}

// `proxy mux weights` is registered, with --auto and --rg.
func TestMuxWeightsCommandIsRegistered(t *testing.T) {
	var found bool
	for _, c := range muxCmd.Commands() {
		if c.Name() == "weights" {
			found = true
			require.NotNil(t, c.Flags().Lookup("auto"))
			require.NotNil(t, c.Flags().Lookup("rg"))
			break
		}
	}
	require.True(t, found, "`proxy mux weights` must be registered")
}
