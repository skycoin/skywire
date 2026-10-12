package treestore

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A publisher with DropPublishedLeaves keeps no leaf bytes for a published
// sub-tree, still serves them, and republishes a changed sub-tree with the
// leaves it did not change.
func TestPublisherDropPublishedLeaves(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	_, sk := cipher.GenerateKeyPair()
	conf := PubConfig{DataDir: dir, BatchWindow: 5 * time.Millisecond, DropPublishedLeaves: true}
	pub, err := NewWithTCP("127.0.0.1:0", sk, conf)
	require.NoError(t, err)

	body := func(day, part int) []byte { return bytes.Repeat([]byte(fmt.Sprintf("d%dp%d;", day, part)), 1000) }
	path := func(day, part int) string { return fmt.Sprintf("metrics/day/2026-10-%02d/part/%04d", day, part) }
	var ops []PutOp
	for d := 1; d <= 3; d++ {
		for p := 0; p < 2; p++ {
			ops = append(ops, PutOp{Path: path(d, p), Value: body(d, p)})
		}
	}
	require.NoError(t, pub.PutBatch(ops))
	require.NoError(t, pub.Flush())

	pub.mu.Lock()
	part := walkLivePath(pub.root, []string{"metrics", "day", "2026-10-02", "part"})
	require.NotNil(t, part)
	for name, v := range part.leaves {
		require.Nil(t, v, "leaf %s kept in memory", name)
	}
	pub.mu.Unlock()

	v, ok := pub.Get(path(2, 1))
	require.True(t, ok)
	require.Equal(t, body(2, 1), v)
	seen := map[string][]byte{}
	pub.Walk("metrics/day", func(p string, v []byte) bool { seen[p] = v; return true })
	require.Len(t, seen, 6)
	require.Equal(t, body(3, 0), seen[path(3, 0)])

	// Change one part of an evicted day and delete another day's part.
	changed := []byte("changed")
	require.NoError(t, pub.PutBatch([]PutOp{{Path: path(2, 0), Value: changed}, {Path: path(3, 1)}}))
	require.NoError(t, pub.Flush())
	require.NoError(t, pub.Close())

	reopened, err := NewWithTCP("127.0.0.1:0", sk, PubConfig{DataDir: dir, BatchWindow: 5 * time.Millisecond})
	require.NoError(t, err)
	defer reopened.Close() //nolint:errcheck
	want := map[string][]byte{
		path(1, 0): body(1, 0), path(1, 1): body(1, 1),
		path(2, 0): changed, path(2, 1): body(2, 1),
		path(3, 0): body(3, 0),
	}
	got := map[string][]byte{}
	reopened.Walk("metrics/day", func(p string, v []byte) bool { got[p] = v; return true })
	require.Equal(t, want, got)
}
