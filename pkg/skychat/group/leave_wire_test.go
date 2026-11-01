package group

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// driveLeave runs handleRelay on a pipe whose far end is remote, writes
// msg, and returns the answer, if any.
func driveLeave(t *testing.T, s *Session, remote net.Addr, msg LeaveMsg) (LeaveAck, bool) {
	t.Helper()
	member, admin := net.Pipe()
	defer member.Close() //nolint:errcheck
	defer admin.Close()  //nolint:errcheck
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.handleRelay(fakeAddrConn{Conn: admin, addr: remote})
	}()
	body, err := json.Marshal(msg)
	require.NoError(t, err)
	require.NoError(t, message.WriteFrame(member, body))
	_ = member.SetReadDeadline(time.Now().Add(250 * time.Millisecond)) //nolint:errcheck
	var ack LeaveAck
	raw, err := message.ReadFrame(member)
	got := err == nil && json.Unmarshal(raw, &ack) == nil
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleRelay did not return")
	}
	return ack, got
}

// TestLeaveFrameRemovesTheAuthenticatedAsker: the leave is for the
// transport's key, and a frame for another group or without one is ignored.
func TestLeaveFrameRemovesTheAuthenticatedAsker(t *testing.T) {
	gid := uuid.NewString()
	s := &Session{cfg: Config{Record: Record{ID: gid}}, log: logging.MustGetLogger("group.leave-test")}
	var asked []cipher.PubKey
	s.SetLeaveHandler(func(pk cipher.PubKey) bool { asked = append(asked, pk); return true })
	member, _ := cipher.GenerateKeyPair()

	ack, got := driveLeave(t, s, dmsg.Addr{PK: member, Port: 1}, LeaveMsg{Kind: frameKindLeave, GroupID: gid})
	require.True(t, got, "the member got no answer")
	require.True(t, ack.Removed)
	require.Equal(t, []cipher.PubKey{member}, asked)

	_, got = driveLeave(t, s, dmsg.Addr{PK: member, Port: 1}, LeaveMsg{Kind: frameKindLeave, GroupID: uuid.NewString()})
	require.False(t, got, "answered a leave for another group")
	_, got = driveLeave(t, s, plainAddr{}, LeaveMsg{Kind: frameKindLeave, GroupID: gid})
	require.False(t, got, "answered an unauthenticated leave")
	require.Len(t, asked, 1)
}

// TestLeaveTakesTheMemberOffTheAdminsRoster: the founder stops listing a
// member as soon as the member leaves.
func TestLeaveTakesTheMemberOffTheAdminsRoster(t *testing.T) {
	a, b := newGroupEnv(t)
	rec, err := a.mgr.Create("open room", KindPublic, nil)
	require.NoError(t, err)
	_, err = b.mgr.Join(inviteFor(t, a, rec.ID))
	require.NoError(t, err)
	joined, _, err := a.store.Get(rec.ID)
	require.NoError(t, err)
	require.Contains(t, joined.Members, b.pk)

	require.NoError(t, b.mgr.Leave(rec.ID))
	require.Eventually(t, func() bool {
		r, _, err := a.store.Get(rec.ID)
		return err == nil && !containsPK(r.Members, b.pk)
	}, 20*time.Second, 100*time.Millisecond, "the founder still lists a member that left")
}
