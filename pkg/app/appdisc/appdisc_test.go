package appdisc

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// Shared fake service-discovery server and keypair for the whole test binary.
// servicedisc caches its auth client as a package-level singleton keyed on the
// first (DiscAddr, PK) it sees, so every test that triggers Register/DeleteEntry
// must go through the SAME server and key — hence the shared state here.
var (
	sdServer  *httptest.Server
	sdPostN   atomic.Int64
	sdDeleteN atomic.Int64
	testPK    cipher.PubKey
	testSK    cipher.SecKey
)

func TestMain(m *testing.M) {
	logging.Disable()
	testPK, testSK = cipher.GenerateKeyPair()

	sdServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/security/nonces/"):
			// httpauth handshake: hand back a nonce for this key.
			fmt.Fprintf(w, `{"edge":"%s","next_nonce":1}`, testPK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/services":
			sdPostN.Add(1)
			// Must be valid JSON: postEntry decodes the body into a Service.
			_, _ = w.Write([]byte("{}"))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/services/"):
			sdDeleteN.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	code := m.Run()
	sdServer.Close()
	os.Exit(code)
}

func testLog() logging.MasterLogger { return *logging.NewMasterLogger() }

// liveClient builds a servicedisc client wired to the shared fake SD server.
// Type VPN avoids the visor-only local-IP discovery path in RegisterEntry.
func liveClient() *servicedisc.HTTPClient {
	conf := servicedisc.Config{
		Type:     servicedisc.ServiceTypeVPN,
		PK:       testPK,
		SK:       testSK,
		Port:     0,
		DiscAddr: sdServer.URL,
	}
	ml := testLog()
	return servicedisc.NewClient(&ml, &ml, conf, &http.Client{}, "")
}

// --- emptyUpdater ---

func TestEmptyUpdater(t *testing.T) {
	// emptyUpdater is a no-op Updater; just confirm it satisfies the interface
	// and its methods don't panic. (Go's cover tool doesn't mark empty {}
	// bodies, so these stay at 0% regardless.)
	var u Updater = &emptyUpdater{}
	u.Start()
	u.Stop()
}

// --- Factory.setDefaults ---

func TestSetDefaults(t *testing.T) {
	t.Run("fills empty fields", func(t *testing.T) {
		f := &Factory{}
		f.setDefaults()
		if f.Log == nil {
			t.Error("Log not defaulted")
		}
		if f.ServiceDisc == "" {
			t.Error("ServiceDisc not defaulted")
		}
		if f.HeartbeatInterval != skyenv.ServiceDiscUpdateInterval {
			t.Errorf("HeartbeatInterval = %v, want default", f.HeartbeatInterval)
		}
	})
	t.Run("keeps provided fields", func(t *testing.T) {
		ml := testLog()
		f := &Factory{Log: &ml, ServiceDisc: "http://sd.local", HeartbeatInterval: 3 * time.Second}
		f.setDefaults()
		if f.ServiceDisc != "http://sd.local" {
			t.Errorf("ServiceDisc overwritten: %q", f.ServiceDisc)
		}
		if f.HeartbeatInterval != 3*time.Second {
			t.Errorf("HeartbeatInterval overwritten: %v", f.HeartbeatInterval)
		}
	})
}

// --- Factory.VisorUpdater ---

func TestVisorUpdater(t *testing.T) {
	t.Run("null keys yield empty updater", func(t *testing.T) {
		f := &Factory{}
		if _, ok := f.VisorUpdater(0).(*emptyUpdater); !ok {
			t.Error("expected emptyUpdater when keys are null")
		}
	})
	t.Run("valid keys yield service updater", func(t *testing.T) {
		f := &Factory{PK: testPK, SK: testSK, ServiceDisc: sdServer.URL}
		if _, ok := f.VisorUpdater(0).(*serviceUpdater); !ok {
			t.Error("expected *serviceUpdater for valid keys")
		}
	})
}

// --- Factory.PublicVisorUpdater ---

func TestFactoryPublicVisorUpdater(t *testing.T) {
	t.Run("null keys yield nil", func(t *testing.T) {
		f := &Factory{}
		if u := f.PublicVisorUpdater(0, time.Minute, 10, func() int { return 0 }); u != nil {
			t.Error("expected nil when keys are null")
		}
	})
	t.Run("valid keys yield updater", func(t *testing.T) {
		f := &Factory{PK: testPK, SK: testSK, ServiceDisc: sdServer.URL}
		if u := f.PublicVisorUpdater(0, time.Minute, 10, func() int { return 0 }); u == nil {
			t.Error("expected non-nil PublicVisorUpdater for valid keys")
		}
	})
}

// --- Factory.AppUpdater ---

func TestAppUpdater(t *testing.T) {
	valid := func() *Factory { return &Factory{PK: testPK, SK: testSK, ServiceDisc: sdServer.URL} }

	tests := []struct {
		name    string
		factory *Factory
		conf    appcommon.ProcConfig
		wantOK  bool
		wantSvc bool // true => *serviceUpdater, false => *emptyUpdater
	}{
		{"null keys", &Factory{}, appcommon.ProcConfig{AppName: skyenv.VPNServerName}, false, false},
		{"passcode protected", valid(), appcommon.ProcConfig{AppName: skyenv.VPNServerName, ProcArgs: []string{"-passcode", "secret"}}, false, false},
		{"whitelist protected", valid(), appcommon.ProcConfig{AppName: skyenv.VPNServerName, ProcArgs: []string{"-whitelist", "pk1"}}, false, false},
		{"vpn server", valid(), appcommon.ProcConfig{AppName: skyenv.VPNServerName}, true, true},
		{"skysocks", valid(), appcommon.ProcConfig{AppName: skyenv.SkysocksName}, true, true},
		{"unknown app", valid(), appcommon.ProcConfig{AppName: "some-random-app"}, false, false},
		{"empty passcode flag falls through", valid(), appcommon.ProcConfig{AppName: skyenv.SkysocksName, ProcArgs: []string{"-passcode"}}, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u, ok := tc.factory.AppUpdater(tc.conf)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			_, isSvc := u.(*serviceUpdater)
			if isSvc != tc.wantSvc {
				t.Errorf("isServiceUpdater = %v, want %v", isSvc, tc.wantSvc)
			}
		})
	}
}

