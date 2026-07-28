// Package commands cmd/apps/skychat/commands/filexfer_transfer_test.go
//
// Unit coverage for the file-transfer core that the existing filexfer tests
// left at 0%: the auto-accept policy (acceptInbound / isEstablishedPeer /
// isGroupMember), the completion hook (onXferDone), and the send path
// (sendFileID / sendFileToGroup).
//
// The centerpiece is TestSendFileID_EndToEnd, which runs a REAL transfer
// between two xfer.Managers over an in-process listener — the receiving side
// wired to the production acceptInbound + onXferDone hooks rather than to
// stubs. That exercises the policy decision, the on-disk save path, the SSE
// surfacing and the history persist in one pass, which is how the pieces
// actually fail: individually they all look fine.
//
// Downloads-dir note: downloadsDir() resolves to <skyenv.LocalPath>/
// skychat-downloads when appCl is nil (LocalPath is a const, so it can't be
// redirected at a temp dir). withCleanDownloads therefore snapshots the
// directory and removes whatever the test added, rather than isolating it.
package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/cmd/apps/skychat/group"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skychat/dm"
	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/skychat/xfer"
)

// --- harness ----------------------------------------------------------------

// withCleanDownloads returns the downloads dir and removes every entry the test
// creates in it on cleanup, leaving anything that was already there.
func withCleanDownloads(t *testing.T) string {
	t.Helper()
	dir, err := downloadsDir()
	if err != nil {
		t.Fatalf("downloadsDir: %v", err)
	}
	before := map[string]bool{}
	if entries, rerr := os.ReadDir(dir); rerr == nil {
		for _, e := range entries {
			before[e.Name()] = true
		}
	}
	t.Cleanup(func() {
		entries, rerr := os.ReadDir(dir)
		if rerr != nil {
			return
		}
		for _, e := range entries {
			if !before[e.Name()] {
				_ = os.Remove(filepath.Join(dir, e.Name())) //nolint:errcheck
			}
		}
	})
	return dir
}

// withXferEnv gives the test a private SSE hub, a discard chatLog, pairing and
// OS notifications off, and a clean file-request registry — restoring every
// global it touches.
func withXferEnv(t *testing.T) {
	t.Helper()
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	withChatLog(t)

	origHub, origPair, origNotify := hub, pairEnable, osNotify
	origMgr, origCtrl := fileMgr, chatCtrl
	origSess, origGroup := cxoGroupSess, cxoGroup

	hub = newSSEHub()
	pairEnable = false
	osNotify = false
	fileMgr = nil
	cxoGroupSess = nil
	cxoGroup = ""

	clearRequestedFiles()
	t.Cleanup(func() {
		hub, pairEnable, osNotify = origHub, origPair, origNotify
		fileMgrMu.Lock()
		fileMgr = origMgr
		fileMgrMu.Unlock()
		chatCtrl = origCtrl
		cxoGroupSess, cxoGroup = origSess, origGroup
		clearRequestedFiles()
	})
}

func clearRequestedFiles() {
	requestedFilesMu.Lock()
	requestedFiles = map[string]cipher.PubKey{}
	requestedFilesMu.Unlock()
}

// establishPeer wires chatCtrl to an in-memory transport and forces a cached
// conn to pk, so isEstablishedPeer(pk) reports true — the real precondition
// the auto-accept policy checks, not a stub of it.
func establishPeer(t *testing.T, pk cipher.PubKey) {
	t.Helper()
	cc := newCapturingClient() // from pairing_handlers_test.go
	ctrl := dm.New(dm.Config{Client: cc, Networks: []appnet.Type{appnet.TypeSkynet}})
	chatCtrl = ctrl
	if err := ctrl.SendRaw(context.Background(), pk, []byte("warm the conn")); err != nil {
		t.Fatalf("seeding a cached conn to %s: %v", pk.Hex(), err)
	}
	if !ctrl.HasConn(pk) {
		t.Fatal("expected a cached conn after SendRaw")
	}
	t.Cleanup(func() {
		_ = ctrl.Close() //nolint:errcheck
		cc.closeAll()
	})
}

// pkAddrConn overrides RemoteAddr so xfer's remotePK sees a real peer key —
// a bare net.Pipe reports no PK, which would make every accept decision run
// against the zero key.
type pkAddrConn struct {
	net.Conn
	addr appnet.Addr
}

