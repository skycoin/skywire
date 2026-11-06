package treestore

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func treeOfLeaves(t *testing.T, leaves map[string]string) *memNode {
	t.Helper()
	root := newMemNode()
	for path, value := range leaves {
		segs, err := SplitPath(path)
		require.NoError(t, err)
		require.NoError(t, putAt(root, segs, []byte(value)))
	}
	return root
}

func clearMarks(n *memNode) {
	n.mutSeq = 0
	for _, sub := range n.subs {
		clearMarks(sub)
	}
}

func leavesOf(root *memNode) map[string]string {
	out := map[string]string{}
	walkLeaves(root, "", func(path string, value []byte) bool {
		out[path] = string(value)
		return true
	}, nil)
	return out
}

// A subtree nobody touched since hydration takes the newer tree exactly, so a
// post unsent after the backup stays gone and a changed value wins.
func TestMergeTakesAnUntouchedSubtreeExactly(t *testing.T) {
	dst := treeOfLeaves(t, map[string]string{"msgs/a": "old", "msgs/unsent": "x", "meta/name": "old"})
	clearMarks(dst)
	dst.subs["msgs"].cached = true
	src := treeOfLeaves(t, map[string]string{"msgs/a": "old", "msgs/b": "new", "meta/name": "new"})

	got := map[string]string{}
	mergeMemNode(dst, src, "", treeMutSeq.Add(1), func(path string, value []byte) { got[path] = string(value) })

	require.Equal(t, map[string]string{"msgs/a": "old", "msgs/b": "new", "meta/name": "new"}, leavesOf(dst))
	require.Equal(t, map[string]string{"msgs/b": "new"}, got, "only a leaf this tree lacked is news")
	require.False(t, dst.subs["msgs"].cached, "a merged node must encode again")
}

// A subtree changed here keeps what this visor wrote since it came back, and
// gains what the newer tree has on top.
func TestMergeKeepsWhatWasWrittenHere(t *testing.T) {
	dst := treeOfLeaves(t, map[string]string{"msgs/alone": "mine", "roster/1": "mine"})
	src := treeOfLeaves(t, map[string]string{"msgs/between": "theirs", "roster/1": "theirs", "keys/1": "theirs"})

	got := map[string]string{}
	mergeMemNode(dst, src, "", treeMutSeq.Add(1), func(path string, value []byte) { got[path] = string(value) })

	require.Equal(t, map[string]string{
		"msgs/alone": "mine", "msgs/between": "theirs", "roster/1": "mine", "keys/1": "theirs",
	}, leavesOf(dst))
	require.Equal(t, map[string]string{"msgs/between": "theirs", "keys/1": "theirs"}, got)
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	require.NoError(t, os.CopyFS(dst, os.DirFS(src)))
}

// A publisher restored from an older copy of its store gets back what it
// published since, from a subscriber, and its next Root keeps all of it.
func TestRestoredPublisherRecoversFromSubscriber(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	dir := filepath.Join(t.TempDir(), "pub")
	open := func() *Publisher {
		p, err := NewWithTCP("127.0.0.1:0", sk, PubConfig{DataDir: dir, BatchWindow: 5 * time.Millisecond})
		require.NoError(t, err)
		return p
	}

	sub, err := NewSubscriberTCP("", pk, SubConfig{InMemoryDB: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck
	connect := func(p *Publisher) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, sub.ConnectTCP(ctx, p.Node().TCP().Address()))
	}
	holds := func(path string) func() bool {
		return func() bool {
			_, ok := sub.Get(path)
			return ok
		}
	}

	pub := open()
	connect(pub)
	require.NoError(t, pub.Put("msgs/before", []byte("1")))
	require.Eventually(t, holds("msgs/before"), 10*time.Second, 10*time.Millisecond)
	require.NoError(t, pub.Close())

	backup := filepath.Join(t.TempDir(), "backup")
	copyDir(t, dir, backup)

	pub = open()
	connect(pub)
	require.NoError(t, pub.Put("msgs/between", []byte("2")))
	require.Eventually(t, holds("msgs/between"), 10*time.Second, 10*time.Millisecond)
	require.NoError(t, pub.Close())

	require.NoError(t, os.RemoveAll(dir))
	copyDir(t, backup, dir)

	pub = open()
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
	var (
		mu        sync.Mutex
		recovered []string
	)
	pub.OnRecovered(func(events []UpdateEvent) {
		mu.Lock()
		defer mu.Unlock()
		for _, ev := range events {
			recovered = append(recovered, ev.Path)
		}
	})
	_, had := pub.Get("msgs/between")
	require.False(t, had, "the restored publisher must start from the older store")

	connect(pub)
	require.Eventually(t, func() bool {
		_, ok := pub.Get("msgs/between")
		return ok
	}, 20*time.Second, 20*time.Millisecond, "the publisher never took the newer Root back")
	mu.Lock()
	require.Equal(t, []string{"msgs/between"}, recovered)
	mu.Unlock()

	require.NoError(t, pub.Put("msgs/after", []byte("3")))
	require.Eventually(t, holds("msgs/after"), 10*time.Second, 10*time.Millisecond)
	for _, path := range []string{"msgs/before", "msgs/between"} {
		require.True(t, holds(path)(), "the next Root dropped %s", path)
	}
}
