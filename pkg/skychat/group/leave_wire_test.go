package group

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
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

	// Over a route the asker is named by a routing.Addr.
	ack, got = driveLeave(t, s, routing.Addr{PubKey: member, Port: 1}, LeaveMsg{Kind: frameKindLeave, GroupID: gid})
	require.True(t, got, "a member leaving over a route got no answer")
	require.True(t, ack.Removed)
	require.Equal(t, []cipher.PubKey{member, member}, asked)

	_, got = driveLeave(t, s, dmsg.Addr{PK: member, Port: 1}, LeaveMsg{Kind: frameKindLeave, GroupID: uuid.NewString()})
	require.False(t, got, "answered a leave for another group")
	_, got = driveLeave(t, s, plainAddr{}, LeaveMsg{Kind: frameKindLeave, GroupID: gid})
	require.False(t, got, "answered an unauthenticated leave")
	require.Len(t, asked, 2)
}

// deadRouteNetworker reaches a listener that reads a frame and hangs up, as a
// half-open route or a refusing admin does.
type deadRouteNetworker struct{ appnet.Networker }

func (deadRouteNetworker) DialContext(context.Context, appnet.Addr) (net.Conn, error) {
	near, far := net.Pipe()
	go func() {
		_, _ = message.ReadFrame(far) //nolint:errcheck
		_ = far.Close()               //nolint:errcheck
	}()
	return near, nil
}

func (deadRouteNetworker) ListenContext(context.Context, appnet.Addr) (net.Listener, error) {
	return nil, errors.New("no listener in this test")
}

// TestLeaveRetriesOverDmsgAfterADeadRoute: a route that takes the frame and
// gives no answer must not lose the leave, as join does not lose a request.
func TestLeaveRetriesOverDmsgAfterADeadRoute(t *testing.T) {
	a, b := newGroupEnv(t)
	rec, err := a.mgr.Create("open room", KindPublic, nil)
	require.NoError(t, err)
	_, err = b.mgr.Join(inviteFor(t, a, rec.ID))
	require.NoError(t, err)

	require.NoError(t, appnet.AddNetworker(appnet.TypeSkynet, deadRouteNetworker{}))
	t.Cleanup(appnet.ClearNetworkers)

	require.NoError(t, b.mgr.Leave(rec.ID))
	require.Eventually(t, func() bool {
		r, _, err := a.store.Get(rec.ID)
		return err == nil && !containsPK(r.Members, b.pk)
	}, 30*time.Second, 100*time.Millisecond, "the founder still lists a member that left")
}

// TestLateLeaveKeepsAMemberAddedBack: a leave sent before the member was added
// back does not remove it again, and a leave sent after that still does.
func TestLateLeaveKeepsAMemberAddedBack(t *testing.T) {
	gid := uuid.NewString()
	member, _ := cipher.GenerateKeyPair()
	addedBack := time.Now().UTC()
	s := &Session{
		cfg:          Config{Record: Record{ID: gid}},
		log:          logging.MustGetLogger("group.leave-test"),
		mutationSeen: map[string]time.Time{watermarkKey(familyRoster, member): addedBack},
	}
	asked := 0
	s.SetLeaveHandler(func(cipher.PubKey) bool { asked++; return true })
	from := dmsg.Addr{PK: member, Port: 1}

	ack, got := driveLeave(t, s, from, LeaveMsg{Kind: frameKindLeave, GroupID: gid, TS: addedBack.Add(-time.Second)})
	require.True(t, got, "the member got no answer")
	require.True(t, ack.Removed, "a late leave must count as done, or the member asks the next admin")
	require.Zero(t, asked, "a late leave removed a member that was added back")

	ack, got = driveLeave(t, s, from, LeaveMsg{Kind: frameKindLeave, GroupID: gid, TS: addedBack.Add(time.Second)})
	require.True(t, got, "the member got no answer")
	require.True(t, ack.Removed)
	require.Equal(t, 1, asked, "a leave sent after the add was ignored")
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