func (c pkAddrConn) RemoteAddr() net.Addr { return c.addr }

// memXferListener is an in-process net.Listener: dialMem hands the caller one
// end of a pipe and queues the other for Accept, stamped with the sender's PK.
type memXferListener struct {
	ch        chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
	senderPK  cipher.PubKey
}

func newMemXferListener(senderPK cipher.PubKey) *memXferListener {
	return &memXferListener{
		ch:       make(chan net.Conn, 8),
		closed:   make(chan struct{}),
		senderPK: senderPK,
	}
}

func (l *memXferListener) dialMem() (net.Conn, error) {
	dialer, callee := net.Pipe()
	accepted := pkAddrConn{
		Conn: callee,
		addr: appnet.Addr{Net: appnet.TypeSkynet, PubKey: l.senderPK, Port: routing.Port(1)},
	}
	select {
	case l.ch <- accepted:
		return dialer, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memXferListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memXferListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *memXferListener) Addr() net.Addr { return appnet.Addr{Net: appnet.TypeSkynet} }

// newEmptyGroupSession returns a zero-value group.Session: PeerPKs() reads an
// empty peer set under its (zero-usable) mutex, which is all isGroupMember and
// sendFileToGroup need. A session with actual peers can't be built from here —
// peerSubs is unexported and only a real CXO attach populates it, so
// isGroupMember's member-found branch stays uncovered by design.
func newEmptyGroupSession() *group.Session { return &group.Session{} }

// waitForFile blocks until path exists with at least size bytes. The receiver
// writes on the xfer manager's goroutine, so the sender returning does not
// guarantee the file is flushed yet.
func waitForFile(t *testing.T, path string, size int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(path); err == nil && fi.Size() >= size {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	fi, err := os.Stat(path)
	t.Fatalf("timed out waiting for %s to reach %d bytes (stat: %v, %v)", path, size, fi, err)
}

// writeTempFile writes content to a fresh temp file and returns its path.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// collectEvents subscribes to the structured /events stream and returns a
// drain func that gathers whatever arrived within a short settle window.
func collectEvents(t *testing.T) func() []chatEvent {
	t.Helper()
	ch, unsub := hub.subscribeEvents(nil, 0)
	t.Cleanup(unsub)
	return func() []chatEvent {
		var out []chatEvent
		deadline := time.After(2 * time.Second)
		for {
			select {
			case ev := <-ch:
				out = append(out, ev)
			case <-time.After(200 * time.Millisecond):
				return out
			case <-deadline:
				return out
			}
		}
	}
}

// --- isEstablishedPeer / isGroupMember --------------------------------------

func TestIsEstablishedPeer(t *testing.T) {
	withXferEnv(t)
	pk, _ := cipher.GenerateKeyPair()

	chatCtrl = nil
	if isEstablishedPeer(pk) {
		t.Error("a nil controller must not report an established peer")
	}

	other, _ := cipher.GenerateKeyPair()
	establishPeer(t, pk)
	if !isEstablishedPeer(pk) {
		t.Error("a peer with a cached conn should be established")
	}
	if isEstablishedPeer(other) {
		t.Error("a peer with no cached conn must not be established")
	}
}

func TestIsGroupMember_FalseCases(t *testing.T) {
	withXferEnv(t)
	pk, _ := cipher.GenerateKeyPair()

	// No active group session at all.
	cxoGroupSess, cxoGroup = nil, "g1"
	if isGroupMember("g1", pk) {
		t.Error("no session → not a member")
	}

	// A session exists, but the offered group id doesn't match ours (or is
	// empty) — a file stamped with someone else's group must not be accepted.
	cxoGroupSess = newEmptyGroupSession()
	cxoGroup = "g1"
	if isGroupMember("", pk) {
		t.Error("an empty group id → not a member")
	}
	if isGroupMember("other-group", pk) {
		t.Error("a different group id → not a member")
	}
	// Right group, but the sender isn't in the peer set.
	if isGroupMember("g1", pk) {
		t.Error("a non-member peer → not a member")
	}
}

// --- acceptInbound ----------------------------------------------------------

func TestAcceptInbound_DeclinesUnestablishedPeer(t *testing.T) {
	withXferEnv(t)
	withCleanDownloads(t)
	from, _ := cipher.GenerateKeyPair()

	w, ok := acceptInbound(from, xfer.Offer{ID: "ai-decline-1", Name: "x.txt", Size: 3})
	if ok || w != nil {
		t.Errorf("acceptInbound from a peer we've never talked to = (%v, %v), want (nil, false)", w, ok)
	}
}

func TestAcceptInbound_AcceptsEstablishedPeer(t *testing.T) {
	withXferEnv(t)
	dir := withCleanDownloads(t)
	from, _ := cipher.GenerateKeyPair()
	establishPeer(t, from)

	offer := xfer.Offer{ID: "ai-est-1", Name: "notes.txt", Size: 5}
	w, ok := acceptInbound(from, offer)
	if !ok || w == nil {
		t.Fatalf("acceptInbound from an established peer = (%v, %v), want a handle and true", w, ok)
	}
	defer func() { _ = w.Close() }() //nolint:errcheck

	nf, isNamed := w.(*namedFile)
	if !isNamed {
		t.Fatalf("handle is %T, want *namedFile (onDone reads the path off it)", w)
	}
	want := filepath.Join(dir, safeFileName(offer.Name, offer.ID))
	if nf.path != want {
		t.Errorf("save path = %q, want %q", nf.path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("accepting should have created the destination file: %v", err)
	}
}

func TestAcceptInbound_AcceptsRequestedFileFromStranger(t *testing.T) {
	// A backfill re-send arrives from a peer we may have no live conn with —
	// we asked for it, so the policy lets it through.
	withXferEnv(t)
	withCleanDownloads(t)
	from, _ := cipher.GenerateKeyPair()

	offer := xfer.Offer{ID: "ai-req-1", Name: "backfilled.png", Size: 9}
	if w, ok := acceptInbound(from, offer); ok || w != nil {
		t.Fatal("precondition: an unrequested file from a stranger must be declined")
	}

	markFileRequested(offer.ID, from)
	w, ok := acceptInbound(from, offer)
	if !ok || w == nil {
		t.Fatalf("a requested file should be accepted, got (%v, %v)", w, ok)
	}
	_ = w.Close() //nolint:errcheck

	// The request is keyed to the peer we asked — a different peer answering
	// with the same id is still a stranger.
	impostor, _ := cipher.GenerateKeyPair()
	if _, ok := acceptInbound(impostor, offer); ok {
		t.Error("a requested id must not accept bytes from a different peer")
	}
}

func TestAcceptInbound_GroupOfferFromNonMember(t *testing.T) {
	withXferEnv(t)
	withCleanDownloads(t)
	from, _ := cipher.GenerateKeyPair()
	// Established as a DM peer, but the offer is stamped with a group — the
	// group branch decides, and we're not in it.
	establishPeer(t, from)
	cxoGroupSess = newEmptyGroupSession()
	cxoGroup = "g1"

	offer := xfer.Offer{ID: "ai-grp-1", Name: "shared.txt", Size: 4, Group: "g1"}
	if w, ok := acceptInbound(from, offer); ok || w != nil {
		t.Error("a group file from a non-member must be declined even if we DM that peer")
	}
}

// TestAcceptInbound_SanitizesHostileName is the path-traversal guard at the
// point where a peer-supplied name first reaches the filesystem.
func TestAcceptInbound_SanitizesHostileName(t *testing.T) {
	withXferEnv(t)
	dir := withCleanDownloads(t)
	from, _ := cipher.GenerateKeyPair()
	establishPeer(t, from)

	hostile := []string{
		"../../evil.sh",
		`..\..\evil.exe`,
		"/etc/passwd",
		"....//....//evil",
	}
	for _, name := range hostile {
		t.Run(name, func(t *testing.T) {
			offer := xfer.Offer{ID: "ai-evil-" + safeFileName(name, "x"), Name: name, Size: 1}
			w, ok := acceptInbound(from, offer)
			if !ok {
				t.Fatalf("expected the transfer to be accepted (then sanitized), got declined")
			}
			defer func() { _ = w.Close() }() //nolint:errcheck

			nf := w.(*namedFile) //nolint:errcheck,forcetypeassert
			if filepath.Dir(nf.path) != filepath.Clean(dir) {
				t.Errorf("save path %q escaped the downloads dir %q", nf.path, dir)
			}
			if strings.Contains(nf.path, "..") {
				t.Errorf("save path %q still contains a traversal segment", nf.path)
			}
		})
	}
}

// TestAcceptInbound_DeclinesWhenDestinationUnwritable covers the save-path
// failure arm: the policy said yes but the file can't be created (here: a
// directory already occupies the name; in the field, a full or read-only
// disk). The transfer must be declined rather than accepted with a nil writer,
// which the xfer receiver would deref.
func TestAcceptInbound_DeclinesWhenDestinationUnwritable(t *testing.T) {
	withXferEnv(t)
	dir := withCleanDownloads(t)
	from, _ := cipher.GenerateKeyPair()
	establishPeer(t, from)

	offer := xfer.Offer{ID: "ai-nowrite-1", Name: "blocked.txt", Size: 4}
	blocker := filepath.Join(dir, safeFileName(offer.Name, offer.ID))
	if err := os.Mkdir(blocker, 0o750); err != nil {
		t.Fatalf("staging a directory at the destination: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(blocker) }) //nolint:errcheck

	w, ok := acceptInbound(from, offer)
	if ok || w != nil {
		t.Errorf("acceptInbound with an uncreatable destination = (%v, %v), want (nil, false)", w, ok)
	}
}

// --- onXferDone -------------------------------------------------------------

func TestOnXferDone_IncomingSuccess(t *testing.T) {
	withXferEnv(t)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.Limits{})
	dir := withCleanDownloads(t)
	drain := collectEvents(t)

	peer, _ := cipher.GenerateKeyPair()
	offer := xfer.Offer{ID: "oxd-in-1", Name: "photo.png", Size: 2048}
	onXferDone(xfer.Incoming, peer, offer, nil)

	evs := drain()
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(evs), evs)
	}
	ev := evs[0]
	if ev.Dir != "in" || ev.From != peer.Hex() {
		t.Errorf("dir/from = %q/%q, want in/%s", ev.Dir, ev.From, peer.Hex())
	}
	if ev.Channel != channelDM || ev.FileStatus != "received" {
		t.Errorf("channel/status = %q/%q, want %s/received", ev.Channel, ev.FileStatus, channelDM)
	}
	if ev.FileID != offer.ID || ev.FileName != offer.Name || ev.FileSize != offer.Size {
		t.Errorf("file fields = %s/%s/%d, want %s/%s/%d",
			ev.FileID, ev.FileName, ev.FileSize, offer.ID, offer.Name, offer.Size)
	}
	if want := filepath.Join(dir, safeFileName(offer.Name, offer.ID)); ev.FilePath != want {
		t.Errorf("file path = %q, want %q", ev.FilePath, want)
	}
	if !strings.Contains(ev.Text, "2.0 KB") {
		t.Errorf("text %q should carry the human size", ev.Text)
	}

	// Persisted so the file re-renders from /history on a fresh browser.
	msgs, err := historyStore.ListByPeer(peer.Hex(), 10)
	if err != nil {
		t.Fatalf("history list: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("history rows = %d, want 1", len(msgs))
	}
	if msgs[0].FileID != offer.ID || msgs[0].FileStatus != "received" || msgs[0].Outgoing {
		t.Errorf("persisted row = %+v, want an incoming received row for %s", msgs[0], offer.ID)
	}
	if want := "/files/" + safeFileName(offer.Name, offer.ID); msgs[0].FileURL != want {
		t.Errorf("persisted FileURL = %q, want %q", msgs[0].FileURL, want)
	}
}

func TestOnXferDone_IncomingFailure(t *testing.T) {
	withXferEnv(t)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.Limits{})
	withCleanDownloads(t)
	drain := collectEvents(t)

	peer, _ := cipher.GenerateKeyPair()
	offer := xfer.Offer{ID: "oxd-inerr-1", Name: "broken.bin", Size: 10}
	onXferDone(xfer.Incoming, peer, offer, context.DeadlineExceeded)

	evs := drain()
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	if evs[0].FileStatus != "failed" {
		t.Errorf("status = %q, want failed", evs[0].FileStatus)
	}
	if !strings.Contains(evs[0].Text, "receive failed") {
		t.Errorf("text %q should say the receive failed", evs[0].Text)
	}
	// A failed receive has no saved file, so no path is advertised.
	if evs[0].FilePath != "" {
		t.Errorf("failed receive advertised a path %q", evs[0].FilePath)
	}
}

func TestOnXferDone_OutgoingSuccessPointsAtSenderCopy(t *testing.T) {
	withXferEnv(t)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.Limits{})
	dir := withCleanDownloads(t)
	drain := collectEvents(t)

	peer, _ := cipher.GenerateKeyPair()
	offer := xfer.Offer{ID: "oxd-out-1", Name: "clip.mp4", Size: 1024}
	onXferDone(xfer.Outgoing, peer, offer, nil)

	evs := drain()
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	ev := evs[0]
	if ev.Dir != "out" || ev.To != peer.Hex() || ev.From != "" {
		t.Errorf("dir/to/from = %q/%q/%q, want out/%s/empty", ev.Dir, ev.To, ev.From, peer.Hex())
	}
	if ev.FileStatus != "sent" {
		t.Errorf("status = %q, want sent", ev.FileStatus)
	}
	// The sender's own bubble renders from the kept copy, which is id-named.
	if want := filepath.Join(dir, sentCopyName(offer)); ev.FilePath != want {
		t.Errorf("file path = %q, want the sender copy %q", ev.FilePath, want)
	}

	msgs, err := historyStore.ListByPeer(peer.Hex(), 10)
	if err != nil {
		t.Fatalf("history list: %v", err)
	}
	if len(msgs) != 1 || !msgs[0].Outgoing {
		t.Fatalf("history rows = %+v, want one outgoing row", msgs)
	}
}

func TestOnXferDone_OutgoingFailure(t *testing.T) {
	withXferEnv(t)
	withCleanDownloads(t)
	drain := collectEvents(t)

	peer, _ := cipher.GenerateKeyPair()
	onXferDone(xfer.Outgoing, peer, xfer.Offer{ID: "oxd-outerr-1", Name: "big.iso", Size: 1}, context.Canceled)

	evs := drain()
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	if evs[0].FileStatus != "failed" || !strings.Contains(evs[0].Text, "send failed") {
		t.Errorf("event = %q/%q, want a failed send", evs[0].FileStatus, evs[0].Text)
	}
	if evs[0].FilePath != "" {
		t.Errorf("failed send advertised a path %q", evs[0].FilePath)
	}
}

// TestOnXferDone_GroupFileRoutesToGroupChannel — a group file must land on the
// group channel with its id, and must NOT be persisted into the DM history
// bucket (group messages live in the group bucket).
func TestOnXferDone_GroupFileRoutesToGroupChannel(t *testing.T) {
	withXferEnv(t)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.Limits{})
	withCleanDownloads(t)
	drain := collectEvents(t)

	peer, _ := cipher.GenerateKeyPair()
	offer := xfer.Offer{ID: "oxd-grp-1", Name: "deck.pdf", Size: 512, Group: "group-abc"}
	onXferDone(xfer.Incoming, peer, offer, nil)

	evs := drain()
	if len(evs) != 1 {
		t.Fatalf("got %d events, want 1", len(evs))
	}
	if evs[0].Channel != channelGroup || evs[0].GroupID != "group-abc" {
		t.Errorf("channel/group = %q/%q, want %s/group-abc", evs[0].Channel, evs[0].GroupID, channelGroup)
	}

	msgs, err := historyStore.ListByPeer(peer.Hex(), 10)
	if err != nil {
		t.Fatalf("history list: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("group file persisted %d DM history rows, want 0", len(msgs))
	}
}

// TestOnXferDone_RequestedFileEmitsFileReady — a backfill response patches the
// message that already references the file instead of creating a new one, so
// it broadcasts a file-ready control event and skips both the normal event and
// the history write.
func TestOnXferDone_RequestedFileEmitsFileReady(t *testing.T) {
	withXferEnv(t)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.Limits{})
	dir := withCleanDownloads(t)

	raw, unsub := hub.subscribe()
	defer unsub()
	drain := collectEvents(t)

	peer, _ := cipher.GenerateKeyPair()
	offer := xfer.Offer{ID: "oxd-ready-1", Name: "late.png", Size: 32}
	markFileRequested(offer.ID, peer)

	onXferDone(xfer.Incoming, peer, offer, nil)

	got := waitForString(t, raw, 2*time.Second)
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("decode file-ready: %v (raw %q)", err, got)
	}
	if m["channel"] != "file-ready" || m["file_id"] != offer.ID {
		t.Errorf("broadcast = %v, want a file-ready for %s", m, offer.ID)
	}
	if want := "/files/" + safeFileName(offer.Name, offer.ID); m["file_url"] != want {
		t.Errorf("file_url = %v, want %q", m["file_url"], want)
	}
	_ = dir

	// The fulfilled request is dropped so a later unsolicited send is declined.
	if isRequestedFile(offer.ID, peer) {
		t.Error("a completed backfill should clear its pending request")
	}
	// No normal file event, and nothing persisted.
	if evs := drain(); len(evs) != 0 {
		t.Errorf("backfill emitted %d normal events, want 0: %+v", len(evs), evs)
	}
	msgs, err := historyStore.ListByPeer(peer.Hex(), 10)
	if err != nil {
		t.Fatalf("history list: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("backfill persisted %d rows, want 0 (it patches an existing message)", len(msgs))
	}
}

