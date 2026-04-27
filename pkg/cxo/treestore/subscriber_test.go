package treestore

import (
	"sort"
	"sync"
	"testing"

	"github.com/skycoin/skywire/pkg/logging"
)

// These tests exercise the subscriber's pure logic — snapshot diff
// and prefix filtering — without spinning up DMSG / a real publisher.
// End-to-end coverage lives in the integration commits where TPD
// actually subscribes to a visor.

func newTestSubscriber(t *testing.T) *Subscriber {
	t.Helper()
	// Construct a Subscriber struct directly without NewSubscriber's
	// DMSG side effects. We're testing applySnapshot/Get/Walk; the
	// DMSG-attached cxoNode isn't on the hot path for these tests.
	return &Subscriber{
		log:   logging.MustGetLogger("treestore-sub-test"),
		cache: make(map[string][]byte),
	}
}

func TestSubscriberApplySnapshotFiresChangesOnce(t *testing.T) {
	s := newTestSubscriber(t)

	var (
		gotMu   sync.Mutex
		got     []UpdateEvent
		batches int
	)
	s.OnUpdate(func(changes []UpdateEvent) {
		gotMu.Lock()
		defer gotMu.Unlock()
		got = append(got, changes...)
		batches++
	})

	first := map[string][]byte{
		"a/b": []byte("v1"),
		"c/d": []byte("v2"),
	}
	s.applySnapshot(first)

	gotMu.Lock()
	if batches != 1 {
		t.Errorf("first snapshot: batches = %d, want 1", batches)
	}
	if len(got) != 2 {
		t.Errorf("first snapshot: changes = %v, want 2 events", got)
	}
	got = nil
	batches = 0
	gotMu.Unlock()

	// Second snapshot: a/b mutated, c/d unchanged, e/f new.
	second := map[string][]byte{
		"a/b": []byte("v1-mutated"),
		"c/d": []byte("v2"),
		"e/f": []byte("v3"),
	}
	s.applySnapshot(second)

	gotMu.Lock()
	defer gotMu.Unlock()
	if batches != 1 {
		t.Errorf("second snapshot: batches = %d, want 1", batches)
	}
	paths := pathsOf(got)
	sort.Strings(paths)
	want := []string{"a/b", "e/f"}
	if !equalSlice(paths, want) {
		t.Errorf("second snapshot: changed paths = %v, want %v (c/d should not be reported as changed)", paths, want)
	}
}

func TestSubscriberApplySnapshotReportsDeletes(t *testing.T) {
	s := newTestSubscriber(t)
	var got []UpdateEvent
	s.OnUpdate(func(changes []UpdateEvent) {
		got = append(got, changes...)
	})

	s.applySnapshot(map[string][]byte{"a": []byte("v"), "b": []byte("v")})
	got = nil

	s.applySnapshot(map[string][]byte{"b": []byte("v")})
	if len(got) != 1 {
		t.Fatalf("expected one delete event, got %d: %v", len(got), got)
	}
	if got[0].Path != "a" || got[0].Value != nil {
		t.Errorf("expected {a, nil}, got %+v", got[0])
	}
}

func TestSubscriberPrefixFilterOnGetWalkCallback(t *testing.T) {
	s := newTestSubscriber(t)
	s.SetPrefixes([]string{"keep/"})

	var changes []UpdateEvent
	s.OnUpdate(func(c []UpdateEvent) { changes = append(changes, c...) })

	s.applySnapshot(map[string][]byte{
		"keep/x": []byte("k"),
		"drop/x": []byte("d"),
	})

	// Callback should only see the keep/ entry.
	if len(changes) != 1 || changes[0].Path != "keep/x" {
		t.Fatalf("callback should be filtered to keep/x, got %v", changes)
	}

	// Get on a filtered-out path returns false even though the
	// entry is in the cache.
	if _, ok := s.Get("drop/x"); ok {
		t.Error("Get on filtered path should return false")
	}
	if v, ok := s.Get("keep/x"); !ok || string(v) != "k" {
		t.Error("Get on matching path should return value")
	}

	// Walk also filters.
	var visited []string
	s.Walk("", func(p string, _ []byte) bool {
		visited = append(visited, p)
		return true
	})
	if len(visited) != 1 || visited[0] != "keep/x" {
		t.Errorf("Walk should be filtered, got %v", visited)
	}
}

func TestSubscriberSetPrefixesNormalisesTrailingSlash(t *testing.T) {
	s := newTestSubscriber(t)
	s.SetPrefixes([]string{"a/b/"}) // trailing slash should be tolerated
	s.applySnapshot(map[string][]byte{"a/b/x": []byte("v"), "other": []byte("v")})
	if v, ok := s.Get("a/b/x"); !ok || string(v) != "v" {
		t.Error("a/b/ should match a/b/x")
	}
	if _, ok := s.Get("other"); ok {
		t.Error("other should be filtered out")
	}
}

func TestSubscriberCallbackNotInvokedIfNoChanges(t *testing.T) {
	s := newTestSubscriber(t)
	var calls int
	s.OnUpdate(func([]UpdateEvent) { calls++ })

	first := map[string][]byte{"a": []byte("v")}
	s.applySnapshot(first)
	calls = 0

	// Same snapshot — no changes, no callback.
	s.applySnapshot(map[string][]byte{"a": []byte("v")})
	if calls != 0 {
		t.Errorf("callback fired %d times for unchanged snapshot, want 0", calls)
	}
}

func pathsOf(events []UpdateEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Path)
	}
	return out
}
