// Package group cmd/apps/skychat/group/manager_state_test.go
//
// Unit coverage for the parts of manager.go that do NOT need a dmsg mesh: the
// session-level reconnect backoff, the warm-up cadence decision, the
// constructor's validation, and the store-backed accessors.
//
// manager.go is the largest untested body in skychat (~1600 lines, almost all
// at 0%) because most of it — Create/Join/Resume/SendToGroup/AddMember/
// Promote/Demote and the tryReconnect* family — opens real CXO sessions over
// dmsg. Those stay on the manual E2E plan, the same convention
// per_peer_backoff_test.go already follows. What IS reachable is the decision
// logic those paths consult, and that is what this file pins:
//
//   - reconnectShouldAttempt / reconnectRecordFailure — the SESSION-level
//     backoff schedule. per_peer_backoff_test.go covers the per-peer
//     counterparts; these are the ones tryReconnect and tryReconnectLegacySub
//     gate on, and the two schedules must stay independent of each other.
//   - hasWarmingPeer — decides whether the reconnect loop holds its fast
//     warmup cadence or eases back to the steady 30s tick. Its documented
//     guarantee is that ONE DEAD PEER CANNOT KEEP THE LOOP FAST FOREVER, which
//     is a property no integration test would notice going wrong.
//
// Sessions are the in-process fixture from roster_authority_test.go
// (newRosterSession + SetAllowlist): real peerSubs on an in-memory CXO node,
// no network.
package group

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// mustPK returns a fresh public key. Real keys throughout — cipher.PubKey
// validates the curve point, so synthetic bytes would not round-trip.
func mustPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

// dmsgStub satisfies NewManager's non-nil DmsgC requirement. Nothing here
// dials, so it is never dereferenced.
var dmsgStub = new(dmsg.Client)