// --- sendFileID -------------------------------------------------------------

func TestSendFileID_Errors(t *testing.T) {
	withXferEnv(t)
	withCleanDownloads(t)
	peer, _ := cipher.GenerateKeyPair()
	ctx := context.Background()

	// No manager (file transfer disabled / standalone).
	fileMgr = nil
	if _, err := sendFileID(ctx, peer, "", writeTempFile(t, "a.txt", "x"), "id1", "a.txt", false); err == nil ||
		!strings.Contains(err.Error(), "not enabled") {
		t.Errorf("nil manager err = %v, want a 'not enabled' error", err)
	}

	// With a manager installed, the source checks run before any dial.
	fileMgr = xfer.NewManager(xfer.Config{Dial: func(context.Context, cipher.PubKey, uint16) (net.Conn, error) {
		t.Error("dial must not be attempted for an unreadable source")
		return nil, net.ErrClosed
	}})

	missing := filepath.Join(t.TempDir(), "nope.txt")
	if _, err := sendFileID(ctx, peer, "", missing, "id2", "nope.txt", false); err == nil ||
		!strings.Contains(err.Error(), "open") {
		t.Errorf("missing source err = %v, want an open error", err)
	}

	if _, err := sendFileID(ctx, peer, "", t.TempDir(), "id3", "", false); err == nil ||
		!strings.Contains(err.Error(), "is a directory") {
		t.Errorf("directory source err = %v, want a directory error", err)
	}
}

