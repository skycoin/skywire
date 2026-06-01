// Package treestore — tcp_pubsub_test.go: in-process guard that a CXO
// feed syncs publisher→subscriber over the node's NATIVE TCP transport
// with no dmsg / discovery. This is the foundation for the CXO-backed
// standalone skychat (docs/skychat_cxo_tcp_standalone.md): publisher A
// listens on an ephemeral TCP port; subscriber B dials it by address
// (ConnectTCP) and must receive every message A publishes.
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

func TestCXOTCPPubSub(t *testing.T) {
	pkA, skA := cipher.GenerateKeyPair()

	// Publisher A on an ephemeral localhost TCP port — no dmsg.
	pub, err := NewWithTCP("127.0.0.1:0", skA, PubConfig{
		InMemoryDB:  true,
		BatchWindow: 5 * time.Millisecond,
	})
	require.NoError(t, err, "NewWithTCP publisher")
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	addr := pub.Node().TCP().Address()
	require.NotEmpty(t, addr, "publisher TCP listen address")

	// Subscriber B (dial-only node) connects to A by address.
	sub, err := NewSubscriberTCP("", pkA, SubConfig{InMemoryDB: true})
	require.NoError(t, err, "NewSubscriberTCP")
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck

	var (
		mu  sync.Mutex
		rcv [][]byte
	)
	sub.OnUpdate(func(events []UpdateEvent) {
		mu.Lock()
		defer mu.Unlock()
		for _, ev := range events {
			if ev.Value == nil {
				continue
			}
			buf := make([]byte, len(ev.Value))
			copy(buf, ev.Value)
			rcv = append(rcv, buf)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, sub.ConnectTCP(ctx, addr), "ConnectTCP")

	const n = 5
	for i := 0; i < n; i++ {
		require.NoErrorf(t,
			pub.Put(fmt.Sprintf("msgs/%05d", i), []byte(fmt.Sprintf("hello-%d", i))),
			"publisher.Put %d", i)
	}

	// Poll for receipt — bounded so a fast machine doesn't sleep for
	// nothing and a slow one doesn't false-fail.
	end := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		got := len(rcv)
		mu.Unlock()
		if got >= n {
			return // success: all messages synced over CXO-TCP
		}
		if time.Now().After(end) {
			t.Fatalf("subscriber received %d/%d messages over CXO-TCP (addr=%s)", got, n, addr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
