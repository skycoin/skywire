// Package commands cmd/apps/skychat/commands/history_wiring_test.go
//
// Unit coverage for the persistence wiring that doesn't need a running visor:
// opening the bolt history store from CLI flags, loading a PK whitelist,
// persisting a message (nil-store no-op, happy path, and the rejected-write
// debug branch), the /history/peers handler, and the embedded static FS.
//
// These toggle package-level persistence globals (historyStore + the persist*
// flag vars), so every test snapshots and restores them; the bolt store lives
// under t.TempDir().
package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/cmd/apps/skychat/history"
	"github.com/skycoin/skywire/pkg/cipher"
)

// withChatLog installs a discard logrus logger as the package chatLog for the
// test (persistMessage / collectGroupHealth log rejected writes at debug level
// through it, and chatLog is nil until RunSkychat wires it). Restores on cleanup.
func withChatLog(t *testing.T) {
	t.Helper()
	prev := chatLog
	l := logrus.New()
	l.SetOutput(io.Discard)
	chatLog = l
	t.Cleanup(func() { chatLog = prev })
}

// restoreHistoryStore snapshots the package historyStore and, on cleanup, closes
// any store the test swapped in and restores the original.
func restoreHistoryStore(t *testing.T) {
	t.Helper()
	prev := historyStore
	t.Cleanup(func() {
		if historyStore != nil && historyStore != prev {
			_ = historyStore.Close() //nolint:errcheck
		}
		historyStore = prev
	})
}

func newTempStore(t *testing.T, limits history.Limits) history.Store {
	t.Helper()
	st, err := history.NewBoltStore(filepath.Join(t.TempDir(), "hist.db"), limits)
	if err != nil {
		t.Fatalf("NewBoltStore: %v", err)
	}
	return st
}