// TestSendFileID_EndToEnd runs a real transfer between two managers over an
// in-process listener, with the receiving side wired to the production
// acceptInbound + onXferDone. It covers the send path, the accept policy, the
// on-disk save, the SSE surfacing and the history write together.
func TestSendFileID_EndToEnd(t *testing.T) {
	withXferEnv(t)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.Limits{})
	dir := withCleanDownloads(t)
	drain := collectEvents(t)

	senderPK, _ := cipher.GenerateKeyPair()
	recvPK, _ := cipher.GenerateKeyPair()

	// The receiver auto-accepts because a chat with the sender is established —
	// the real policy, not a stubbed Accept.
	establishPeer(t, senderPK)

	lis := newMemXferListener(senderPK)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)

	// The receiver runs on its own goroutines and touches the package globals
	// (hub, historyStore) that withXferEnv restores on cleanup. Join both
	// before returning, or the restore races the in-flight receive. Deferred
	// here so it runs ahead of every t.Cleanup.
	serveDone := make(chan struct{})
	recvDone := make(chan struct{})
	var recvOnce sync.Once
	defer func() {
		cancel()
		_ = lis.Close() //nolint:errcheck
		<-serveDone
	}()

	recvMgr := xfer.NewManager(xfer.Config{
		LocalPK: recvPK,
		Port:    1,
		Accept:  acceptInbound,
		OnDone: func(d xfer.Direction, p cipher.PubKey, o xfer.Offer, e error) {
			onXferDone(d, p, o, e)
			recvOnce.Do(func() { close(recvDone) })
		},
	})
	go func() {
		defer close(serveDone)
		recvMgr.Serve(ctx, lis)
	}()

	fileMgrMu.Lock()
	fileMgr = xfer.NewManager(xfer.Config{
		LocalPK: senderPK,
		Port:    1,
		Dial:    func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dialMem() },
		OnDone:  onXferDone,
	})
	fileMgrMu.Unlock()

	payload := strings.Repeat("skychat-payload-", 512) // 8 KiB
	src := writeTempFile(t, "report.txt", payload)

	const id = "e2e-xfer-1"
	gotID, err := sendFileID(ctx, recvPK, "", src, id, "report.txt", true)
	if err != nil {
		t.Fatalf("sendFileID: %v", err)
	}
	if gotID != id {
		t.Errorf("returned id = %q, want %q", gotID, id)
	}

	// SendFile returns once the receipt is read; the receiver's own OnDone
	// (history persist included) can still be running.
	select {
	case <-recvDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the receiver's OnDone never fired")
	}

	// The receiver saved the bytes under the sanitized name.
	saved := filepath.Join(dir, safeFileName("report.txt", id))
	waitForFile(t, saved, int64(len(payload)))
	b, rerr := os.ReadFile(saved) //nolint:gosec
	if rerr != nil {
		t.Fatalf("read saved file: %v", rerr)
	}
	if !bytes.Equal(b, []byte(payload)) {
		t.Errorf("saved content differs (%d bytes vs %d sent)", len(b), len(payload))
	}

	// The sender kept its own served copy so its bubble survives a reload.
	sentCopy := filepath.Join(dir, sentCopyName(xfer.Offer{ID: id, Name: "report.txt"}))
	if _, serr := os.Stat(sentCopy); serr != nil {
		t.Errorf("sender copy %q missing: %v", sentCopy, serr)
	}

	// Both legs surfaced: an "out" event from the sender manager and an "in"
	// event from the receiver's.
	evs := drain()
	var sawIn, sawOut bool
	for _, ev := range evs {
		if ev.FileID != id {
			continue
		}
		switch ev.Dir {
		case "in":
			sawIn = true
			if ev.FileStatus != "received" {
				t.Errorf("inbound status = %q, want received", ev.FileStatus)
			}
		case "out":
			sawOut = true
			if ev.FileStatus != "sent" {
				t.Errorf("outbound status = %q, want sent", ev.FileStatus)
			}
		}
	}
	if !sawIn || !sawOut {
		t.Errorf("events in=%v out=%v, want both (%d total: %+v)", sawIn, sawOut, len(evs), evs)
	}
}

