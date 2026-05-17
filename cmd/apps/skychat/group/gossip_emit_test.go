// Package group — cmd/apps/skychat/group/gossip_emit_test.go:
// targeted coverage for Session.PublishRosterMutation /
// PublishAdminMutation (step 2 of #2636 RFC).
//
// Scope: the publish helpers' contract — seq monotonicity, signed
// envelope reachable via the publisher's local view, the
// session-without-publisher error path. Cross-visor convergence is
// the reconciler's responsibility (step 3) and lives in its own
// test file.
package group

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/logging"
)

// nilPublisherSession is the smallest Session that exercises the
// nil-publisher guard in the Publish helpers. Mirrors the shape of
// a sessions-but-degraded-state Session (no DMSG, no publisher) —
// we don't run treestore.NewWithDMSG here because the helpers'
// no-publisher branch is the only thing under test.
func nilPublisherSession(t *testing.T) *Session {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	return &Session{
		cfg: Config{
			MyPK: pk,
			MySK: sk,
			Record: Record{
				ID:      "00000000-0000-0000-0000-000000000001",
				OwnerPK: pk,
			},
		},
	}
}

func TestPublishRosterMutationRejectsNilPublisher(t *testing.T) {
	s := nilPublisherSession(t)
	peer, _ := cipher.GenerateKeyPair()
	_, err := s.PublishRosterMutation(RosterOpAdd, peer, 0)
	if err == nil {
		t.Fatal("expected error when publisher is nil")
	}
}

func TestPublishAdminMutationRejectsNilPublisher(t *testing.T) {
	s := nilPublisherSession(t)
	peer, _ := cipher.GenerateKeyPair()
	_, err := s.PublishAdminMutation(AdminOpPromote, peer, 0)
	if err == nil {
		t.Fatal("expected error when publisher is nil")
	}
}

// testPublisherSession builds an in-memory Publisher (no DMSG —
// pkg/cxo/treestore.New plus a no-op CXO node) wrapped in a minimal
// Session whose cfg carries the publisher's PK/SK and a valid group
// ID. Enough to exercise PublishRosterMutation / PublishAdminMutation
// end-to-end including the publisher's Put round-trip.
func testPublisherSession(t *testing.T) *Session {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	pub := newInProcessPublisher(t, sk)
	return &Session{
		cfg: Config{
			MyPK: pk,
			MySK: sk,
			Record: Record{
				ID:      uuid.NewString(),
				OwnerPK: pk,
			},
		},
		pub: pub,
		log: logging.MustGetLogger("group.gossip-emit-test"),
	}
}

