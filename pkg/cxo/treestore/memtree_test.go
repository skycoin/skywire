package treestore

import (
	"sort"
	"testing"
)

// These tests exercise the in-memory tree operations directly (no
// CXO involved) so the path/conflict logic can be validated cheaply.
// CXO round-trip lives in publisher_test.go.

func TestMemNodePutGet(t *testing.T) {
	root := newMemNode()
	if err := putAt(root, []string{"a", "b", "c"}, []byte("hello")); err != nil {
		t.Fatalf("putAt: %v", err)
	}
	got, ok := getAt(root, []string{"a", "b", "c"})
	if !ok || string(got) != "hello" {
		t.Fatalf("getAt: got %q ok=%v", got, ok)
	}
	// Sub-tree at a/b should exist and be a memNode, not a leaf.
	if _, ok := root.subs["a"].subs["b"].leaves["c"]; !ok {
		t.Fatal("leaf c not at expected position")
	}
	if _, ok := root.subs["a"].subs["b"].subs["c"]; ok {
		t.Fatal("c should not be a sub-tree")
	}
}

func TestMemNodePutLeafConflictsWithSubtree(t *testing.T) {
	root := newMemNode()
	if err := putAt(root, []string{"a", "b"}, []byte("v")); err != nil {
		t.Fatal(err)
	}
	// Now try to put at "a" — conflicts because "a" is a sub-tree.
	err := putAt(root, []string{"a"}, []byte("v"))
	if err == nil {
		t.Fatal("expected PathConflictError when putting leaf at sub-tree position")
	}
	if _, ok := err.(*PathConflictError); !ok {
		t.Fatalf("expected *PathConflictError, got %T", err)
	}
}

func TestMemNodePutDescentThroughLeafFails(t *testing.T) {
	root := newMemNode()
	if err := putAt(root, []string{"a"}, []byte("v")); err != nil {
		t.Fatal(err)
	}
	// "a" is now a leaf; putting at "a/b" must fail.
	if err := putAt(root, []string{"a", "b"}, []byte("v")); err == nil {
		t.Fatal("expected error when descending through a leaf")
	}
}

func TestMemNodeDeletePrunesEmptyParents(t *testing.T) {
	root := newMemNode()
	mustPutAt(t, root, []string{"a", "b", "c"}, []byte("v"))
	mustPutAt(t, root, []string{"a", "b", "d"}, []byte("w"))

	// Delete one leaf — parent still has the other, no pruning.
	if !deleteAt(root, []string{"a", "b", "c"}) {
		t.Fatal("expected deleteAt to return true")
	}
	if _, ok := root.subs["a"].subs["b"]; !ok {
		t.Fatal("parent should still exist with sibling leaf present")
	}

	// Delete the second leaf — now a/b and a should be pruned.
	if !deleteAt(root, []string{"a", "b", "d"}) {
		t.Fatal("expected deleteAt to return true")
	}
	if _, ok := root.subs["a"]; ok {
		t.Fatalf("expected a to be pruned, root.subs = %+v", root.subs)
	}
}

func TestMemNodePruneAt(t *testing.T) {
	root := newMemNode()
	mustPutAt(t, root, []string{"keep", "x"}, []byte("k"))
	mustPutAt(t, root, []string{"drop", "x"}, []byte("d"))
	mustPutAt(t, root, []string{"drop", "y"}, []byte("e"))

	if !pruneAt(root, []string{"drop"}) {
		t.Fatal("pruneAt should return true")
	}
	if _, ok := root.subs["drop"]; ok {
		t.Fatal("drop subtree should be gone")
	}
	if _, ok := root.subs["keep"].leaves["x"]; !ok {
		t.Fatal("keep/x should still be present")
	}
}

func TestMemNodeWalkLeavesSorted(t *testing.T) {
	root := newMemNode()
	mustPutAt(t, root, []string{"b", "y"}, []byte("by"))
	mustPutAt(t, root, []string{"a", "z"}, []byte("az"))
	mustPutAt(t, root, []string{"a", "x"}, []byte("ax"))

	var visits []string
	walkLeaves(root, "", func(p string, _ []byte) bool {
		visits = append(visits, p)
		return true
	})
	want := []string{"a/x", "a/z", "b/y"}
	if !sort.StringsAreSorted(visits) {
		t.Errorf("visits not sorted: %v", visits)
	}
	if !equalSlice(visits, want) {
		t.Errorf("visits = %v, want %v", visits, want)
	}
}

func TestSortedNamesIsStable(t *testing.T) {
	root := newMemNode()
	mustPutAt(t, root, []string{"z"}, []byte("v"))
	mustPutAt(t, root, []string{"a"}, []byte("v"))
	mustPutAt(t, root, []string{"m"}, []byte("v"))
	got := sortedNames(root)
	want := []string{"a", "m", "z"}
	if !equalSlice(got, want) {
		t.Errorf("sortedNames = %v, want %v", got, want)
	}
}

// mustPutAt is a fatal-on-error wrapper so test setup failures
// surface immediately instead of being silently dropped.
func mustPutAt(t *testing.T, root *memNode, segs []string, value []byte) {
	t.Helper()
	if err := putAt(root, segs, value); err != nil {
		t.Fatalf("putAt %v: %v", segs, err)
	}
}
