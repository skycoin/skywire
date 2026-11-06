package group

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
)

// A restored owner holds no copy of a post it made after the backup, so that
// post must reach its handler. A normal echo of its own post must not.
func TestOnlyARecoveredOwnPostReachesTheHandler(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	msg := Message{SenderPK: pk, TS: time.Now().UTC(), Text: "after the backup"}
	require.NoError(t, SignMessage(&msg, sk))
	leaf, err := json.Marshal(msg)
	require.NoError(t, err)
	events := []treestore.UpdateEvent{{
		Path:  fmt.Sprintf("%s/%s/%d/0", MessagePathPrefix, pk.Hex(), msg.TS.UnixNano()),
		Value: leaf,
	}}
	session := func() (*Session, *msgInbox) {
		rec := Record{ID: uuid.NewString(), OwnerPK: pk, Members: []cipher.PubKey{pk}}
		s, _ := reconcileSession(t, pk, sk, rec)
		box := &msgInbox{}
		s.handler = box.deliver
		return s, box
	}

	s, box := session()
	s.onUpdate(events)
	require.Empty(t, box.snapshot(), "an own post came back as an echo")

	s, box = session()
	s.onRecovered(events)
	require.True(t, hasText("after the backup")(box.snapshot()), "the recovered post was not delivered")
}

// The session's own heartbeat is a post on the feed, so it must wait for a
// connection too, or a lone owner climbs past its members' Root seq.
func TestOwnerHeartbeatWaitsForAConnection(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	pub, err := treestore.NewWithTCP("127.0.0.1:0", sk, treestore.PubConfig{InMemoryDB: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
	s, _ := reconcileSession(t, pk, sk, Record{ID: uuid.NewString(), OwnerPK: pk, Members: []cipher.PubKey{pk}})
	s.pub = pub

	leaves := func() int {
		n := 0
		pub.Walk(MessagePathPrefix, func(string, []byte) bool { n++; return true })
		return n
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.heartbeatWG.Add(1)
	go s.runHeartbeatLoop(ctx, 5*time.Millisecond)
	t.Cleanup(func() { cancel(); s.heartbeatWG.Wait() })

	time.Sleep(100 * time.Millisecond)
	require.Zero(t, leaves(), "a heartbeat went out with nobody connected")

	sub, err := treestore.NewSubscriberTCP("", pk, treestore.SubConfig{InMemoryDB: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck
	require.NoError(t, sub.ConnectTCP(ctx, pub.Node().TCP().Address()))
	require.Eventually(t, func() bool { return leaves() > 0 }, 5*time.Second, 10*time.Millisecond,
		"no heartbeat with a peer connected")
}
