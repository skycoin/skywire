package treestore

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestSubscriberCatchesUpWhilePublisherFills is TestSubscriberCatchesUpPastSweptRoots
// on a publisher that also fills another feed, like the TPD aggregator. Its
// own fills must not park a request for an object of its own swept Root.
func TestSubscriberCatchesUpWhilePublisherFills(t *testing.T) {
	otherPK, otherSK := cipher.GenerateKeyPair()
	other, err := NewWithTCP("127.0.0.1:0", otherSK, PubConfig{
		InMemoryDB:  true,
		BatchWindow: 5 * time.Millisecond,
		KeepRoots:   1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = other.Close() }) //nolint:errcheck

	pk, sk := cipher.GenerateKeyPair()
	pub, err := NewWithTCP("127.0.0.1:0", sk, PubConfig{
		InMemoryDB:  true,
		BatchWindow: 5 * time.Millisecond,
		KeepRoots:   1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	const live = 40
	for i := 0; i < live; i++ {
		require.NoError(t, other.Put(fmt.Sprintf("o/%05d", i), []byte("seed")))
		require.NoError(t, pub.Put(fmt.Sprintf("tp/%05d", i), []byte("seed")))
	}
	require.NoError(t, other.Flush())
	require.NoError(t, pub.Flush())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fill, err := NewSubscriberOnNode(pub.Node(), otherPK, SubConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = fill.Close() }) //nolint:errcheck
	require.NoError(t, fill.ConnectAndWaitForRootTCP(ctx, other.Node().TCP().Address()))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := live; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = other.Put(fmt.Sprintf("o/%05d", i), []byte("live")) //nolint:errcheck
			_ = other.Delete(fmt.Sprintf("o/%05d", i-live))         //nolint:errcheck
			time.Sleep(time.Millisecond)
		}
	}()
	t.Cleanup(func() { close(stop); wg.Wait() })

	cs := newChurnSub()
	sub, err := NewSubscriberTCP("", pk, SubConfig{InMemoryDB: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck
	sub.OnUpdate(cs.apply)
	require.NoError(t, sub.ConnectAndWaitForRootTCP(ctx, pub.Node().TCP().Address()))

	next, oldest := live, 0
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); {
		require.NoError(t, pub.Put(fmt.Sprintf("tp/%05d", next), []byte("live")))
		require.NoError(t, pub.Delete(fmt.Sprintf("tp/%05d", oldest)))
		next++
		oldest++
		time.Sleep(2 * time.Millisecond)
	}
	require.NoError(t, pub.Flush())

	want := make(map[string]struct{}, live)
	for i := oldest; i < next; i++ {
		want[fmt.Sprintf("tp/%05d", i)] = struct{}{}
	}
	converged := func() bool {
		missing, extra := cs.diff(want)
		return len(missing) == 0 && len(extra) == 0
	}
	if !assertEventually(converged, 20*time.Second) {
		missing, extra := cs.diff(want)
		t.Fatalf("subscriber stuck on a stale Root: %d missing, %d extra (first missing %v)",
			len(missing), len(extra), head(missing, 3))
	}
}
