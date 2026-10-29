package treestore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestSubscriberCatchesUpPastSweptRoots keeps a single Root on the publisher
// and publishes faster than the subscriber fills, so the subscriber keeps
// asking for objects of Roots the publisher has already swept. The publisher
// must refuse those requests at once so the subscriber moves on to the newer
// Root. Parking them until the response timeout left the subscriber on a
// stale Root, the TestCXOChurn_Sustained flake.
func TestSubscriberCatchesUpPastSweptRoots(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	pub, err := NewWithTCP("127.0.0.1:0", sk, PubConfig{
		InMemoryDB:  true,
		BatchWindow: 5 * time.Millisecond,
		KeepRoots:   1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	cs := newChurnSub()
	sub, err := NewSubscriberTCP("", pk, SubConfig{InMemoryDB: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck
	sub.OnUpdate(cs.apply)

	const live = 40
	for i := 0; i < live; i++ {
		require.NoError(t, pub.Put(fmt.Sprintf("tp/%05d", i), []byte("seed")))
	}
	require.NoError(t, pub.Flush())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
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

func assertEventually(cond func() bool, timeout time.Duration) bool {
	for end := time.Now().Add(timeout); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return cond()
}

func head(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
