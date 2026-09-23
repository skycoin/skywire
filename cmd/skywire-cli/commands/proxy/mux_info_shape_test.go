// Package skysocksc cmd/skywire-cli/commands/proxy/mux_info_shape_test.go c2-net-routing
//
// The `proxy mux info` header, where a session's SHAPE and its move history
// are read. Both halves regressed live on 2026-09-23: the header printed
// "shape=4x1(mux.shape)" for a session holding 1,2,1,1, and it never printed
// last= or moves[] at all, because the visor was keying the move ledger on an
// app name that is empty unless that app has an override of its own.
package skysocksc

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// captureStdout runs f with os.Stdout redirected and returns what it printed.
// The render path writes with fmt.Printf, so this is the only seam.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r) //nolint:errcheck // the pipe closes with w
		done <- buf.String()
	}()
	f()
	require.NoError(t, w.Close())
	os.Stdout = saved
	return <-done
}

// The header must carry the measured shape, the last move and the move tally
// on the tunnel it was measured from.
func TestMuxInfoHeaderShowsShapeAndMoves(t *testing.T) {
	rgs := []muxRouteGroupInfo{{
		MuxEnabled:  false,
		TunnelRole:  "active",
		Shape:       "2,1,1,1",
		ShapeTarget: "4x1",
		ShapeSource: "mux.shape",
		LastMove: &muxShapeMoveInfo{
			Move: "pool_leg_released", From: "2,2", To: "2,1,1",
			Reason: "shape 2x2 -> 4x1", At: time.Now(),
		},
		MoveCounts: map[string]uint64{"pool_leg_released": 2, "tunnel_promoted": 1},
	}}

	out := captureStdout(t, func() { newMuxRateTracker().render(&cobra.Command{}, rgs) })

	require.Contains(t, out, "shape=2,1,1,1->4x1(mux.shape)",
		"the header must report the shape the session HAS, never the target standing in for it")
	require.NotContains(t, out, "shape=4x1(mux.shape)")
	require.Contains(t, out, "last=pool_leg_released", "the last shape move belongs in the header (#5142)")
	require.Contains(t, out, "moves[pool_leg_released=2 tunnel_promoted=1]", "and so does the tally")
}