// --- newServiceUpdater ---

func TestNewServiceUpdater_DefaultInterval(t *testing.T) {
	ml := testLog()
	u := newServiceUpdater(&ml, liveClient(), 0)
	if u.heartbeatInterval != skyenv.ServiceDiscUpdateInterval {
		t.Errorf("interval = %v, want default", u.heartbeatInterval)
	}
	u2 := newServiceUpdater(&ml, liveClient(), 7*time.Second)
	if u2.heartbeatInterval != 7*time.Second {
		t.Errorf("interval = %v, want 7s", u2.heartbeatInterval)
	}
}

// --- serviceUpdater lifecycle (against the live fake SD) ---

func TestServiceUpdater_StartStop(t *testing.T) {
	ml := testLog()
	u := newServiceUpdater(&ml, liveClient(), time.Hour) // long interval: no heartbeat ticks
	u.Start()
	u.Stop()
	u.Stop() // idempotent
}

func TestServiceUpdater_Heartbeat(t *testing.T) {
	ml := testLog()
	u := newServiceUpdater(&ml, liveClient(), 15*time.Millisecond)

	before := sdPostN.Load()
	u.Start()
	defer u.Stop()

	// Wait for at least one heartbeat tick beyond the initial Start register.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sdPostN.Load() >= before+2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected >=2 register POSTs (initial + heartbeat), got %d", sdPostN.Load()-before)
}

func TestServiceUpdater_PauseResume(t *testing.T) {
	ml := testLog()
	u := newServiceUpdater(&ml, liveClient(), 15*time.Millisecond)
	u.Start()
	defer u.Stop()

	u.Pause()
	if !u.paused.Load() {
		t.Fatal("expected paused after Pause")
	}
	u.Pause() // idempotent: second call is a no-op

	// While paused, heartbeat ticks must not POST; give the loop time to skip.
	postsAfterPause := sdPostN.Load()
	time.Sleep(60 * time.Millisecond)
	if got := sdPostN.Load(); got != postsAfterPause {
		t.Errorf("paused updater sent %d heartbeats, want 0", got-postsAfterPause)
	}

	u.Resume()
	if u.paused.Load() {
		t.Fatal("expected not paused after Resume")
	}
	u.Resume() // idempotent
}

func TestServiceUpdater_ResumeWithoutStart(t *testing.T) {
	ml := testLog()
	u := newServiceUpdater(&ml, liveClient(), time.Hour)
	// Pause sets the flag (and deletes); Resume must early-return because
	// cancel is nil (Start never ran).
	u.Pause()
	u.Resume()
	if u.paused.Load() {
		t.Error("Resume should have cleared the paused flag")
	}
}

// --- PublicVisorUpdater ---

func TestPublicVisorUpdater_Validation(t *testing.T) {
	ml := testLog()
	u := NewPublicVisorUpdater(&ml, newServiceUpdater(&ml, liveClient(), time.Hour), 0, 0, nil)
	if u.IsValidated() {
		t.Error("should not be validated before any external connection")
	}
	u.OnExternalSTCPR()
	if !u.IsValidated() {
		t.Error("should be validated after external STCPR")
	}
	u.OnExternalSTCPR() // already validated: no-op path
	if !u.IsValidated() {
		t.Error("should remain validated")
	}
}

func TestPublicVisorUpdater_StartStop(t *testing.T) {
	ml := testLog()
	u := NewPublicVisorUpdater(&ml, newServiceUpdater(&ml, liveClient(), time.Hour), 0, 0, nil)
	u.Start()
	u.Stop()
	u.Stop() // idempotent
}

func TestPublicVisorUpdater_TimeoutDeregisters(t *testing.T) {
	ml := testLog()
	u := NewPublicVisorUpdater(&ml, newServiceUpdater(&ml, liveClient(), time.Hour), 20*time.Millisecond, 0, nil)

	// deregister() runs on the monitor goroutine; observe it via the race-free
	// DELETE counter (inner.Stop deletes the entry), not by reading
	// deregisteredFor concurrently.
	delBefore := sdDeleteN.Load()
	u.Start()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if sdDeleteN.Load() > delBefore {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	u.Stop() // joins the monitor goroutine; after this, deregisteredFor is stable.
	if u.deregisteredFor != "no_external_stcpr" {
		t.Fatalf("deregisteredFor = %q, want no_external_stcpr", u.deregisteredFor)
	}
}

func TestPublicVisorUpdater_ValidatedBeforeTimeout(t *testing.T) {
	ml := testLog()
	u := NewPublicVisorUpdater(&ml, newServiceUpdater(&ml, liveClient(), time.Hour), 50*time.Millisecond, 0, nil)
	u.Start()

	// Validate immediately; the timeout must then disable itself rather than
	// deregistering. Also exercises the externalCh wake-up path in the loop.
	u.OnExternalSTCPR()

	time.Sleep(120 * time.Millisecond) // outlive the registration timeout
	u.Stop()                           // join the goroutine before reading deregisteredFor
	if u.deregisteredFor != "" {
		t.Errorf("validated visor was deregistered: %q", u.deregisteredFor)
	}
}