// newStateTestManager builds a Manager on a real bbolt store with the
// reconnect maps initialized. No dmsg client: every path exercised here either
// stops at the store or reads sessions the test installs directly.
func newStateTestManager(t *testing.T, myPK cipher.PubKey) *Manager {
	t.Helper()
	st, err := OpenStore(filepath.Join(t.TempDir(), "groups.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() }) //nolint:errcheck
	return &Manager{
		store:              st,
		myPK:               myPK,
		log:                logging.MustGetLogger("group.manager-state-test"),
		portAlloc:          defaultPortAlloc,
		sessions:           make(map[string]*Session),
		reconnectState:     make(map[string]*reconnectState),
		peerReconnectState: make(map[string]map[cipher.PubKey]*reconnectState),
	}
}

// installSession registers sess under id so the Manager's session-keyed
// accessors can see it.
func installSession(m *Manager, id string, sess *Session) {
	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()
}

// seedRecord persists an active record for id with the given members.
func seedRecord(t *testing.T, m *Manager, id string, owner cipher.PubKey, members []cipher.PubKey, status Status) {
	t.Helper()
	r := Record{
		ID:      id,
		Name:    "state-test",
		OwnerPK: owner,
		Port:    60001,
		Mode:    ModePublic,
		Members: members,
		Role:    RoleMember,
		Status:  status,
	}
	if err := m.store.Put(r); err != nil {
		t.Fatalf("store.Put: %v", err)
	}
}

// --- session-level reconnect backoff ----------------------------------------

func TestReconnectShouldAttempt_DefaultsTrue(t *testing.T) {
	m := newStateTestManager(t, mustPK(t))
	if !m.reconnectShouldAttempt("group-1", time.Now().UTC()) {
		t.Error("no recorded failures → the loop should attempt a reconnect")
	}
}

func TestReconnectRecordFailure_BackoffSchedule(t *testing.T) {
	m := newStateTestManager(t, mustPK(t))
	const id = "group-1"
	now := time.Now().UTC()
	boom := errors.New("synthetic session reconnect failure")

	// Below the first threshold the loop keeps trying every tick.
	for i := uint32(1); i < reconnectBackoffFailures1; i++ {
		m.reconnectRecordFailure(id, boom)
		if !m.reconnectShouldAttempt(id, now) {
			t.Fatalf("at failure=%d (below threshold %d), want shouldAttempt=true", i, reconnectBackoffFailures1)
		}
	}

	// Crossing threshold 1 pushes the next attempt out by interval 1.
	m.reconnectRecordFailure(id, boom)
	if m.reconnectShouldAttempt(id, now) {
		t.Errorf("at failure=%d, want shouldAttempt=false inside the backoff window", reconnectBackoffFailures1)
	}
	if !m.reconnectShouldAttempt(id, now.Add(reconnectBackoffInterval1+time.Second)) {
		t.Error("once interval 1 elapses, the loop should attempt again")
	}

	// Crossing threshold 2 extends further — past interval 1 is no longer enough.
	for i := reconnectBackoffFailures1; i < reconnectBackoffFailures2; i++ {
		m.reconnectRecordFailure(id, boom)
	}
	if m.reconnectShouldAttempt(id, now.Add(reconnectBackoffInterval1+time.Second)) {
		t.Errorf("at failure=%d the longer interval should still be engaged", reconnectBackoffFailures2)
	}
	if !m.reconnectShouldAttempt(id, now.Add(reconnectBackoffInterval2+time.Second)) {
		t.Error("once interval 2 elapses, the loop should attempt again")
	}
}

func TestReconnectState_IndependentBetweenGroups(t *testing.T) {
	m := newStateTestManager(t, mustPK(t))
	for i := uint32(0); i < reconnectBackoffFailures1; i++ {
		m.reconnectRecordFailure("group-a", errors.New("force-state"))
	}
	now := time.Now().UTC()
	if m.reconnectShouldAttempt("group-a", now) {
		t.Error("group-a should be in backoff")
	}
	if !m.reconnectShouldAttempt("group-b", now) {
		t.Error("group-b state must be independent of group-a's")
	}
}

// TestReconnectState_SessionAndPeerSchedulesAreIndependent is the T2b property
// read from the session side: engaging the session-level backoff must not gate
// per-peer attempts, and vice versa. They share applyBackoffTransition but must
// not share state.
func TestReconnectState_SessionAndPeerSchedulesAreIndependent(t *testing.T) {
	m := newStateTestManager(t, mustPK(t))
	const id = "group-1"
	pk := mustPK(t)
	now := time.Now().UTC()

	for i := uint32(0); i < reconnectBackoffFailures1; i++ {
		m.reconnectRecordFailure(id, errors.New("session-level"))
	}
	if m.reconnectShouldAttempt(id, now) {
		t.Fatal("precondition: session-level backoff should be engaged")
	}
	if !m.peerReconnectShouldAttempt(id, pk, now) {
		t.Error("a session-level backoff must not gate per-peer reconnect attempts")
	}
	if got := m.peerReconnectFailures(id, pk); got != 0 {
		t.Errorf("peerReconnectFailures = %d, want 0 — session failures are not peer failures", got)
	}

	// And the reverse.
	m2 := newStateTestManager(t, mustPK(t))
	for i := uint32(0); i < reconnectBackoffFailures1; i++ {
		m2.peerReconnectRecordFailure(id, pk, errors.New("peer-level"))
	}
	if m2.peerReconnectShouldAttempt(id, pk, now) {
		t.Fatal("precondition: per-peer backoff should be engaged")
	}
	if !m2.reconnectShouldAttempt(id, now) {
		t.Error("a per-peer backoff must not gate the session-level attempt")
	}
}

// --- peerReconnectFailures --------------------------------------------------

func TestPeerReconnectFailures_Counts(t *testing.T) {
	m := newStateTestManager(t, mustPK(t))
	const id = "group-1"
	a, b := mustPK(t), mustPK(t)

	if got := m.peerReconnectFailures(id, a); got != 0 {
		t.Errorf("untracked peer = %d, want 0", got)
	}
	for i := 0; i < 3; i++ {
		m.peerReconnectRecordFailure(id, a, errors.New("x"))
	}
	if got := m.peerReconnectFailures(id, a); got != 3 {
		t.Errorf("after 3 failures = %d, want 3", got)
	}
	if got := m.peerReconnectFailures(id, b); got != 0 {
		t.Errorf("a different peer = %d, want 0", got)
	}
	if got := m.peerReconnectFailures("other-group", a); got != 0 {
		t.Errorf("the same peer in another group = %d, want 0", got)
	}
	m.peerReconnectClear(id, a)
	if got := m.peerReconnectFailures(id, a); got != 0 {
		t.Errorf("after clear = %d, want 0", got)
	}
}

// --- hasWarmingPeer ---------------------------------------------------------

// warmingFixture builds a manager with one active record whose live session
// follows `peer`, freshly added and therefore never attached.
func warmingFixture(t *testing.T) (m *Manager, id string, sess *Session, peer cipher.PubKey) {
	t.Helper()
	me, sk := cipher.GenerateKeyPair()
	peer = mustPK(t)
	id = uuid.NewString()

	m = newStateTestManager(t, me)
	seedRecord(t, m, id, me, []cipher.PubKey{me, peer}, StatusActive)

	sess = newRosterSession(t, me, sk, Record{ID: id, OwnerPK: me, Role: RoleOwner})
	if _, err := sess.SetAllowlist([]cipher.PubKey{me, peer}); err != nil {
		t.Fatalf("SetAllowlist: %v", err)
	}
	installSession(m, id, sess)
	return m, id, sess, peer
}

func TestHasWarmingPeer_NoRecords(t *testing.T) {
	m := newStateTestManager(t, mustPK(t))
	if m.hasWarmingPeer() {
		t.Error("an empty store has nothing warming")
	}
}

func TestHasWarmingPeer_NoLiveSession(t *testing.T) {
	me := mustPK(t)
	m := newStateTestManager(t, me)
	seedRecord(t, m, uuid.NewString(), me, []cipher.PubKey{me, mustPK(t)}, StatusActive)
	// Record exists but nothing was opened for it.
	if m.hasWarmingPeer() {
		t.Error("a record with no live session has no warming peer")
	}
}

// TestHasWarmingPeer_UnattachedPeerHoldsFastCadence — a freshly added peer has
// a zero PeerLastInbound and no failures, so the loop stays fast while it
// converges.
func TestHasWarmingPeer_UnattachedPeerHoldsFastCadence(t *testing.T) {
	m, _, sess, peer := warmingFixture(t)
	if !sess.PeerLastInbound(peer).IsZero() {
		t.Fatal("precondition: a peer that never connected should have a zero last-inbound")
	}
	if !m.hasWarmingPeer() {
		t.Error("an unattached peer within its retry budget should hold the fast cadence")
	}
}

// TestHasWarmingPeer_AttachedPeerReleasesFastCadence — once the peer reports an
// inbound it is converged, so the loop can ease back to the steady tick.
func TestHasWarmingPeer_AttachedPeerReleasesFastCadence(t *testing.T) {
	m, _, sess, peer := warmingFixture(t)

	sess.peerSubsMu.RLock()
	a := sess.peerLastInboundNs[peer]
	sess.peerSubsMu.RUnlock()
	if a == nil {
		t.Fatal("precondition: expected a liveness counter for the peer")
	}
	a.Store(time.Now().UnixNano())

	if m.hasWarmingPeer() {
		t.Error("an attached peer should not hold the fast cadence")
	}
}

// TestHasWarmingPeer_DeadPeerStopsHoldingFastCadence is the documented
// guarantee: a peer that never attaches stops qualifying once it burns its
// fast-retry budget, so one dead peer cannot pin the loop at the fast cadence
// forever. Without it the reconnect loop would spin fast indefinitely whenever
// any group has one permanently-offline member.
func TestHasWarmingPeer_DeadPeerStopsHoldingFastCadence(t *testing.T) {
	m, id, _, peer := warmingFixture(t)
	if !m.hasWarmingPeer() {
		t.Fatal("precondition: the peer should start out warming")
	}

	for i := uint32(0); i < reconnectBackoffFailures1-1; i++ {
		m.peerReconnectRecordFailure(id, peer, errors.New("unreachable"))
	}
	if !m.hasWarmingPeer() {
		t.Errorf("with %d failures (budget %d) the peer should still be warming",
			reconnectBackoffFailures1-1, reconnectBackoffFailures1)
	}

	m.peerReconnectRecordFailure(id, peer, errors.New("unreachable"))
	if m.hasWarmingPeer() {
		t.Errorf("after %d failures the peer has exhausted its fast-retry budget "+
			"and must fall to the backoff schedule, not hold the loop fast", reconnectBackoffFailures1)
	}
}

// TestHasWarmingPeer_SkipsInactiveRecords — left/revoked groups are not
// converging toward anything.
func TestHasWarmingPeer_SkipsInactiveRecords(t *testing.T) {
	for _, status := range []Status{StatusLeft, StatusRevoked} {
		t.Run(string(status), func(t *testing.T) {
			me, sk := cipher.GenerateKeyPair()
			peer := mustPK(t)
			id := uuid.NewString()

			m := newStateTestManager(t, me)
			seedRecord(t, m, id, me, []cipher.PubKey{me, peer}, status)
			sess := newRosterSession(t, me, sk, Record{ID: id, OwnerPK: me, Role: RoleOwner})
			if _, err := sess.SetAllowlist([]cipher.PubKey{me, peer}); err != nil {
				t.Fatalf("SetAllowlist: %v", err)
			}
			installSession(m, id, sess)

			if m.hasWarmingPeer() {
				t.Errorf("a %s record must not hold the fast cadence", status)
			}
		})
	}
}

// --- NewManager -------------------------------------------------------------

func TestNewManager_Validation(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "groups.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() }) //nolint:errcheck

	if _, err := NewManager(ManagerConfig{DmsgC: dmsgStub, DataDir: "/tmp"}); err == nil {
		t.Error("a nil Store must be rejected")
	}
	if _, err := NewManager(ManagerConfig{Store: st, DataDir: "/tmp"}); err == nil {
		t.Error("a nil DmsgC must be rejected")
	}
	if _, err := NewManager(ManagerConfig{Store: st, DmsgC: dmsgStub}); err == nil {
		t.Error("an empty DataDir must be rejected unless InMemoryDB is set")
	}
	// InMemoryDB waives the DataDir requirement (the js/wasm visor has no disk).
	if _, err := NewManager(ManagerConfig{Store: st, DmsgC: dmsgStub, InMemoryDB: true}); err != nil {
		t.Errorf("InMemoryDB should waive DataDir, got %v", err)
	}
}

func TestNewManager_Defaults(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "groups.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() }) //nolint:errcheck

	m, err := NewManager(ManagerConfig{
		Store: st, DmsgC: dmsgStub, DataDir: t.TempDir(),
		HeartbeatInterval: DefaultHeartbeatInterval,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if m.log == nil {
		t.Error("a nil Logger should be defaulted, not left nil")
	}
	if m.portAlloc == nil {
		t.Error("portAlloc should default to defaultPortAlloc")
	}
	// The maps must be non-nil or the first reconnect bookkeeping write panics.
	if m.sessions == nil || m.reconnectState == nil || m.peerReconnectState == nil {
		t.Error("NewManager must initialize sessions/reconnectState/peerReconnectState")
	}
	if m.heartbeatInterval != DefaultHeartbeatInterval {
		t.Errorf("heartbeatInterval = %v, want %v", m.heartbeatInterval, DefaultHeartbeatInterval)
	}
	// No sessions are opened until Resume.
	if len(m.sessions) != 0 {
		t.Errorf("NewManager opened %d sessions, want 0", len(m.sessions))
	}
}

// --- store-backed accessors -------------------------------------------------

func TestManagerGetAndList(t *testing.T) {
	me := mustPK(t)
	m := newStateTestManager(t, me)

	if _, ok, err := m.Get("nope"); err != nil || ok {
		t.Errorf("Get(unknown) = ok %v err %v, want false/nil", ok, err)
	}
	id := uuid.NewString()
	seedRecord(t, m, id, me, []cipher.PubKey{me}, StatusActive)

	r, ok, err := m.Get(id)
	if err != nil || !ok || r.ID != id {
		t.Fatalf("Get = %+v ok %v err %v", r, ok, err)
	}
	all, err := m.List()
	if err != nil || len(all) != 1 || all[0].ID != id {
		t.Errorf("List = %+v err %v, want the one seeded record", all, err)
	}
}

func TestBuildInvite(t *testing.T) {
	me := mustPK(t)
	m := newStateTestManager(t, me)

	if _, err := m.BuildInvite("missing"); err == nil {
		t.Error("BuildInvite for an unknown group should error")
	}

	// A non-admin member may not issue invites.
	otherOwner := mustPK(t)
	memberID := uuid.NewString()
	seedRecord(t, m, memberID, otherOwner, []cipher.PubKey{otherOwner, me}, StatusActive)
	if _, err := m.BuildInvite(memberID); err == nil {
		t.Error("a non-admin member must not be able to issue invites")
	}

	// The founder can, and the link round-trips.
	ownID := uuid.NewString()
	seedRecord(t, m, ownID, me, []cipher.PubKey{me}, StatusActive)
	link, err := m.BuildInvite(ownID)
	if err != nil {
		t.Fatalf("BuildInvite as founder: %v", err)
	}
	inv, err := DecodeInvite(link)
	if err != nil {
		t.Fatalf("DecodeInvite(%q): %v", link, err)
	}
	if inv.ID != ownID || inv.OwnerPK != me {
		t.Errorf("decoded invite = %+v, want id %s owner %s", inv, ownID, me.Hex()[:8])
	}
}

func TestMarkMessageDelivered(t *testing.T) {
	me := mustPK(t)
	m := newStateTestManager(t, me)
	id := uuid.NewString()
	seedRecord(t, m, id, me, []cipher.PubKey{me}, StatusActive)

	ts := time.Now().UTC().Truncate(time.Millisecond)
	m.MarkMessageDelivered(id, ts)

	r, ok, err := m.Get(id)
	if err != nil || !ok {
		t.Fatalf("Get: ok %v err %v", ok, err)
	}
	if r.LastMessageAt.IsZero() {
		t.Error("MarkMessageDelivered should refresh LastMessageAt")
	}
	// Best-effort for an unknown group: logged, never panics or errors out.
	m.MarkMessageDelivered("unknown", ts)
}

func TestIsSubscriberAlive_NoSession(t *testing.T) {
	m := newStateTestManager(t, mustPK(t))
	if m.IsSubscriberAlive("nope") {
		t.Error("no live session → not alive")
	}
}

func TestPeerLivenessAndUpdateCount(t *testing.T) {
	m := newStateTestManager(t, mustPK(t))

	// Graceful degradation: empty (non-nil) maps when there is no session, so
	// callers can range over the result without a nil check.
	if got := m.PeerLiveness("nope"); got == nil || len(got) != 0 {
		t.Errorf("PeerLiveness(unknown) = %v, want an empty non-nil map", got)
	}
	if got := m.PeerUpdateCount("nope"); got == nil || len(got) != 0 {
		t.Errorf("PeerUpdateCount(unknown) = %v, want an empty non-nil map", got)
	}

	m2, id, sess, peer := warmingFixture(t)
	live := m2.PeerLiveness(id)
	if len(live) != 1 {
		t.Fatalf("PeerLiveness = %v, want one entry", live)
	}
	if ts, ok := live[peer]; !ok || !ts.IsZero() {
		t.Errorf("PeerLiveness[peer] = %v ok=%v, want a zero time for a never-attached peer", ts, ok)
	}
	counts := m2.PeerUpdateCount(id)
	if got, ok := counts[peer]; !ok || got != 0 {
		t.Errorf("PeerUpdateCount[peer] = %d ok=%v, want 0", got, ok)
	}
	_ = sess
}

// --- handler wiring ---------------------------------------------------------

func TestSetMessageHandler_PropagatesToLiveSessions(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	m := newStateTestManager(t, me)
	id := uuid.NewString()
	sess := newRosterSession(t, me, sk, Record{ID: id, OwnerPK: me, Role: RoleOwner})
	installSession(m, id, sess)

	called := false
	m.SetMessageHandler(func(string, cipher.PubKey, Message) { called = true })

	m.onMessageMu.RLock()
	stored := m.onMessage
	m.onMessageMu.RUnlock()
	if stored == nil {
		t.Fatal("SetMessageHandler should record the handler on the manager")
	}
	// And it must reach the already-open session, not just future ones.
	h := sess.handler
	if h == nil {
		t.Fatal("SetMessageHandler should propagate to live sessions")
	}
	h(id, me, Message{Text: "x"})
	if !called {
		t.Error("the propagated handler should be the one that was installed")
	}
}

// TestWrapHandler_MarksMessageAndCallsThrough — the decorator is what keeps
// last_message_at fresh for INBOUND traffic; before it, the field only moved on
// outbound sends and member-side records read "0001-01-01" forever.
func TestWrapHandler_MarksMessageAndCallsThrough(t *testing.T) {
	me := mustPK(t)
	m := newStateTestManager(t, me)
	id := uuid.NewString()
	seedRecord(t, m, id, me, []cipher.PubKey{me}, StatusActive)

	var gotGroup string
	var gotText string
	wrapped := m.wrapHandler(id, func(g string, _ cipher.PubKey, msg Message) {
		gotGroup, gotText = g, msg.Text
	})

	ts := time.Now().UTC()
	wrapped(id, me, Message{Text: "inbound", TS: ts})

	if gotGroup != id || gotText != "inbound" {
		t.Errorf("inner handler saw (%q, %q), want (%q, inbound)", gotGroup, gotText, id)
	}
	r, ok, err := m.Get(id)
	if err != nil || !ok {
		t.Fatalf("Get: ok %v err %v", ok, err)
	}
	if r.LastMessageAt.IsZero() {
		t.Error("wrapHandler should refresh LastMessageAt on inbound traffic")
	}
}

// --- persistRoster ----------------------------------------------------------

// TestPersistRoster_WritesConvergedRoster covers the #3426 follow-up: the
// reconciler's converged member+admin set has to reach the store, or group
// info/list keep showing the pre-convergence roster and it is lost on restart.
func TestPersistRoster_WritesConvergedRoster(t *testing.T) {
	founder := mustPK(t)
	m := newStateTestManager(t, founder)
	id := uuid.NewString()
	seedRecord(t, m, id, founder, []cipher.PubKey{founder}, StatusActive)

	a, b := mustPK(t), mustPK(t)
	m.persistRoster(id)([]cipher.PubKey{founder, a, b}, []cipher.PubKey{a})

	r, ok, err := m.Get(id)
	if err != nil || !ok {
		t.Fatalf("Get: ok %v err %v", ok, err)
	}
	assertPKSet(t, "persisted members", r.Members, []cipher.PubKey{founder, a, b})
	// EnsureFounderInAdmins runs on the way in, so the founder is always
	// explicitly present even though the reconciler didn't list them.
	assertPKSet(t, "persisted admins", r.Admins, []cipher.PubKey{founder, a})
	if !r.IsAdmin(founder) {
		t.Error("the founder must remain an admin after a roster persist")
	}
}

func TestPersistRoster_UnknownGroupIsNoOp(t *testing.T) {
	m := newStateTestManager(t, mustPK(t))
	// Must not panic or create a record out of thin air.
	m.persistRoster("missing")([]cipher.PubKey{mustPK(t)}, nil)
	all, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("persistRoster for an unknown group created %d records, want 0", len(all))
	}
}

// --- terminate / Leave / Delete ---------------------------------------------

func TestLeave_MarksLeftAndDropsSessionState(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	m := newStateTestManager(t, me)
	id := uuid.NewString()
	peer := mustPK(t)
	seedRecord(t, m, id, me, []cipher.PubKey{me, peer}, StatusActive)

	sess := newRosterSession(t, me, sk, Record{ID: id, OwnerPK: me, Role: RoleOwner})
	installSession(m, id, sess)
	m.reconnectRecordFailure(id, errors.New("x"))
	m.peerReconnectRecordFailure(id, peer, errors.New("x"))

	if err := m.Leave(id); err != nil {
		t.Fatalf("Leave: %v", err)
	}

	r, ok, err := m.Get(id)
	if err != nil || !ok {
		t.Fatalf("Get: ok %v err %v", ok, err)
	}
	if r.Status != StatusLeft {
		t.Errorf("status = %q, want %q", r.Status, StatusLeft)
	}
	m.mu.RLock()
	_, stillLive := m.sessions[id]
	m.mu.RUnlock()
	if stillLive {
		t.Error("Leave should drop the live session")
	}
	// Reconnect bookkeeping for a departed group would otherwise be
	// unreachable memory until process restart.
	m.reconnectMu.Lock()
	_, sessState := m.reconnectState[id]
	_, peerState := m.peerReconnectState[id]
	m.reconnectMu.Unlock()
	if sessState || peerState {
		t.Error("Leave should drop the group's reconnect state")
	}
}

func TestLeave_UnknownGroupIsIdempotent(t *testing.T) {
	m := newStateTestManager(t, mustPK(t))
	if err := m.Leave("missing"); err != nil {
		t.Errorf("Leave(unknown) = %v, want nil (idempotent)", err)
	}
}

func TestDelete_RequiresAdmin(t *testing.T) {
	me := mustPK(t)
	m := newStateTestManager(t, me)

	// Someone else's group, we're a plain member → must refuse and leave the
	// record untouched, so a typo can't revoke a group another admin owns.
	otherOwner := mustPK(t)
	id := uuid.NewString()
	seedRecord(t, m, id, otherOwner, []cipher.PubKey{otherOwner, me}, StatusActive)
	if err := m.Delete(id); err == nil {
		t.Error("a non-admin must not be able to revoke a group")
	}
	r, _, _ := m.Get(id) //nolint:errcheck
	if r.Status != StatusActive {
		t.Errorf("status = %q after a refused Delete, want it untouched at %q", r.Status, StatusActive)
	}

	// Our own group → revoked.
	ownID := uuid.NewString()
	seedRecord(t, m, ownID, me, []cipher.PubKey{me}, StatusActive)
	if err := m.Delete(ownID); err != nil {
		t.Fatalf("Delete as founder: %v", err)
	}
	own, _, _ := m.Get(ownID) //nolint:errcheck
	if own.Status != StatusRevoked {
		t.Errorf("status = %q, want %q", own.Status, StatusRevoked)
	}

	// Unknown id is idempotent, not an error.
	if err := m.Delete("missing"); err != nil {
		t.Errorf("Delete(unknown) = %v, want nil", err)
	}
}

// --- Close ------------------------------------------------------------------

func TestManagerClose_ClearsSessions(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	m := newStateTestManager(t, me)
	id := uuid.NewString()
	installSession(m, id, newRosterSession(t, me, sk, Record{ID: id, OwnerPK: me, Role: RoleOwner}))

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	m.mu.RLock()
	n := len(m.sessions)
	m.mu.RUnlock()
	if n != 0 {
		t.Errorf("Close left %d sessions, want 0", n)
	}
	// Idempotent — a second Close with no reconnect loop running must not hang
	// on the waitgroup or double-cancel.
	if err := m.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// --- kickReconnect ----------------------------------------------------------

// TestKickReconnect_NoOpCases — SendToGroup calls this on every send, so the
// cheap guards matter: an unknown group, a session with no legacy subscriber,
// and a healthy subscriber must all return without spawning a reconnect.
func TestKickReconnect_NoOpCases(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	m := newStateTestManager(t, me)

	m.kickReconnect(t.Context(), "unknown") // no session

	id := uuid.NewString()
	sess := newRosterSession(t, me, sk, Record{ID: id, OwnerPK: me, Role: RoleOwner})
	installSession(m, id, sess) // sess.sub is nil (owner role)
	m.kickReconnect(t.Context(), id)
}

// --- defaultPortAlloc -------------------------------------------------------

func TestDefaultPortAlloc_InRange(t *testing.T) {
	// Must stay clear of the pairing range; a collision there would have two
	// subsystems fighting over the same dmsg port.
	seen := map[uint16]bool{}
	for i := 0; i < 200; i++ {
		p, err := defaultPortAlloc()
		if err != nil {
			t.Fatalf("defaultPortAlloc: %v", err)
		}
		if p < 60000 || p >= 65000 {
			t.Fatalf("port %d outside [60000, 65000)", p)
		}
		seen[p] = true
	}
	if len(seen) < 2 {
		t.Errorf("200 allocations produced %d distinct ports — the allocator looks stuck", len(seen))
	}
}
