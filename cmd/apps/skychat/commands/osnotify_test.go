// Package commands cmd/apps/skychat/commands/osnotify_test.go
//
// Unit coverage for the host-OS notification plumbing that doesn't touch the OS:
// the capability lease + uiCanNotify gate, the /notify-capable handler, and the
// title/preview helpers. The real osnotify backend is never invoked here (see
// TestMain), so no desktop notification is posted during the suite.
package commands

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// appLogRecorder is the package-wide appLog sink, installed ONCE by TestMain.
//
// appLog is a plain global func var that background loops (the pollers, the
// cxo-group Connect/reconnect goroutines, the tcp-direct dialers) call from
// goroutines which routinely outlive the test that started them. Any test that
// assigned appLog itself would therefore race those readers. Installing one
// mutex-guarded recorder up front means nothing ever writes the variable again,
// and a test that wants to assert on log output reads the recorder instead.
type appLogRecorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *appLogRecorder) record(format string, args ...interface{}) {
	r.mu.Lock()
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
	r.mu.Unlock()
}

// count returns how many lines have been recorded so far.
func (r *appLogRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.lines)
}

// since returns the lines recorded after index n.
func (r *appLogRecorder) since(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > len(r.lines) {
		return nil
	}
	return append([]string(nil), r.lines[n:]...)
}

var testAppLog = &appLogRecorder{}

func TestMain(m *testing.M) {
	// Never fire a real host-OS notification during the suite: handleConn-driven
	// tests reach notifyOSInbound, and on a developer desktop
	// osnotify.Available() is true, which would otherwise pop real notifications.
	osNotify = false
	appLog = testAppLog.record
	os.Exit(m.Run())
}

// leaseIsFresh reads the capability lease directly (no clientCount gate), for
// asserting the handler's effect.
func leaseIsFresh() bool {
	notifyCapMu.Lock()
	defer notifyCapMu.Unlock()
	return !notifyCapAt.IsZero() && time.Since(notifyCapAt) < notifyCapableTTL
}

func TestNotifyCapableHandler(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	t.Cleanup(func() { markUINotifyCapable(false) })

	// Wrong method → 405.
	rr := httptest.NewRecorder()
	notifyCapableHandler(rr, httptest.NewRequest(http.MethodGet, "/notify-capable", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: code=%d, want 405", rr.Code)
	}

	// Malformed body → 400.
	rr = httptest.NewRecorder()
	notifyCapableHandler(rr, httptest.NewRequest(http.MethodPost, "/notify-capable", strings.NewReader("{bad")))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad body: code=%d, want 400", rr.Code)
	}

	// capable:true → 204 + a fresh lease.
	markUINotifyCapable(false)
	rr = httptest.NewRecorder()
	notifyCapableHandler(rr, httptest.NewRequest(http.MethodPost, "/notify-capable", strings.NewReader(`{"capable":true}`)))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("capable=true: code=%d", rr.Code)
	}
	if !leaseIsFresh() {
		t.Error("capable=true should set a fresh lease")
	}

	// capable:false → 204 + lease cleared.
	rr = httptest.NewRecorder()
	notifyCapableHandler(rr, httptest.NewRequest(http.MethodPost, "/notify-capable", strings.NewReader(`{"capable":false}`)))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("capable=false: code=%d", rr.Code)
	}
	if leaseIsFresh() {
		t.Error("capable=false should clear the lease")
	}
}