// newInProcessPublisher builds a Publisher backed by an in-memory CXO
// node with no network listeners. Mirrors publisher_test.go's nopNode
// pattern. Suitable for emit unit-tests: the publisher accepts Puts
// and stores them in the in-memory container; the broadcast side is
// a no-op because no peers are connected.
func newInProcessPublisher(t *testing.T, sk cipher.SecKey) *treestore.Publisher {
	t.Helper()
	cfg := node.NewConfig()
	cfg.TCP.Listen = ""
	cfg.UDP.Listen = ""
	cfg.RPC = ""
	cfg.Config.InMemoryDB = true
	n, err := node.NewNode(cfg)
	if err != nil {
		t.Fatalf("node.NewNode: %v", err)
	}
	t.Cleanup(func() { _ = n.Close() }) //nolint:errcheck
	pub, err := treestore.New(n, sk, treestore.Config{BatchWindow: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("treestore.New: %v", err)
	}
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
	return pub
}

// pubReadable polls the publisher's in-memory view until path is
// readable, then returns its body. Bounded poll guards against the
// (very unlikely in this in-memory path) async-publish race.
func pubReadable(t *testing.T, pub *treestore.Publisher, path string) []byte {
	t.Helper()
	for i := 0; i < 100; i++ {
		body, ok := pub.Get(path)
		if ok {
			return body
		}
		// no-op — Get is in-process, no delay needed beyond the
		// loop's natural retry cadence.
	}
	t.Fatalf("pubReadable: %s not present after retries", path)
	return nil
}

func TestPublishRosterMutationLandsOnFeed(t *testing.T) {
	s := testPublisherSession(t)
	peer, _ := cipher.GenerateKeyPair()

	seq1, err := s.PublishRosterMutation(RosterOpAdd, peer, 0)
	if err != nil {
		t.Fatalf("PublishRosterMutation #1: %v", err)
	}
	if seq1 != 1 {
		t.Fatalf("first publish seq: want 1 got %d", seq1)
	}

	seq2, err := s.PublishRosterMutation(RosterOpRemove, peer, seq1)
	if err != nil {
		t.Fatalf("PublishRosterMutation #2: %v", err)
	}
	if seq2 != 2 {
		t.Fatalf("second publish seq: want 2 got %d", seq2)
	}

	// Read back leaf #1 and verify the envelope decodes + verifies.
	body := pubReadable(t, s.pub, fmt.Sprintf("%s/%05d", RosterPathPrefix, seq1))
	m, err := UnmarshalRoster(body)
	if err != nil {
		t.Fatalf("UnmarshalRoster: %v", err)
	}
	if m.Op != RosterOpAdd {
		t.Errorf("Op: want RosterOpAdd got %v", m.Op)
	}
	if m.PeerPK != peer {
		t.Errorf("PeerPK mismatch")
	}
	if m.IssuerPK != s.cfg.MyPK {
		t.Errorf("IssuerPK: want %s got %s", s.cfg.MyPK, m.IssuerPK)
	}
	if m.ParentSeq != 0 {
		t.Errorf("ParentSeq: want 0 got %d", m.ParentSeq)
	}

	// And #2 carries the chained ParentSeq.
	body2 := pubReadable(t, s.pub, fmt.Sprintf("%s/%05d", RosterPathPrefix, seq2))
	m2, err := UnmarshalRoster(body2)
	if err != nil {
		t.Fatalf("UnmarshalRoster #2: %v", err)
	}
	if m2.ParentSeq != seq1 {
		t.Errorf("ParentSeq chain: want %d got %d", seq1, m2.ParentSeq)
	}
}

func TestPublishAdminMutationLandsOnFeed(t *testing.T) {
	s := testPublisherSession(t)
	peer, _ := cipher.GenerateKeyPair()

	seq, err := s.PublishAdminMutation(AdminOpPromote, peer, 0)
	if err != nil {
		t.Fatalf("PublishAdminMutation: %v", err)
	}
	if seq != 1 {
		t.Fatalf("first admin publish seq: want 1 got %d", seq)
	}

	body := pubReadable(t, s.pub, fmt.Sprintf("%s/%05d", AdminPathPrefix, seq))
	m, err := UnmarshalAdmin(body)
	if err != nil {
		t.Fatalf("UnmarshalAdmin: %v", err)
	}
	if m.Op != AdminOpPromote {
		t.Errorf("Op: want AdminOpPromote got %v", m.Op)
	}
	if m.PeerPK != peer {
		t.Errorf("PeerPK mismatch")
	}
	if m.IssuerPK != s.cfg.MyPK {
		t.Errorf("IssuerPK: want %s got %s", s.cfg.MyPK, m.IssuerPK)
	}
}

func TestPublishSequenceIsAtomic(t *testing.T) {
	// Concurrent publishes must each get a distinct seq with no
	// gaps. Two goroutines hammer the helper; the seq counter
	// (atomic.Uint64.Add) is the single source of monotonicity.
	s := testPublisherSession(t)
	peer, _ := cipher.GenerateKeyPair()

	const N = 50
	var (
		seen [N + 1]atomic.Bool
		errs atomic.Uint64
	)
	done := make(chan struct{}, 2)
	for w := 0; w < 2; w++ {
		go func() {
			for i := 0; i < N/2; i++ {
				seq, err := s.PublishRosterMutation(RosterOpAdd, peer, 0)
				if err != nil {
					errs.Add(1)
					continue
				}
				if seq < 1 || seq > uint64(N) {
					errs.Add(1)
					continue
				}
				if seen[seq].Swap(true) {
					errs.Add(1) // duplicate seq — would be a bug in atomic.Add
				}
			}
			done <- struct{}{}
		}()
	}
	<-done
	<-done
	if errs.Load() != 0 {
		t.Fatalf("concurrent publish: %d errors / collisions observed", errs.Load())
	}
	// Every seq 1..N must have been observed exactly once.
	for i := 1; i <= N; i++ {
		if !seen[i].Load() {
			t.Errorf("seq %d not observed", i)
		}
	}
}