// TestSendFileID_DeclinedByPeer covers the receiver-verdict branch: an
// unestablished peer declines, and the sender must report that rather than
// claiming success.
func TestSendFileID_DeclinedByPeer(t *testing.T) {
	withXferEnv(t)
	withCleanDownloads(t)

	senderPK, _ := cipher.GenerateKeyPair()
	recvPK, _ := cipher.GenerateKeyPair()
	// NOTE: no establishPeer → acceptInbound declines.
	chatCtrl = nil

	lis := newMemXferListener(senderPK)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)

	// acceptInbound reads chatCtrl, which withXferEnv restores on cleanup —
	// join the receiver before returning. A DECLINED transfer never calls
	// OnDone, so the Accept hook itself is what signals.
	serveDone := make(chan struct{})
	consulted := make(chan struct{})
	var acceptOnce sync.Once
	defer func() {
		cancel()
		_ = lis.Close() //nolint:errcheck
		<-serveDone
	}()

	recvMgr := xfer.NewManager(xfer.Config{
		LocalPK: recvPK,
		Port:    1,
		Accept: func(pk cipher.PubKey, o xfer.Offer) (io.WriteCloser, bool) {
			defer acceptOnce.Do(func() { close(consulted) })
			return acceptInbound(pk, o)
		},
	})
	go func() {
		defer close(serveDone)
		recvMgr.Serve(ctx, lis)
	}()

	fileMgrMu.Lock()
	fileMgr = xfer.NewManager(xfer.Config{
		LocalPK: senderPK,
		Port:    1,
		Dial:    func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return lis.dialMem() },
	})
	fileMgrMu.Unlock()

	src := writeTempFile(t, "rejected.txt", "nope")
	_, err := sendFileID(ctx, recvPK, "", src, "e2e-decline-1", "rejected.txt", false)
	if err == nil {
		t.Fatal("sendFileID to a declining peer should return an error")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("err = %v, want it to mention the peer rejected the transfer", err)
	}
	select {
	case <-consulted:
	case <-time.After(10 * time.Second):
		t.Fatal("the accept policy was never consulted")
	}
}

