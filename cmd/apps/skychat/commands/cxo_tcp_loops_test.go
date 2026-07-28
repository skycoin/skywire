// Package commands cmd/apps/skychat/commands/cxo_tcp_loops_test.go
//
// Coverage for the CXO-over-native-TCP loops: startCXOTCP, startCXOGroup,
// cxoDialUntilConnected, publishCXO and surfaceCXOInbound.
//
// Like tcp-direct, none of this needs dmsg — treestore has a native TCP
// transport and an in-memory DB, so a test can stand up a real peer publisher
// on 127.0.0.1, point --cxo-peer at it, and watch a genuine leaf propagate
// through the production subscriber into the SSE hub.
//
// Two behaviors are pinned beyond mere reachability:
//
//   - startCXOTCP must SKIP a malformed --cxo-peer and keep going. Aborting
//     the whole transport over one bad operator flag would take the working
//     peers down with it.
//   - cxoDialUntilConnected exists because Subscriber.ConnectTCP only arms its
//     reconnect watchdog after a FIRST success — a peer that isn't listening
//     yet at startup would otherwise never be retried. The test asserts it
//     keeps retrying and that the retry sleep is cancel-interruptible.
package commands

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/skychat/dm"
)

// --- harness ----------------------------------------------------------------

// withCXOEnv resets every cxo global, gives the test a private hub, and tears
// down whatever the run brought up before restoring.
func withCXOEnv(t *testing.T) {
	t.Helper()
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	withChatLog(t)

	origHub, origCtrl := hub, chatCtrl
	origEnable, origListen, origPeers := cxoEnable, cxoListen, cxoPeers
	origGroup, origOwner := cxoGroup, cxoGroupOwner
	origSK, origCfg := tcpSKFlag, tcpConfigPath

	hub = newSSEHub()
	chatCtrl = dm.New(dm.Config{})
	cxoEnable, cxoListen, cxoPeers = false, "", nil
	cxoGroup, cxoGroupOwner = "", ""
	tcpSKFlag, tcpConfigPath = "", ""
	t.Setenv("DMSGCURL_SK", "") // resolveTCPIdentity reads it

	cxoMu.Lock()
	cxoPub, cxoSubs, cxoGroupSess, cxoSeq = nil, nil, nil, 0
	cxoMu.Unlock()

	t.Cleanup(func() {
		cxoMu.Lock()
		pub, subs, sess := cxoPub, cxoSubs, cxoGroupSess
		cxoPub, cxoSubs, cxoGroupSess, cxoSeq = nil, nil, nil, 0
		cxoMu.Unlock()
		for _, s := range subs {
			_ = s.Close() //nolint:errcheck
		}
		if pub != nil {
			_ = pub.Close() //nolint:errcheck
		}
		if sess != nil {
			_ = sess.Close() //nolint:errcheck
		}
		hub, chatCtrl = origHub, origCtrl
		cxoEnable, cxoListen, cxoPeers = origEnable, origListen, origPeers
		cxoGroup, cxoGroupOwner = origGroup, origOwner
		tcpSKFlag, tcpConfigPath = origSK, origCfg
	})
}

