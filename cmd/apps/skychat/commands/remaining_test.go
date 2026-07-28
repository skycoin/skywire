// Package commands cmd/apps/skychat/commands/remaining_test.go
//
// The tail of the 0% list: deleteHandler, voiceErrStatus, fileDialFunc,
// startFileXfer's disabled guard, and RunSkychat's flag-parse failure.
//
// Two functions are deliberately NOT covered here and are expected to stay at
// 0%:
//
//   - Execute — on error it calls log.Fatal, which os.Exit(1)s the test binary;
//     on success it runs the whole app. There is no way to drive either arm
//     from a unit test without forking a subprocess, and the body is two lines
//     of cobra plumbing.
//   - RunSkychat past its flag parsing — it opens listeners, dials the visor,
//     starts the HTTP server and blocks. That is the job of the native e2e
//     lane (internal/nativee2e), not of a unit test.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/dm"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// Sentinel errors for the table-driven cases below.
var (
	errNoRoute         = errors.New("no route to peer")
	errVoiceDisabled   = errors.New("voice: disabled")
	errWrappedDisabled = fmt.Errorf("rpc call VoiceActive: %w", errVoiceDisabled)
)

// --- deleteHandler ----------------------------------------------------------

func TestDeleteHandler_Validation(t *testing.T) {
	withLifecycleEnv(t)
	h := deleteHandler(context.Background())
	pk, _ := cipher.GenerateKeyPair()

	cases := []struct {
		name, method, body string
		want               int
	}{
		{"non-POST", http.MethodGet, "", http.StatusMethodNotAllowed},
		{"malformed json", http.MethodPost, `{"pk":`, http.StatusBadRequest},
		{"bad pk", http.MethodPost, `{"pk":"not-a-pk","id":"m-1"}`, http.StatusBadRequest},
		{"empty pk", http.MethodPost, `{"pk":"","id":"m-1"}`, http.StatusBadRequest},
		{"missing id", http.MethodPost, `{"pk":"` + pk.Hex() + `"}`, http.StatusBadRequest},
		{"blank id", http.MethodPost, `{"pk":"` + pk.Hex() + `","id":"   "}`, http.StatusBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h(rr, httptest.NewRequest(c.method, "/delete", strings.NewReader(c.body)))
			if rr.Code != c.want {
				t.Errorf("status = %d, want %d; body=%q", rr.Code, c.want, rr.Body.String())
			}
		})
	}
}

func TestDeleteHandler_NoControllerReturns503(t *testing.T) {
	withLifecycleEnv(t)
	chatCtrl = nil
	pk, _ := cipher.GenerateKeyPair()

	rr := httptest.NewRecorder()
	deleteHandler(context.Background())(rr, httptest.NewRequest(http.MethodPost, "/delete",
		strings.NewReader(`{"pk":"`+pk.Hex()+`","id":"m-1"}`)))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 with no DM controller", rr.Code)
	}
}

func TestDeleteHandler_SendsTombstoneAndTombstonesLocally(t *testing.T) {
	withLifecycleEnv(t)
	cc := newCapturingClient()
	chatCtrl = dm.New(dm.Config{Client: cc, Networks: []appnet.Type{appnet.TypeSkynet}})
	t.Cleanup(func() {
		_ = chatCtrl.Close() //nolint:errcheck
		cc.closeAll()
	})

	raw, unsub := hub.subscribe()
	defer unsub()

	peer, _ := cipher.GenerateKeyPair()
	rr := httptest.NewRecorder()
	deleteHandler(context.Background())(rr, httptest.NewRequest(http.MethodPost, "/delete",
		strings.NewReader(`{"pk":"`+peer.Hex()+`","id":"m-42"}`)))

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rr.Code, rr.Body.String())
	}

	// The peer gets a chat-delete so their UI tombstones it too.
	select {
	case frame := <-cc.frames:
		var env message.Envelope
		if err := json.Unmarshal(frame, &env); err != nil {
			t.Fatalf("wire frame is not an envelope: %v (%q)", err, frame)
		}
		if env.Type != message.TypeDelete || env.ID != "m-42" {
			t.Errorf("wire envelope = %+v, want a chat-delete for m-42", env)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no chat-delete reached the wire")
	}

	// And our own bubble tombstones immediately via a dm-status event.
	got := waitForString(t, raw, 2*time.Second)
	var m map[string]string
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("dm-status is not JSON: %v (%q)", err, got)
	}
	if m["channel"] != "dm-status" || m["id"] != "m-42" ||
		m["status"] != dmStatusDeleted || m["peer"] != peer.Hex() {
		t.Errorf("dm-status = %v, want a deleted status for m-42/%s", m, peer.Hex()[:8])
	}
}

