// Package pairing — cmd/apps/skychat/pairing/delete_test.go
//
// Delete-for-everyone on the pair feed. A pair message had no identifier
// either side could name, so a retraction was impossible over CXO: the id is
// now derived from the timestamp sealed into the body, which both ends see,
// and a retraction is a control record published onto the same feed.
package pairing

import (
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// MsgID must be derived purely from fields that travel in the sealed body —
// that identity is the whole basis for naming a message across the two
// visors, so it cannot depend on anything local to one side.
func TestMessageMsgIDIsDerivedFromTheSealedBody(t *testing.T) {
	ts := time.Date(2026, 8, 3, 12, 0, 0, 123456789, time.UTC)
	msg := Message{Text: "hi", TS: ts, Seq: 7}
	require.Equal(t, strconv.FormatInt(ts.UnixNano(), 10)+"-7", msg.MsgID())

	// Round-tripping through the wire form preserves it: the receiver derives
	// the same id the sender minted.
	raw, err := json.Marshal(msg)
	require.NoError(t, err)
	var decoded Message
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, msg.MsgID(), decoded.MsgID())
}

// Two messages that land on the same nanosecond must still get distinct ids —
// the seq that already disambiguates their leaf paths does the same for their
// ids, so a delete can't take both.
func TestMessageMsgIDDisambiguatesSameNanosecond(t *testing.T) {
	ts := time.Date(2026, 8, 3, 12, 0, 0, 5, time.UTC)
	a := Message{Text: "first", TS: ts, Seq: 1}
	b := Message{Text: "second", TS: ts, Seq: 2}
	require.NotEqual(t, a.MsgID(), b.MsgID())
}

// A chat message adds no delete-specific fields on the wire, so an older
// subscriber sees the same record shape it always did. (seq is additive and
// omitted at zero; an older peer ignores it.)
func TestChatMessageWireFormAddsNoControlFields(t *testing.T) {
	ts := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	raw, err := json.Marshal(Message{Text: "hello", TS: ts})
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"type"`)
	require.NotContains(t, string(raw), `"id"`)
}

func TestSendDeleteRejectsEmptyID(t *testing.T) {
	require.Error(t, (&Pair{}).SendDelete(""))
}

// deleteRecorder captures the full Message (not just its text, as testInbox
// does) so a retraction's Type and ID are observable.
type deleteRecorder struct {
	mu   sync.Mutex
	msgs []Message
}

func (r *deleteRecorder) deliver(_ cipher.PubKey, msg Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, msg)
}

func (r *deleteRecorder) snapshot() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Message, len(r.msgs))
	copy(out, r.msgs)
	return out
}

func (r *deleteRecorder) waitFor(timeout time.Duration, pred func([]Message) bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pred(r.snapshot()) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// The end-to-end shape of a CXO delete: A sends, learns the id, retracts it,
// and B receives a record naming exactly the message it received earlier.
func TestPairSendDeleteRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; skipped under -short")
	}
	rig := newDMRig(t)

	recB := &deleteRecorder{}
	rig.mgrB.SetMessageHandler(recB.deliver)

	id, err := rig.pairA.SendID("delete me")
	require.NoError(t, err)
	require.NotEmpty(t, id)

	require.True(t, recB.waitFor(30*time.Second, func(msgs []Message) bool {
		for _, m := range msgs {
			if m.Text == "delete me" {
				return true
			}
		}
		return false
	}), "B never received the message")

	// B derives the same id from the record it got — without that the
	// retraction below would name nothing.
	var received Message
	for _, m := range recB.snapshot() {
		if m.Text == "delete me" {
			received = m
		}
	}
	require.Equal(t, id, received.MsgID(),
		"the id A minted and the id B derives must match")

	require.NoError(t, rig.pairA.SendDelete(id))

	require.True(t, recB.waitFor(30*time.Second, func(msgs []Message) bool {
		for _, m := range msgs {
			if m.Type == MessageTypeDelete && m.ID == id {
				return true
			}
		}
		return false
	}), "B never received the retraction for %s", id)
}