func TestLoadWhitelist(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	// Comment lines, blanks, and surrounding whitespace are all ignored.
	content := "# team whitelist\n" + pkA.Hex() + "\n\n  " + pkB.Hex() + "  \n# trailing comment\n"
	path := filepath.Join(t.TempDir(), "wl.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	wl, err := loadWhitelist(path)
	if err != nil {
		t.Fatalf("loadWhitelist: %v", err)
	}
	if len(wl) != 2 || !wl[pkA.Hex()] || !wl[pkB.Hex()] {
		t.Errorf("whitelist = %v, want exactly {%s, %s}", wl, pkA.Hex(), pkB.Hex())
	}
	if wl["# team whitelist"] {
		t.Error("comment lines must not be added to the whitelist")
	}

	// A missing file surfaces the read error.
	if _, err := loadWhitelist(filepath.Join(t.TempDir(), "nope.txt")); err == nil {
		t.Error("loadWhitelist should error on a missing file")
	}
}

func TestGetFileSystem(t *testing.T) {
	fsys := getFileSystem()
	if fsys == nil {
		t.Fatal("getFileSystem returned nil")
	}
	// The embedded UI is served from static/; index.html must resolve.
	f, err := fsys.Open("index.html")
	if err != nil {
		t.Fatalf("open embedded index.html: %v", err)
	}
	_ = f.Close() //nolint:errcheck
}

func TestPersistMessage(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	withChatLog(t)
	restoreHistoryStore(t)

	// A nil store is a no-op (persistence disabled) — must not panic.
	historyStore = nil
	persistMessage(history.Message{Peer: "peer", Text: "ignored"})

	// Happy path: the message lands in the store.
	pk, _ := cipher.GenerateKeyPair()
	st := newTempStore(t, history.Limits{})
	historyStore = st
	persistMessage(history.Message{Peer: pk.Hex(), Text: "hello", Timestamp: time.Now().UTC()})
	got, err := st.ListByPeer(pk.Hex(), 0)
	if err != nil || len(got) != 1 || got[0].Text != "hello" {
		t.Fatalf("after persist: got=%v err=%v", got, err)
	}

	// Rejected-write branch: an oversize message hits ErrTooLarge and is dropped
	// via the debug-log path (chatLog) without blocking or panicking.
	small := newTempStore(t, history.Limits{MaxMessageSize: 4})
	historyStore = small
	persistMessage(history.Message{Peer: pk.Hex(), Text: "far larger than the four-byte cap"})
	if rows, _ := small.ListByPeer(pk.Hex(), 0); len(rows) != 0 {
		t.Errorf("oversize message should be dropped, stored %d", len(rows))
	}
}

func TestOpenHistoryStore(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	restoreHistoryStore(t)
	withChatLog(t)
	// Snapshot the persist* flags openHistoryStore reads.
	prevDB, prevWL := persistDBPath, persistWhitelistFile
	t.Cleanup(func() { persistDBPath, persistWhitelistFile = prevDB, prevWL })

	// Open from an explicit db path (no whitelist).
	persistDBPath = filepath.Join(t.TempDir(), "open.db")
	persistWhitelistFile = ""
	if err := openHistoryStore(); err != nil {
		t.Fatalf("openHistoryStore: %v", err)
	}
	if historyStore == nil {
		t.Fatal("openHistoryStore left historyStore nil")
	}
	pk, _ := cipher.GenerateKeyPair()
	persistMessage(history.Message{Peer: pk.Hex(), Text: "persisted", Timestamp: time.Now().UTC()})
	if peers, err := historyStore.Peers(); err != nil || len(peers) != 1 {
		t.Errorf("Peers after open+persist = %v err=%v", peers, err)
	}
	_ = historyStore.Close() //nolint:errcheck
	historyStore = nil

	// Whitelist branch: only the whitelisted peer is persisted.
	wlPK, _ := cipher.GenerateKeyPair()
	wlPath := filepath.Join(t.TempDir(), "wl.txt")
	if err := os.WriteFile(wlPath, []byte(wlPK.Hex()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	persistDBPath = filepath.Join(t.TempDir(), "open-wl.db")
	persistWhitelistFile = wlPath
	if err := openHistoryStore(); err != nil {
		t.Fatalf("openHistoryStore (whitelist): %v", err)
	}
	other, _ := cipher.GenerateKeyPair()
	persistMessage(history.Message{Peer: other.Hex(), Text: "blocked", Timestamp: time.Now().UTC()})
	persistMessage(history.Message{Peer: wlPK.Hex(), Text: "allowed", Timestamp: time.Now().UTC()})
	peers, _ := historyStore.Peers()
	if len(peers) != 1 || peers[0] != wlPK.Hex() {
		t.Errorf("whitelist store peers = %v, want only %s", peers, wlPK.Hex())
	}
	_ = historyStore.Close() //nolint:errcheck
	historyStore = nil

	// A missing whitelist file makes openHistoryStore fail (store not set).
	persistDBPath = filepath.Join(t.TempDir(), "open-fail.db")
	persistWhitelistFile = filepath.Join(t.TempDir(), "absent-whitelist")
	if err := openHistoryStore(); err == nil {
		t.Error("openHistoryStore should fail when the whitelist file is missing")
	}
}

func TestHistoryPeersHandler(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	restoreHistoryStore(t)

	// Persistence disabled → 503.
	historyStore = nil
	rr := httptest.NewRecorder()
	historyPeersHandler(rr, httptest.NewRequest(http.MethodGet, "/history/peers", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil store: code=%d, want 503", rr.Code)
	}

	// With a store holding a peer → 200 + a JSON array containing it.
	st := newTempStore(t, history.Limits{})
	historyStore = st
	pk, _ := cipher.GenerateKeyPair()
	if err := st.Append(history.Message{Peer: pk.Hex(), Text: "hi", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	historyPeersHandler(rr, httptest.NewRequest(http.MethodGet, "/history/peers", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("with store: code=%d body=%q", rr.Code, rr.Body.String())
	}
	var peers []string
	if err := json.Unmarshal(rr.Body.Bytes(), &peers); err != nil {
		t.Fatalf("decode peers: %v (body=%q)", err, rr.Body.String())
	}
	found := false
	for _, p := range peers {
		if p == pk.Hex() {
			found = true
		}
	}
	if !found {
		t.Errorf("peers = %v, missing %s", peers, pk.Hex())
	}
}
