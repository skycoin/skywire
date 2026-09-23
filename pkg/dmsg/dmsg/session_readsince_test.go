// Package dmsg pkg/dmsg/dmsg/session_readsince_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestSessionReadSince covers the marker the ping reaper now consults: no
// evidence before anything is read, positive evidence after, and no false
// positive for a window that closed before the read.
func TestSessionReadSince(t *testing.T) {
	var sc SessionCommon

	if sc.ReadSince(time.Now().Add(-time.Hour)) {
		t.Fatal("ReadSince true with nothing read")
	}

	before := time.Now()
	time.Sleep(2 * time.Millisecond)
	sc.markRead()
	time.Sleep(2 * time.Millisecond)
	after := time.Now()

	if !sc.ReadSince(before) {
		t.Fatal("ReadSince false for a window containing the read")
	}
	if sc.ReadSince(after) {
		t.Fatal("ReadSince true for a window starting after the read")
	}

	// A nil receiver is the "no session" case the Stream guard leaves open;
	// it must answer "no evidence" rather than panic.
	var nilSC *SessionCommon
	if nilSC.ReadSince(before) {
		t.Fatal("nil session claimed to have relayed")
	}
	nilSC.markRead()
}

// TestDecideReap pins the rule that a failing ping no longer closes a session
// that is still carrying traffic.
//
// The bug it guards: the reaper closed on the ping alone, so a session with a
// live voice call on it — fifty packets a second each way, proof the server
// was relaying — was torn down because a separate, newly opened probe stream
// had missed twice. The call ended 17ms later, and nothing said why.
func TestDecideReap(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fails    int
		relaying bool
		want     reapDecision
	}{
		{"under threshold, silent", pingDeadThreshold - 1, false, reapWait},
		{"under threshold, relaying", pingDeadThreshold - 1, true, reapWait},
		{"at threshold, silent", pingDeadThreshold, false, reapClose},
		{"at threshold, relaying", pingDeadThreshold, true, reapKeep},
		{"well past threshold, relaying", pingDeadThreshold + 5, true, reapKeep},
		{"well past threshold, silent", pingDeadThreshold + 5, false, reapClose},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideReap(tc.fails, tc.relaying); got != tc.want {
				t.Fatalf("decideReap(%d, %v) = %v, want %v", tc.fails, tc.relaying, got, tc.want)
			}
		})
	}
}

// TestSortSessionsPutsFailingServersLast is the consequence of decideReap:
// a session that is no longer closed for failing its pings must instead stop
// being the one new dials pick.
//
// Latency alone gets this backwards. LastPing holds the last SUCCESSFUL
// round-trip, so a server that has stopped answering keeps whatever fast
// number it last posted and sorts ahead of healthy ones — which is how a new
// call would be relayed through the sickest server available.
func TestSortSessionsPutsFailingServersLast(t *testing.T) {
	newSession := func(name string, lastPing time.Duration, fails int32) ClientSession {
		sc := &SessionCommon{}
		sc.rPK = pkNamed(t, name)
		sc.SetLastPing(lastPing)
		sc.pingFails.Store(fails)
		return ClientSession{SessionCommon: sc}
	}

	// The failing one is also the fastest on paper — the case that used to
	// sort it first.
	sessions := []ClientSession{
		newSession("slow-healthy", 500*time.Millisecond, 0),
		newSession("fast-failing", 1*time.Millisecond, pingDeadThreshold),
		newSession("fast-healthy", 10*time.Millisecond, 0),
		newSession("unmeasured-healthy", 0, 0),
		newSession("one-miss", 5*time.Millisecond, pingDeadThreshold-1),
	}
	sortSessionsByLatency(sessions)

	got := make([]string, 0, len(sessions))
	for _, ses := range sessions {
		got = append(got, nameOfPK(t, ses.RemotePK()))
	}
	// Healthy ones by latency, one-miss among them on its 5ms and not
	// demoted for the single miss, and the failing one last despite being the
	// fastest number in the list.
	want := []string{"one-miss", "fast-healthy", "slow-healthy", "unmeasured-healthy", "fast-failing"}
	require.Equal(t, want, got,
		"a session failing its pings must sort last, and a single miss must not demote one")
}

