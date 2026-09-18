// Package proxystatus tests for how the live skysocks page rides out a restart
// of the very proxy that serves it.
package proxystatus

import (
	"fmt"
	"strings"
	"testing"
)

// TestLiveScriptNeverReloadsBlind is the regression test for the status page
// falling back to a dead tab by itself. The old script armed an unconditional
// location.reload() 15 s after the WebSocket first closed — a full navigation
// through a SOCKS proxy that, during a visor/proxy restart, is refusing
// connections, so the browser replaced the live page with its own proxy-error
// page (which has no retry). Any reload must now be preceded by a probe that
// proves the page is fetchable.
func TestLiveScriptNeverReloadsBlind(t *testing.T) {
	s := liveScript
	if !strings.Contains(s, "function probeReload()") {
		t.Fatal("liveScript lost the probing reload guard")
	}
	// Every location.reload() must sit inside the probe's success branch.
	if n := strings.Count(s, "location.reload()"); n != 1 {
		t.Fatalf("location.reload() appears %d times; the only one allowed is the probed reload", n)
	}
	if !strings.Contains(s, `fetch(location.pathname,{cache:"no-store"`) {
		t.Error("the reload must be gated on a no-store fetch of the page's own URL")
	}
	if !strings.Contains(s, "if(r&&r.ok){location.reload();}") {
		t.Error("the reload must fire only for an ok probe response")
	}
	if strings.Contains(s, "},15000)") {
		t.Error("the unconditional 15 s reload timer is back")
	}
}

// TestLiveScriptReconnectsForeverWithCappedBackoff pins the recovery loop: the
// page reconnects indefinitely, backing off to a cap rather than hammering, and
// snaps back to the base cadence the moment a frame arrives. A reload is only
// even considered after wsReloadAfterMs of continuous downtime — well past a
// normal restart, so the ordinary case heals with no navigation at all.
func TestLiveScriptReconnectsForeverWithCappedBackoff(t *testing.T) {
	s := liveScript
	for _, want := range []string{
		fmt.Sprintf("bo=%d,", wsBackoffBaseMs),
		fmt.Sprintf("BASE=%d,MAX=%d,RELOAD=%d;", wsBackoffBaseMs, wsBackoffMaxMs, wsReloadAfterMs),
		"bo=Math.min(Math.round(bo*1.6),MAX)",
		"Date.now()-down>RELOAD",
		"bo=BASE;down=0;",
		`window.addEventListener("online",wake)`,
		`document.addEventListener("visibilitychange"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("liveScript missing %q", want)
		}
	}
	if wsReloadAfterMs <= wsBackoffMaxMs {
		t.Errorf("wsReloadAfterMs (%d) must be well past the backoff cap (%d)", wsReloadAfterMs, wsBackoffMaxMs)
	}
	// The reconnect must be re-armed from onclose, forever — not from a bounded
	// attempt counter.
	if !strings.Contains(s, `ws.onclose=function(){ws=null;if(!down){down=Date.now();}stat("reconnecting","warn");sched();};`) {
		t.Error("onclose must re-arm the reconnect unconditionally")
	}
}

// TestLiveSurfaceStillHasNoMetaRefresh guards the property the WebSocket
// replaced: the skysocks page must not regain a meta refresh while adding the
// reconnect hardening above.
func TestLiveSurfaceStillHasNoMetaRefresh(t *testing.T) {
	page := string(Render(Snapshot{Surface: SurfaceSkysocks}))
	if strings.Contains(page, `http-equiv="refresh"`) {
		t.Error("skysocks status page must stay meta-refresh-free")
	}
	if !strings.Contains(page, "function probeReload()") {
		t.Error("rendered skysocks page is missing the live recovery script")
	}
}