// peerPublisher stands up a real CXO publisher on loopback — the "other visor"
// our subscriber follows.
func peerPublisher(t *testing.T) (feedPK cipher.PubKey, addr string, pub *treestore.Publisher) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	addr = freeTCPAddr(t) // from tcp_direct_loops_test.go
	p, err := treestore.NewWithTCP(addr, sk, treestore.PubConfig{
		InMemoryDB:  true,
		BatchWindow: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("peer publisher on %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck
	return pk, addr, p
}

// --- surfaceCXOInbound ------------------------------------------------------

func TestSurfaceCXOInbound(t *testing.T) {
	withCXOEnv(t)
	cxoGroup = "grp-42"

	sub, unsub := hub.subscribeEvents(nil, 0)
	defer unsub()

	sender, _ := cipher.GenerateKeyPair()
	surfaceCXOInbound(sender.Hex(), "from the feed")

	select {
	case ev := <-sub:
		if ev.Channel != channelGroup || ev.Transport != "cxo" || ev.Dir != "in" {
			t.Errorf("event = channel %q transport %q dir %q; want group/cxo/in",
				ev.Channel, ev.Transport, ev.Dir)
		}
		if ev.From != sender.Hex() || ev.Text != "from the feed" || ev.GroupID != "grp-42" {
			t.Errorf("event = %+v", ev)
		}
		if ev.ID == "" {
			t.Error("a surfaced CXO message needs an id so the UI can address it")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("surfaceCXOInbound published nothing on the hub")
	}
}

// --- publishCXO -------------------------------------------------------------

func TestPublishCXO_NoPublisherIsAnError(t *testing.T) {
	withCXOEnv(t)
	if _, err := publishCXO("nowhere to go"); err == nil {
		t.Error("publishing with no CXO publisher should error, not silently drop")
	}
}

func TestPublishCXO_WritesUniqueOrderedPaths(t *testing.T) {
	withCXOEnv(t)
	_, sk := cipher.GenerateKeyPair()
	pub, err := treestore.NewWithTCP(freeTCPAddr(t), sk, treestore.PubConfig{
		InMemoryDB: true, BatchWindow: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("publisher: %v", err)
	}
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	cxoMu.Lock()
	cxoPub = pub
	cxoMu.Unlock()

	shape := regexp.MustCompile(`^msgs/\d{19}-\d{6}$`)
	var paths []string
	for i := 0; i < 3; i++ {
		p, perr := publishCXO("body " + string(rune('a'+i)))
		if perr != nil {
			t.Fatalf("publishCXO: %v", perr)
		}
		if !shape.MatchString(p) {
			t.Errorf("path %q does not match msgs/<ts-nano>-<seq>", p)
		}
		paths = append(paths, p)
	}

	// Unique and lexically ordered — two sends in the same nanosecond must
	// still sort deterministically, which is what the seq suffix is for.
	for i := 1; i < len(paths); i++ {
		if paths[i] <= paths[i-1] {
			t.Errorf("paths not strictly increasing: %q then %q", paths[i-1], paths[i])
		}
	}
	if body, ok := pub.Get(paths[0]); !ok || string(body) != "body a" {
		t.Errorf("leaf at %q = %q ok=%v, want the published body", paths[0], body, ok)
	}
}

// --- cxoDialUntilConnected --------------------------------------------------

func TestCXODialUntilConnected_ReturnsOnFirstSuccess(t *testing.T) {
	withCXOEnv(t)
	feedPK, addr, _ := peerPublisher(t)

	sub, err := treestore.NewSubscriberTCP("", feedPK, treestore.SubConfig{InMemoryDB: true})
	if err != nil {
		t.Fatalf("subscriber: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck

	done := make(chan struct{})
	go func() {
		defer close(done)
		cxoDialUntilConnected(context.Background(), sub, addr, feedPK.Hex())
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("cxoDialUntilConnected never returned against a live peer")
	}
}

// TestCXODialUntilConnected_RetriesAndIsCancelable — the retry loop is the
// whole reason this helper exists (ConnectTCP arms its watchdog only after a
// first success), so it must keep trying against a dead peer AND unwind
// promptly on cancel rather than sleeping out the backoff.
func TestCXODialUntilConnected_RetriesAndIsCancelable(t *testing.T) {
	withCXOEnv(t)
	feedPK, _ := cipher.GenerateKeyPair()
	dead := freeTCPAddr(t) // reserved then released — nothing is listening

	sub, err := treestore.NewSubscriberTCP("", feedPK, treestore.SubConfig{InMemoryDB: true})
	if err != nil {
		t.Fatalf("subscriber: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		cxoDialUntilConnected(ctx, sub, dead, feedPK.Hex())
	}()

	// Still retrying, not returned.
	select {
	case <-done:
		t.Fatal("cxoDialUntilConnected returned against a dead peer; it must keep retrying")
	case <-time.After(300 * time.Millisecond):
	}

	cancel()
	start := time.Now()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cxoDialUntilConnected did not unwind after cancel")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("unwind took %v — cancel should interrupt the backoff sleep", elapsed)
	}
}

// --- startCXOTCP ------------------------------------------------------------

func TestStartCXOTCP_DisabledIsNoOp(t *testing.T) {
	withCXOEnv(t)
	cxoEnable, cxoGroup = false, ""

	if err := startCXOTCP(context.Background()); err != nil {
		t.Errorf("with --cxo unset startCXOTCP should be a no-op, got %v", err)
	}
	cxoMu.Lock()
	defer cxoMu.Unlock()
	if cxoPub != nil {
		t.Error("a disabled CXO transport must not open a publisher")
	}
}

func TestStartCXOTCP_RequiresIdentity(t *testing.T) {
	withCXOEnv(t)
	cxoEnable = true
	cxoListen = freeTCPAddr(t)

	if err := startCXOTCP(context.Background()); err == nil {
		t.Error("enabling --cxo with no configured identity should error")
	}
}

// TestStartCXOTCP_SubscribesAndSurfacesPeerMessages is the end-to-end case: a
// real peer publisher, the production subscriber wiring, and a genuine leaf
// arriving on the SSE hub.
func TestStartCXOTCP_SubscribesAndSurfacesPeerMessages(t *testing.T) {
	withCXOEnv(t)
	_, mySK := cipher.GenerateKeyPair()
	feedPK, peerAddr, peerPub := peerPublisher(t)

	cxoEnable = true
	cxoListen = freeTCPAddr(t)
	tcpSKFlag = mySK.Hex()
	cxoPeers = []string{
		"tcp://not-a-pk@127.0.0.1:1",             // malformed: must be skipped
		"tcp://" + feedPK.Hex() + "@" + peerAddr, // the real one
	}

	events := make(chan chatEvent, 8)
	sub, unsub := hub.subscribeEvents(nil, 0)
	defer unsub()
	go func() {
		for ev := range sub {
			select {
			case events <- ev:
			default:
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := startCXOTCP(ctx); err != nil {
		t.Fatalf("startCXOTCP: %v", err)
	}

	// One bad spec must not take the transport down with it.
	cxoMu.Lock()
	pubUp, nSubs := cxoPub != nil, len(cxoSubs)
	cxoMu.Unlock()
	if !pubUp {
		t.Fatal("our own feed publisher should be up")
	}
	if nSubs != 1 {
		t.Fatalf("subscribers = %d, want 1 (the malformed --cxo-peer is skipped)", nSubs)
	}

	// Give the subscriber time to attach, then publish from the peer.
	deadline := time.Now().Add(30 * time.Second)
	var got chatEvent
	for time.Now().Before(deadline) {
		if err := peerPub.Put("msgs/0000000000000000001-000001", []byte("hello from the peer")); err != nil {
			t.Fatalf("peer Put: %v", err)
		}
		select {
		case ev := <-events:
			got = ev
		case <-time.After(500 * time.Millisecond):
			continue
		}
		break
	}
	if got.Text != "hello from the peer" {
		t.Fatalf("surfaced event = %+v, want the peer's message", got)
	}
	if got.From != feedPK.Hex() {
		t.Errorf("event from = %s, want the peer feed %s", got.From, feedPK.Hex()[:8])
	}
	if got.Transport != "cxo" {
		t.Errorf("event transport = %q, want cxo", got.Transport)
	}
}

// --- startCXOGroup ----------------------------------------------------------

func TestStartCXOGroup_RejectsBadOwner(t *testing.T) {
	withCXOEnv(t)
	_, sk := cipher.GenerateKeyPair()
	cxoGroup = uuid.NewString()
	cxoGroupOwner = "not-a-pk"
	tcpSKFlag = sk.Hex()
	cxoListen = freeTCPAddr(t)

	err := startCXOTCP(context.Background()) // routes to startCXOGroup when cxoGroup is set
	if err == nil {
		t.Fatal("an invalid --cxo-group-owner should error")
	}
	if !strings.Contains(err.Error(), "cxo-group") {
		t.Errorf("err = %v, want it attributed to cxo-group", err)
	}
}

func TestStartCXOGroup_RequiresIdentity(t *testing.T) {
	withCXOEnv(t)
	owner, _ := cipher.GenerateKeyPair()
	cxoGroup = uuid.NewString()
	cxoGroupOwner = owner.Hex()
	cxoListen = freeTCPAddr(t)
	tcpSKFlag, tcpConfigPath = "", ""

	if err := startCXOTCP(context.Background()); err == nil {
		t.Error("cxo-group with no configured identity should error")
	}
}

// TestStartCXOGroup_OpensSessionAndRoutesSends — with --cxo-group set,
// startCXOTCP hands off to the group subsystem, and publishCXO must route
// through the session rather than the flat per-feed publisher.
func TestStartCXOGroup_OpensSessionAndRoutesSends(t *testing.T) {
	withCXOEnv(t)
	myPK, mySK := cipher.GenerateKeyPair()
	peerPK, _ := cipher.GenerateKeyPair()

	cxoGroup = uuid.NewString()
	cxoGroupOwner = myPK.Hex() // we are the owner → RoleOwner
	tcpSKFlag = mySK.Hex()
	cxoListen = freeTCPAddr(t)
	cxoPeers = []string{
		"tcp://malformed", // skipped
		"tcp://" + peerPK.Hex() + "@127.0.0.1:" + "65001", // never dialed successfully; that's fine
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := startCXOTCP(ctx); err != nil {
		t.Fatalf("startCXOTCP → startCXOGroup: %v", err)
	}

	cxoMu.Lock()
	sess, myPKSet := cxoGroupSess, cxoMyPK
	cxoMu.Unlock()
	if sess == nil {
		t.Fatal("a group session should be open")
	}
	if myPKSet != myPK {
		t.Errorf("cxoMyPK = %s, want %s", myPKSet.Hex()[:8], myPK.Hex()[:8])
	}
	if sess.ID() != cxoGroup {
		t.Errorf("session id = %q, want %q", sess.ID(), cxoGroup)
	}

	// publishCXO takes the group branch and reports "group" as the path.
	path, err := publishCXO("a group send")
	if err != nil {
		t.Fatalf("publishCXO in group mode: %v", err)
	}
	if path != "group" {
		t.Errorf("path = %q, want \"group\" — group mode routes through Session.Send", path)
	}
}