// TestDeleteHandler_UnreachablePeerStillTombstonesLocally — the operator's
// intent is recorded even when the peer cannot be told. Failing the request
// here would leave the deleter staring at a message they just deleted.
func TestDeleteHandler_UnreachablePeerStillTombstonesLocally(t *testing.T) {
	withLifecycleEnv(t)
	cc := newCapturingClient()
	cc.setDialErr(errNoRoute)
	chatCtrl = dm.New(dm.Config{Client: cc, Networks: []appnet.Type{appnet.TypeSkynet}})
	t.Cleanup(func() {
		_ = chatCtrl.Close() //nolint:errcheck
		cc.closeAll()
	})

	raw, unsub := hub.subscribe()
	defer unsub()

	peer, _ := cipher.GenerateKeyPair()
	rr := httptest.NewRecorder()
	deleteHandler(context.Background())(rr, httptest.NewRequest(http.MethodPost, "/delete",
		strings.NewReader(`{"pk":"`+peer.Hex()+`","id":"m-offline"}`)))

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 even when the peer is unreachable", rr.Code)
	}
	got := waitForString(t, raw, 2*time.Second)
	if !strings.Contains(got, "m-offline") || !strings.Contains(got, dmStatusDeleted) {
		t.Errorf("local tombstone missing; broadcast = %q", got)
	}
}

// --- voiceErrStatus ---------------------------------------------------------

