// Package clirpc routingsession_test.go — mux is applied at the value passed.
package clirpc

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor"
)

// muxRecorder is a visor.API that records SetMuxRoutes calls and no-ops the
// other two setters ApplyRoutingSession may reach. The embedded nil interface
// supplies the rest of the (large) API surface: any method this test does not
// expect to be called panics rather than silently passing, which is the
// property being bought here.
type muxRecorder struct {
	visor.API
	calls []int
}

func (m *muxRecorder) SetMuxRoutes(n int) error {
	m.calls = append(m.calls, n)
	return nil
}

func (m *muxRecorder) SetExistingTPOnly(bool) error   { return nil }
func (m *muxRecorder) SetForceLocalRoutes(bool) error { return nil }

// TestApplyRoutingSession_MuxRoutes locks in that mux is applied at exactly the
// value passed, 1 included.
//
// 1 used to be skipped, on the reading that it is the default and therefore a
// no-op. It is not: SetMuxRoutes writes the router runtime, the live
// networker's dial default, and the on-disk config, so skipping it meant
// inheriting whatever the previous caller set. A `proxy start --mux 2` left
// every later plain `proxy start` on that visor multiplexed across restarts,
// and --mux 1 — the one way to ask for it back — was the request being dropped.
// The docker e2e found this the expensive way: TestMux set 2, and TestSkysocks
// and TestMultiHopRoute then ran with mux on while documenting it as off,
// flapping skysocks-client against the 6s route-group handshake window.
func TestApplyRoutingSession_MuxRoutes(t *testing.T) {
	tests := []struct {
		name string
		mux  *int
		want []int
	}{
		{name: "one disables rather than skipping", mux: new(1), want: []int{1}},
		{name: "zero means unlimited", mux: new(0), want: []int{math.MaxInt32}},
		{name: "explicit count is passed through", mux: new(3), want: []int{3}},
		{name: "nil leaves the setting untouched", mux: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &muxRecorder{}
			require.NoError(t, ApplyRoutingSession(rec, RoutingSessionOpts{MuxRoutes: tt.mux}))
			require.Equal(t, tt.want, rec.calls)
		})
	}
}
