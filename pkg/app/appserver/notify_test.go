// Package appserver pkg/app/appserver/notify_test.go
//
// Coverage for the app→visor notification seam. The security-relevant property
// is app attribution: the App field is taken from the calling proc's own
// identity and OVERWRITES whatever arrived on the wire. Without that, any app
// could publish under another's name — which downstream maps to a per-app
// notification channel the user may have deliberately muted.
package appserver

import (
	"sync"
	"testing"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/logging"
)

// hubRecorder captures what the visor-side hub would receive.
type hubRecorder struct {
	mu  sync.Mutex
	got []NotifyReq
}

func (h *hubRecorder) Publish(n NotifyReq) {
	h.mu.Lock()
	h.got = append(h.got, n)
	h.mu.Unlock()
}

func (h *hubRecorder) calls() []NotifyReq {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]NotifyReq(nil), h.got...)
}

func TestRPCIngressGateway_Notify_OverridesAppName(t *testing.T) {
	hub := &hubRecorder{}
	proc := &Proc{conf: appcommon.ProcConfig{AppName: "skychat"}, notifyHub: hub}
	gw := NewRPCGateway(logging.MustGetLogger("rpc_gateway"), proc)

	// The app claims to be something else; the gateway must not believe it.
	var reply struct{}
	if err := gw.Notify(&NotifyReq{App: "vpn-client", Title: "alice", Body: "hi", Tag: "dm"}, &reply); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	got := hub.calls()
	if len(got) != 1 {
		t.Fatalf("hub received %d notifications, want 1", len(got))
	}
	if got[0].App != "skychat" {
		t.Errorf("App = %q, want skychat (the proc's own identity, not the claim)", got[0].App)
	}
	if got[0].Title != "alice" || got[0].Body != "hi" || got[0].Tag != "dm" {
		t.Errorf("payload = %+v, want title/body/tag passed through untouched", got[0])
	}
}

func TestRPCIngressGateway_Notify_StampsEmptyAppName(t *testing.T) {
	// The normal case: apps leave App empty and the visor fills it in.
	hub := &hubRecorder{}
	proc := &Proc{conf: appcommon.ProcConfig{AppName: "skydex-client"}, notifyHub: hub}
	gw := NewRPCGateway(logging.MustGetLogger("rpc_gateway"), proc)

	var reply struct{}
	if err := gw.Notify(&NotifyReq{Title: "SkyDEX", Body: "Connected"}, &reply); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	got := hub.calls()
	if len(got) != 1 || got[0].App != "skydex-client" {
		t.Fatalf("hub got %+v, want App=skydex-client", got)
	}
}

func TestRPCIngressGateway_Notify_NilRequestErrors(t *testing.T) {
	gw := NewRPCGateway(logging.MustGetLogger("rpc_gateway"), &Proc{})

	var reply struct{}
	if err := gw.Notify(nil, &reply); err == nil {
		t.Error("Notify(nil) should error rather than panic")
	}
}

func TestRPCIngressGateway_Notify_NilProcIsNoOp(t *testing.T) {
	// Many tests in this package build a gateway with a nil proc; the
	// notification path must tolerate it.
	gw := NewRPCGateway(logging.MustGetLogger("rpc_gateway"), nil)

	var reply struct{}
	if err := gw.Notify(&NotifyReq{Title: "x"}, &reply); err != nil {
		t.Errorf("Notify with a nil proc should be a silent no-op, got %v", err)
	}
}

func TestProc_Notify_NilHubIsNoOp(t *testing.T) {
	// A bare &Proc{} (and the browser wasm-visor, which passes no hub) must
	// drop notifications rather than panic.
	p := &Proc{conf: appcommon.ProcConfig{AppName: "skychat"}}
	p.Notify(NotifyReq{Title: "x"}) // must not panic
}