// pkNamed / nameOfPK give the sort test readable identities: the sort only
// moves whole sessions, so any stable per-session marker will do, and the
// remote PK is the one already on the session.
var sortTestKeys = map[string]cipher.PubKey{}

func pkNamed(t *testing.T, name string) cipher.PubKey {
	t.Helper()
	if pk, ok := sortTestKeys[name]; ok {
		return pk
	}
	pk, _ := GenKeyPair(t, name)
	sortTestKeys[name] = pk
	return pk
}

func nameOfPK(t *testing.T, pk cipher.PubKey) string {
	t.Helper()
	for name, known := range sortTestKeys {
		if known == pk {
			return name
		}
	}
	return pk.String()
}

// TestStreamReadMarksSession is the other half: the rule above is only worth
// anything if real traffic actually reaches the marker. A dmsg stream that
// reads bytes must record it on the session that carried them, or the reaper
// sees an idle session and closes it under a live call.
func TestStreamReadMarksSession(t *testing.T) {
	dc := disc.NewMock(0)

	pkSrv, skSrv := GenKeyPair(t, "server")
	srv := NewServer(pkSrv, skSrv, dc, &ServerConfig{MaxSessions: 10, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger("server"))
	lisSrv, err := net.Listen("tcp", "")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lisSrv, "") }() //nolint:errcheck

	pkA, skA := GenKeyPair(t, "client A")
	clientA := NewClient(pkA, skA, dc, DefaultConfig())
	clientA.SetLogger(logging.MustGetLogger("client_A"))
	go clientA.Serve(context.Background())

	pkB, skB := GenKeyPair(t, "client B")
	clientB := NewClient(pkB, skB, dc, DefaultConfig())
	clientB.SetLogger(logging.MustGetLogger("client_B"))
	go clientB.Serve(context.Background())

	t.Cleanup(func() {
		_ = clientA.Close() //nolint:errcheck
		_ = clientB.Close() //nolint:errcheck
		_ = srv.Close()     //nolint:errcheck
	})

	require.Eventually(t, func() bool {
		return clientA.SessionCount() > 0 && clientB.SessionCount() > 0
	}, 10*time.Second, 200*time.Millisecond, "clients failed to connect to the dmsg server")

	const port = uint16(80)
	lis, err := clientB.Listen(port)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lis.Close() }) //nolint:errcheck

	accepted := make(chan net.Conn, 1)
	go func() {
		c, aerr := lis.Accept()
		if aerr == nil {
			accepted <- c
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	strA, err := clientA.DialStream(ctx, Addr{PK: pkB, Port: port})
	require.NoError(t, err)
	t.Cleanup(func() { _ = strA.Close() }) //nolint:errcheck

	var strB net.Conn
	select {
	case strB = <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("stream was never accepted")
	}
	t.Cleanup(func() { _ = strB.Close() }) //nolint:errcheck

	// The window opens AFTER the dial, so the handshake's own reads cannot
	// be what satisfies it — only the payload below can.
	since := time.Now()
	time.Sleep(5 * time.Millisecond)

	_, err = strB.Write([]byte("media"))
	require.NoError(t, err)
	buf := make([]byte, 5)
	require.NoError(t, strA.SetReadDeadline(time.Now().Add(10*time.Second)))
	_, err = strA.Read(buf)
	require.NoError(t, err)

	sessions := clientA.allClientSessions(clientA.porter)
	require.NotEmpty(t, sessions)
	relaying := false
	for _, ses := range sessions {
		if ses.SessionCommon.ReadSince(since) {
			relaying = true
			break
		}
	}
	require.True(t, relaying,
		"a stream that just read bytes did not mark its session; the ping reaper would close it under a live call")
}
