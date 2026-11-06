package group

import (
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