// TestVoiceErrStatus — a DISABLED voice manager (no dmsg, no audio device) must
// map to 503 so the browser hides the call controls entirely; every other
// proxy failure is a 502 the UI can surface as a transient error.
func TestVoiceErrStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil error", nil, http.StatusBadGateway},
		{"voice disabled", errVoiceDisabled, http.StatusServiceUnavailable},
		{"disabled inside a wrapped message", errWrappedDisabled, http.StatusServiceUnavailable},
		{"unrelated failure", errNoRoute, http.StatusBadGateway},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := voiceErrStatus(c.err); got != c.want {
				t.Errorf("voiceErrStatus(%v) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}

// --- file transport guards --------------------------------------------------

// TestFileDialFunc_NoAppClient — in --standalone mode there is no app client,
// so a file dial must report that rather than nil-deref.
func TestFileDialFunc_NoAppClient(t *testing.T) {
	withLifecycleEnv(t)
	origApp := appCl
	appCl = nil
	t.Cleanup(func() { appCl = origApp })

	peer, _ := cipher.GenerateKeyPair()
	conn, err := fileDialFunc(context.Background(), peer, 1)
	if err == nil || conn != nil {
		t.Errorf("fileDialFunc with no app client = (%v, %v), want (nil, error)", conn, err)
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("err = %v, want it to name the missing app client", err)
	}
}

// TestStartFileXfer_NoAppClientIsNoOp — same standalone guard on the listener
// side: no app client means no file transport, silently.
func TestStartFileXfer_NoAppClientIsNoOp(t *testing.T) {
	withLifecycleEnv(t)
	origApp, origMgr := appCl, fileMgr
	appCl = nil
	fileMgrMu.Lock()
	fileMgr = nil
	fileMgrMu.Unlock()
	t.Cleanup(func() {
		appCl = origApp
		fileMgrMu.Lock()
		fileMgr = origMgr
		fileMgrMu.Unlock()
	})

	startFileXfer(context.Background())

	fileMgrMu.Lock()
	defer fileMgrMu.Unlock()
	if fileMgr != nil {
		t.Error("startFileXfer should not build a manager without an app client")
	}
}

// --- RunSkychat flag parsing ------------------------------------------------

// snapshotFlagGlobals saves and restores every package global RunSkychat's
// FlagSet binds. pflag writes each default into its target as soon as the flag
// is registered — before Parse can fail — so even the error path rewrites all
// of them.
func snapshotFlagGlobals(t *testing.T) {
	t.Helper()
	type snap struct {
		addr                                   string
		portless                               bool
		appPort                                uint16
		useSkynet, useDmsg, osNotify           bool
		passwordFile, internalToken            string
		persistEnabled                         bool
		persistDBPath                          string
		persistMaxMsgSize, persistPerPeerRate  int
		persistPerPeerCap, persistTotalCapMB   int
		persistTTLDays                         int
		persistWhitelistFile                   string
		persistSeedCount                       int
		pairEnable                             bool
		pairRPCAddr                            string
		pairPollInterval                       time.Duration
		tcpListen                              string
		tcpPeers                               []string
		tcpWhitelist, tcpSKFlag, tcpConfigPath string
	}
	s := snap{
		addr, portless, appPort, useSkynet, useDmsg, osNotify,
		passwordFile, internalToken, persistEnabled, persistDBPath,
		persistMaxMsgSize, persistPerPeerRate, persistPerPeerCap,
		persistTotalCapMB, persistTTLDays, persistWhitelistFile,
		persistSeedCount, pairEnable, pairRPCAddr, pairPollInterval,
		tcpListen, tcpPeers, tcpWhitelist, tcpSKFlag, tcpConfigPath,
	}
	t.Cleanup(func() {
		addr, portless, appPort = s.addr, s.portless, s.appPort
		useSkynet, useDmsg, osNotify = s.useSkynet, s.useDmsg, s.osNotify
		passwordFile, internalToken = s.passwordFile, s.internalToken
		persistEnabled, persistDBPath = s.persistEnabled, s.persistDBPath
		persistMaxMsgSize, persistPerPeerRate = s.persistMaxMsgSize, s.persistPerPeerRate
		persistPerPeerCap, persistTotalCapMB = s.persistPerPeerCap, s.persistTotalCapMB
		persistTTLDays, persistWhitelistFile = s.persistTTLDays, s.persistWhitelistFile
		persistSeedCount, pairEnable = s.persistSeedCount, s.pairEnable
		pairRPCAddr, pairPollInterval = s.pairRPCAddr, s.pairPollInterval
		tcpListen, tcpPeers = s.tcpListen, s.tcpPeers
		tcpWhitelist, tcpSKFlag, tcpConfigPath = s.tcpWhitelist, s.tcpSKFlag, s.tcpConfigPath
	})
}

// TestRunSkychat_RejectsUnknownFlag — the visor passes skychat's args verbatim
// from skywire.json, so a typo there must fail loudly at startup instead of
// silently running with defaults on the wrong port/transport.
func TestRunSkychat_RejectsUnknownFlag(t *testing.T) {
	snapshotFlagGlobals(t)

	err := RunSkychat(context.Background(), []string{"--definitely-not-a-flag"})
	if err == nil {
		t.Fatal("an unknown flag should abort startup")
	}
	if !strings.Contains(err.Error(), "parse flags") {
		t.Errorf("err = %v, want it attributed to flag parsing", err)
	}
}

func TestRunSkychat_RejectsMalformedFlagValue(t *testing.T) {
	snapshotFlagGlobals(t)

	// --port is a uint16; a non-numeric value must not silently become 0.
	err := RunSkychat(context.Background(), []string{"--port=not-a-number"})
	if err == nil {
		t.Fatal("a malformed flag value should abort startup")
	}
	if !strings.Contains(err.Error(), "parse flags") {
		t.Errorf("err = %v, want it attributed to flag parsing", err)
	}
}