func TestSendFileID_SenderCopyRules(t *testing.T) {
	withXferEnv(t)
	dir := withCleanDownloads(t)
	peer, _ := cipher.GenerateKeyPair()
	ctx := context.Background()

	// A dial that always fails: the copy decision happens BEFORE SendFile, so
	// the send failing doesn't affect what this asserts.
	fileMgrMu.Lock()
	fileMgr = xfer.NewManager(xfer.Config{
		Dial: func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return nil, net.ErrClosed },
	})
	fileMgrMu.Unlock()

	cases := []struct {
		name     string
		id       string
		group    string
		keepCopy bool
		want     bool
	}{
		{"dm send keeps a copy", "copy-dm-1", "", true, true},
		{"backfill re-send keeps none", "copy-bf-1", "", false, false},
		{"group send keeps none", "copy-grp-1", "g1", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := writeTempFile(t, "doc.txt", "content")
			_, _ = sendFileID(ctx, peer, c.group, src, c.id, "doc.txt", c.keepCopy) //nolint:errcheck

			copyPath := filepath.Join(dir, sentCopyName(xfer.Offer{ID: c.id, Name: "doc.txt"}))
			_, err := os.Stat(copyPath)
			if got := err == nil; got != c.want {
				t.Errorf("sender copy at %q exists = %v, want %v", copyPath, got, c.want)
			}
		})
	}
}

