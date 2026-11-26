package treestore

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// expandedEntries counts the entries of the expanded part of the tree, the
// figure MemoryEntries bounds.
func expandedEntries(n *memNode) int {
	total := len(n.leaves) + len(n.subs)
	for _, sub := range n.subs {
		if !sub.folded {
			total += expandedEntries(sub)
		}
	}
	return total
}

func foldTestPath(i int) string {
	day := i / 20
	return fmt.Sprintf("msgs/sender/2026%02d/%02d/%d/%d", day/28+1, day%28+1, 1000+i, i)
}

func walkAll(p *Publisher) map[string]string {
	out := map[string]string{}
	p.Walk("", func(path string, v []byte) bool {
		out[path] = string(v)
		return true
	})
	return out
}

func TestPublisherFold_KeepsEveryLeafWithinBudget(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	n := fileBackedNode(t, filepath.Join(t.TempDir(), "node"), sk)
	t.Cleanup(func() { _ = n.Close() }) //nolint:errcheck

	const budget, total = 40, 400
	p, err := New(n, sk, Config{BatchWindow: time.Millisecond, MemoryEntries: budget, KeepRoots: 4})
	require.NoError(t, err)
	for i := range total {
		require.NoError(t, p.Put(foldTestPath(i), fmt.Appendf(nil, "v%d", i)))
		if i%50 == 49 {
			require.NoError(t, p.Flush())
		}
	}
	require.NoError(t, p.Flush())

	p.mu.Lock()
	expanded := expandedEntries(p.root)
	p.mu.Unlock()
	require.LessOrEqual(t, expanded, budget+10, "expanded tree past the budget")
	p.mu.Lock()
	newest := walkLivePath(p.root, []string{"msgs", "sender", "202601", "20"})
	p.mu.Unlock()
	require.NotNil(t, newest, "the newest day folded before older ones")

	got := walkAll(p)
	require.Len(t, got, total)
	require.Equal(t, "v7", got[foldTestPath(7)])

	v, ok := p.Get(foldTestPath(3))
	require.True(t, ok, "Get of a folded leaf")
	require.Equal(t, "v3", string(v))

	require.NoError(t, p.Delete(foldTestPath(5)))
	require.NoError(t, p.Put(foldTestPath(6), []byte("changed")))
	require.NoError(t, p.Flush())
	require.NoError(t, p.Close())

	// A publisher without folding reads the whole published tree back.
	full, err := New(n, sk, Config{BatchWindow: time.Millisecond})
	require.NoError(t, err)
	got = walkAll(full)
	require.NoError(t, full.Close())
	require.Len(t, got, total-1)
	require.NotContains(t, got, foldTestPath(5))
	require.Equal(t, "changed", got[foldTestPath(6)])
	require.Equal(t, "v399", got[foldTestPath(399)])
}

func TestPublisherFold_RestartReadsOnlyTheRootLevel(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	n := fileBackedNode(t, filepath.Join(t.TempDir(), "node"), sk)
	t.Cleanup(func() { _ = n.Close() }) //nolint:errcheck

	cfg := Config{BatchWindow: time.Millisecond, MemoryEntries: 20, KeepRoots: 4}
	p, err := New(n, sk, cfg)
	require.NoError(t, err)
	for i := range 100 {
		require.NoError(t, p.Put(foldTestPath(i), fmt.Appendf(nil, "v%d", i)))
	}
	require.NoError(t, p.Put("roster/00001", []byte("r")))
	require.NoError(t, p.Close())

	p, err = New(n, sk, cfg)
	require.NoError(t, err)
	p.mu.Lock()
	require.Equal(t, 2, expandedEntries(p.root), "a restart expands only the root level")
	p.mu.Unlock()
	require.Len(t, walkAll(p), 101)

	require.NoError(t, p.Put(foldTestPath(100), []byte("after restart")))
	require.NoError(t, p.Flush())
	require.NoError(t, p.Close())

	full, err := New(n, sk, Config{BatchWindow: time.Millisecond})
	require.NoError(t, err)
	got := walkAll(full)
	require.NoError(t, full.Close())
	require.Len(t, got, 102)
	require.Equal(t, "after restart", got[foldTestPath(100)])
	require.Equal(t, "v0", got[foldTestPath(0)])
}

func TestPublisherFold_PrunePrefixUnderFoldedTree(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	n := fileBackedNode(t, filepath.Join(t.TempDir(), "node"), sk)
	t.Cleanup(func() { _ = n.Close() }) //nolint:errcheck

	p, err := New(n, sk, Config{BatchWindow: time.Millisecond, MemoryEntries: 4, KeepRoots: 4})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck
	for i := range 60 {
		require.NoError(t, p.Put(foldTestPath(i), []byte("v")))
	}
	require.NoError(t, p.Flush())

	// The ts node of message 2, as Session.Unsend prunes it.
	require.NoError(t, p.PrunePrefix("msgs/sender/202601/01/1002"))
	require.NoError(t, p.Flush())
	got := walkAll(p)
	require.Len(t, got, 59)
	require.NotContains(t, got, foldTestPath(2))
}

func TestDiffStored_ReportsOnlyTheChange(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	n := fileBackedNode(t, filepath.Join(t.TempDir(), "node"), sk)
	t.Cleanup(func() { _ = n.Close() }) //nolint:errcheck

	p, err := New(n, sk, Config{BatchWindow: time.Millisecond, KeepRoots: 4})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck
	for i := range 30 {
		require.NoError(t, p.Put(foldTestPath(i), []byte("v")))
	}
	require.NoError(t, p.Put("a/leaf", []byte("x")))
	require.NoError(t, p.Flush())
	before := rootHash(p.lastRoot.Load())

	require.NoError(t, p.Put(foldTestPath(30), []byte("new")))
	require.NoError(t, p.Put(foldTestPath(1), []byte("changed")))
	require.NoError(t, p.Delete(foldTestPath(2)))
	require.NoError(t, p.Delete("a/leaf"))
	require.NoError(t, p.Put("a/leaf/now-a-tree", []byte("t")))
	require.NoError(t, p.Flush())
	after := rootHash(p.lastRoot.Load())

	rd := &storeReader{p: p}
	defer rd.close()
	pack, err := rd.pack()
	require.NoError(t, err)
	got := map[string]string{}
	require.NoError(t, diffStored(pack, before, after, "", func(path string, v []byte) {
		if v == nil {
			got[path] = "<deleted>"
			return
		}
		got[path] = string(v)
	}))
	require.Equal(t, map[string]string{
		foldTestPath(30):    "new",
		foldTestPath(1):     "changed",
		foldTestPath(2):     "<deleted>",
		"a/leaf":            "<deleted>",
		"a/leaf/now-a-tree": "t",
	}, got)
}
