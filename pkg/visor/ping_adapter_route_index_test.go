// Package visor pkg/visor/ping_adapter_route_index_test.go
//
// Pins the visorPingAdapter contract that conf.RouteIndex (and
// conf.MinHops) cross the rpcgrpc.PingConf → visor.PingConfig
// boundary intact. Pre-fix, the PingOnceWithEcho adapter silently
// dropped both fields — every PingOnceWithEcho call from the
// mux-bw pump goroutine looked up the ping connection at
// PingRouteRef{PK, Index: 0} regardless of the goroutine's own
// RouteIndex, so aux routes (Index >= 1) registered via DialPing
// were unreachable. The observable failure was "no ping connection
// for <pk>#0, call DialPing first" emitted via MuxRouteFailure
// even when the route was successfully established at the matching
// index — surfaced by #2756 (Beta's pump-phase failure event),
// which is how this bug became diagnosable from the wire.
//
// Strategy: with no ping connection registered, PingOnceWithEcho
// returns an error whose message contains "%s#%d" with conf.PK
// and conf.RouteIndex. If the adapter forwards RouteIndex
// correctly, the error mentions the requested index (e.g. "#3");
// if it doesn't, the error mentions "#0". So the error-message
// content IS the regression signal — no fixture / mock needed.

package visor

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

func TestPingAdapter_PingOnceWithEcho_ForwardsRouteIndex(t *testing.T) {
	v := &Visor{ping: pingState{conns: make(map[PingRouteRef]ping), mu: &sync.Mutex{}}}
	a := &visorPingAdapter{v: v}

	// No ping connection registered for the requested (PK, Index).
	// The adapter should pass RouteIndex through, so the resulting
	// error message names the same index we asked for.
	for _, idx := range []int{0, 1, 2, 7} {
		_, _, _, err := a.PingOnceWithEcho(rpcgrpc.PingConf{
			RouteIndex: idx,
			PcktSize:   1,
		}, false)
		if err == nil {
			t.Fatalf("idx=%d: expected error for unregistered conn, got nil", idx)
		}
		// Error format: "no ping connection for %s#%d, call DialPing first"
		// We assert the index portion verbatim so a regression that drops
		// RouteIndex to zero surfaces immediately.
		want := "#" + strconv.Itoa(idx) + ","
		if !strings.Contains(err.Error(), want) {
			t.Errorf("idx=%d: error %q does not contain %q — adapter dropped RouteIndex?",
				idx, err.Error(), want)
		}
	}
}