func TestUICanNotify(t *testing.T) {
	origHub := hub
	hub = newSSEHub()
	t.Cleanup(func() {
		hub = origHub
		markUINotifyCapable(false)
	})

	// No SSE client → never "capable UI present", regardless of the lease.
	markUINotifyCapable(true)
	if uiCanNotify() {
		t.Error("no SSE client attached → uiCanNotify must be false")
	}

	// Attach a client. Now a fresh lease means a capable UI is present.
	_, unsub := hub.subscribe()
	t.Cleanup(unsub)

	markUINotifyCapable(true)
	if !uiCanNotify() {
		t.Error("client + fresh lease → uiCanNotify should be true")
	}

	// A stale lease (older than the TTL) → not capable, even with a client.
	notifyCapMu.Lock()
	notifyCapAt = time.Now().Add(-notifyCapableTTL - time.Second)
	notifyCapMu.Unlock()
	if uiCanNotify() {
		t.Error("stale lease → uiCanNotify should be false (Go side takes over)")
	}

	// Client present but browser reported it can't notify → not capable.
	markUINotifyCapable(false)
	if uiCanNotify() {
		t.Error("capable=false → uiCanNotify should be false")
	}
}

// TestShouldNotifyOS pins the no-double-fire rule that survives the move to the
// visor-global hub. skychat keeps deciding WHETHER (it alone knows the focused
// thread and the per-thread mutes); the visor decides WHERE. If this gate ever
// let both tiers through, one inbound message would surface twice — once from
// the browser's Web Notification and once from whichever sink the hub picks.
func TestShouldNotifyOS(t *testing.T) {
	cases := []struct {
		name             string
		enabled, capable bool
		want             bool
	}{
		{"capable UI attached → browser tier owns it", true, true, false},
		{"no capable UI → Go side delivers", true, false, true},
		{"notifications disabled", false, false, false},
		{"disabled and a capable UI", false, true, false},
	}
	for _, c := range cases {
		if got := shouldNotifyOS(c.enabled, c.capable); got != c.want {
			t.Errorf("%s: shouldNotifyOS(%v, %v) = %v, want %v", c.name, c.enabled, c.capable, got, c.want)
		}
	}
}

// TestNotifyOSInboundRespectsDisableFlag guards the TestMain contract: the
// suite flips the single osNotify flag to keep handleConn-driven tests from
// posting real desktop notifications, so that flag must remain the first and
// sufficient short-circuit.
func TestNotifyOSInboundRespectsDisableFlag(t *testing.T) {
	if osNotify {
		t.Fatal("TestMain must leave osNotify=false, or the suite pops real notifications")
	}
	// Must be inert regardless of lease state.
	notifyOSInbound("alice", "hi")
}

func TestShortHexPK(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	hex := pk.Hex()
	got := shortHexPK(hex)
	if got != hex[:8]+"…"+hex[len(hex)-4:] {
		t.Errorf("shortHexPK(%s) = %q", hex, got)
	}
	// A short string is returned unchanged (no panic on slicing).
	if shortHexPK("abc") != "abc" {
		t.Errorf("short input should pass through")
	}
}

func TestFileKindLabel(t *testing.T) {
	cases := map[string]struct{ name, mime, want string }{
		"png image":      {"photo.png", "", "Image"},
		"mov video":      {"clip.MOV", "", "Video"},
		"flac audio":     {"song.flac", "", "Audio"},
		"pdf":            {"paper.pdf", "", "PDF"},
		"zip archive":    {"bundle.zip", "", "Archive"},
		"docx document":  {"notes.docx", "", "Document"},
		"unknown ext":    {"data.bin", "", "File"},
		"no ext":         {"README", "", "File"},
		"mime fallback":  {"weirdname", "image/x-thing", "Image"},
		"ext beats mime": {"a.mp3", "application/octet-stream", "Audio"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := fileKindLabel(tc.name, tc.mime); got != tc.want {
				t.Errorf("fileKindLabel(%q,%q) = %q, want %q", tc.name, tc.mime, got, tc.want)
			}
		})
	}
}

func TestNotifPreview(t *testing.T) {
	if got := notifPreview("  hello   world \n friend "); got != "hello world friend" {
		t.Errorf("collapse whitespace: got %q", got)
	}
	long := strings.Repeat("a", notifPreviewLen+20)
	got := notifPreview(long)
	if r := []rune(got); len(r) != notifPreviewLen+1 || r[notifPreviewLen] != '…' {
		t.Errorf("truncation: len=%d want %d + ellipsis", len([]rune(got)), notifPreviewLen)
	}
}
