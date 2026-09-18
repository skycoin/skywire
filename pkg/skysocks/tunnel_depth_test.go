// Package skysocks pkg/skysocks/tunnel_depth_test.go
package skysocks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// The bandwidth-delay depth table. delay is the FULL delay term (the caller
// adds tunnel.depth_margin to the RTT), so each row reads as the plain product.
func TestBDPDepth(t *testing.T) {
	const (
		mib4 = 4 << 20
		mib1 = 1 << 20
	)
	for _, tc := range []struct {
		name  string
		rate  float64
		delay time.Duration
		chunk int64
		want  int
	}{
		{"8 MB/s x 150 ms over 4 MiB is under one chunk — the floor holds it at 2", 8e6, 150 * time.Millisecond, mib4, 2},
		{"40 MB/s x 200 ms over 4 MiB is two chunks in flight", 40e6, 200 * time.Millisecond, mib4, 2},
		{"8 MB/s x 400 ms over 1 MiB needs four", 8e6, 400 * time.Millisecond, mib1, 4},
		{"a very fast, very far tunnel is held at the ceiling", 200e6, time.Second, mib4, 8},
		{"an unmeasured tunnel gets the floor", 0, 0, mib4, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, bdpDepth(tc.rate, tc.delay, tc.chunk, rsDepthMin, rsDepthMax))
		})
	}

	require.Equal(t, 1, bdpDepth(1e6, time.Millisecond, 4<<20, 0, 0), "a nonsense clamp still yields a usable depth")
	require.Equal(t, 3, bdpDepth(8e6, 400*time.Millisecond, 1<<20, 1, 3), "the ceiling governs")
}

// Off — the default — the depth is exactly today's fixed number, whatever the
// meters say. That is the no-flag-gate contract: the mechanism ships inert.
func TestTunnelDepthIsOffByDefault(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	c := &Client{}
	require.Equal(t, rsChunksPerTunnel, c.tunnelDepth(4<<20, false, rsChunksPerTunnel))
	require.Equal(t, uploadConcurrency, c.tunnelDepth(4<<20, true, uploadConcurrency))

	// On, but with nothing measured, it still falls back rather than inventing
	// a depth from an unproven tunnel.
	require.True(t, skysettings.Apply(map[string]int64{
		skysettings.ChunkDepthDynamic:  1,
		skysettings.UploadDepthDynamic: 1,
	}))
	require.Equal(t, rsChunksPerTunnel, c.tunnelDepth(4<<20, false, rsChunksPerTunnel))
	require.Equal(t, uploadConcurrency, c.tunnelDepth(4<<20, true, uploadConcurrency))
}