// TestSendFileID_EmptyNameFallsBackToBase pins the display-name default: an
// empty name must not produce a nameless offer.
func TestSendFileID_EmptyNameFallsBackToBase(t *testing.T) {
	withXferEnv(t)
	dir := withCleanDownloads(t)
	peer, _ := cipher.GenerateKeyPair()

	fileMgrMu.Lock()
	fileMgr = xfer.NewManager(xfer.Config{
		Dial: func(context.Context, cipher.PubKey, uint16) (net.Conn, error) { return nil, net.ErrClosed },
	})
	fileMgrMu.Unlock()

	src := writeTempFile(t, "fallback.dat", "x")
	_, _ = sendFileID(context.Background(), peer, "", src, "name-fb-1", "", true) //nolint:errcheck

	// The kept copy's extension comes from the offer name, which defaulted to
	// the source's base name.
	want := filepath.Join(dir, "name-fb-1.dat")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected the sender copy at %q (name defaulted to the base name): %v", want, err)
	}
}

// TestSendFileID_SenderCopyFailureIsNonFatal pins the best-effort contract on
// the kept copy: if it can't be written the send still goes ahead (the copy
// only backs the sender's own thumbnail). Here a directory occupies the copy's
// name; in the field it would be a full disk.
func TestSendFileID_SenderCopyFailureIsNonFatal(t *testing.T) {
	withXferEnv(t)
	dir := withCleanDownloads(t)
	peer, _ := cipher.GenerateKeyPair()

	const id = "copy-fail-1"
	blocker := filepath.Join(dir, sentCopyName(xfer.Offer{ID: id, Name: "doc.txt"}))
	if err := os.Mkdir(blocker, 0o750); err != nil {
		t.Fatalf("staging a directory at the copy path: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(blocker) }) //nolint:errcheck

	dialed := false
	fileMgrMu.Lock()
	fileMgr = xfer.NewManager(xfer.Config{
		Dial: func(context.Context, cipher.PubKey, uint16) (net.Conn, error) {
			dialed = true
			return nil, net.ErrClosed
		},
	})
	fileMgrMu.Unlock()

	src := writeTempFile(t, "doc.txt", "content")
	if _, err := sendFileID(context.Background(), peer, "", src, id, "doc.txt", true); err == nil {
		t.Fatal("expected the (failing) dial to surface an error")
	}
	if !dialed {
		t.Error("a failed sender copy must not abort the send before it dials")
	}
}

// --- sendFileToGroup --------------------------------------------------------

func TestSendFileToGroup_Errors(t *testing.T) {
	withXferEnv(t)
	src := writeTempFile(t, "g.txt", "x")

	cxoGroupSess, cxoGroup = nil, ""
	if n, err := sendFileToGroup(context.Background(), src); err == nil || n != 0 {
		t.Errorf("no session: (%d, %v), want (0, error)", n, err)
	}

	// A session with an id but no online members has nobody to send to.
	cxoGroupSess = newEmptyGroupSession()
	cxoGroup = "g1"
	n, err := sendFileToGroup(context.Background(), src)
	if err == nil || n != 0 {
		t.Errorf("no members: (%d, %v), want (0, error)", n, err)
	}
	if err != nil && !strings.Contains(err.Error(), "no other members online") {
		t.Errorf("err = %v, want it to name the empty-group case", err)
	}
}
