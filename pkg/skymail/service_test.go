package skymail

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/skymailbridge"
)

type dmsgDialer struct{ c *dmsg.Client }

func (d dmsgDialer) Dial(ctx context.Context, peer cipher.PubKey, port uint16) (net.Conn, error) {
	return d.c.Dial(ctx, dmsg.Addr{PK: peer, Port: port})
}

// noSkynet stands in for a visor with no route to anyone.
type noSkynet struct{}

func (noSkynet) Dial(context.Context, cipher.PubKey, uint16) (net.Conn, error) {
	return nil, errors.New("no skynet here")
}

// node is one visor's mailbox app, whose only network is dmsg.
type node struct {
	t     *testing.T
	ctx   context.Context
	cfg   ServiceConfig
	svc   *Service
	ctl   Control
	peers *pkSet
	hvs   *pkSet
}

func newNode(t *testing.T, ctx context.Context, c *dmsg.Client) *node {
	t.Helper()
	n := &node{t: t, ctx: ctx, peers: &pkSet{}, hvs: &pkSet{}}
	n.cfg = ServiceConfig{
		Host: Host{
			PK: c.LocalPK(),
			Listen: func(network string) (net.Listener, error) {
				if network != NetDmsg {
					return nil, errors.New("no skynet here")
				}
				return c.Listen(Port)
			},
			Dialers: map[string]skymailbridge.Dialer{".dmsg": dmsgDialer{c}, ".skynet": noSkynet{}},
			PeerPK: func(conn net.Conn) (cipher.PubKey, bool) {
				a, ok := conn.RemoteAddr().(dmsg.Addr)
				return a.PK, ok
			},
			Sealer: KeySealer(c.LocalSK()),
			Peers:  func() ([]cipher.PubKey, []cipher.PubKey) { return n.peers.list(), n.hvs.list() },
			Log:    logrus.New(),
		},
		Dir:    t.TempDir(),
		Outbox: true,
	}
	n.start()
	t.Cleanup(n.stop)
	return n
}

func (n *node) start() {
	svc, err := StartService(n.ctx, n.cfg)
	require.NoError(n.t, err)
	n.svc = svc
	h := svc.Handler("")
	n.ctl = Control{Do: func(req *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Result(), nil
	}}
}

func (n *node) stop() {
	if n.svc != nil {
		n.svc.Close()
		n.svc = nil
	}
}

func (n *node) settings(f func(*Settings)) {
	s, err := LoadSettings(n.cfg.Dir)
	require.NoError(n.t, err)
	f(&s)
	require.NoError(n.t, s.Save(n.cfg.Dir))
	require.NoError(n.t, n.ctl.Reload())
}

func dmsgEnv(t *testing.T, clients int) ([]*dmsg.Client, context.Context) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, clients, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return env.AllClients(), ctx
}

func bobAt(pk cipher.PubKey) string { return "bob@" + pk.DNSLabel() + ".dmsg" }

// TestServiceOverRealDmsg sends between two mailboxes over dmsg streams,
// through their control surfaces, so the sender PK the mailbox records is
// the one dmsg authenticated.
func TestServiceOverRealDmsg(t *testing.T) {
	clients, ctx := dmsgEnv(t, 3)
	a, b := newNode(t, ctx, clients[0]), newNode(t, ctx, clients[1])
	pkA, pkB, pkC := clients[0].LocalPK(), clients[1].LocalPK(), clients[2].LocalPK()

	st, err := b.ctl.Status()
	require.NoError(t, err)
	require.Equal(t, "mail@"+pkB.DNSLabel()+".skynet", st.Address)
	require.True(t, st.HasOutbox)
	require.True(t, st.Sealing)

	res, err := a.ctl.Send(Outgoing{From: "alice", To: []string{bobAt(pkB)}, Subject: "over dmsg", Body: "hello"})
	require.NoError(t, err)
	require.Empty(t, res.Recipients[0].Err)
	require.Equal(t, "dmsg", res.Recipients[0].Via)

	inbox, err := b.ctl.List(FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.Equal(t, pkA.Hex(), inbox[0].PeerPK, "dmsg's authenticated remote PK")
	require.True(t, inbox[0].FromVerified)

	st, err = b.ctl.Status()
	require.NoError(t, err)
	require.Equal(t, 1, st.Unread)
	m, err := b.ctl.Read(FolderInbox, inbox[0].ID)
	require.NoError(t, err)
	require.Equal(t, "hello\r\n", m.Text)
	st, err = b.ctl.Status()
	require.NoError(t, err)
	require.Equal(t, 0, st.Unread, "reading marks it seen")
	raw, err := b.ctl.Raw(FolderInbox, inbox[0].ID)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(raw), PeerHeader+": "))

	_, err = b.ctl.Read(FolderInbox, "nope")
	require.ErrorIs(t, err, ErrNotFound, "a missing message stays ErrNotFound across the control surface")

	require.NoError(t, b.ctl.SetWhitelist([]cipher.PubKey{pkC}))
	res, err = a.ctl.Send(Outgoing{To: []string{bobAt(pkB)}, Body: "again"})
	require.Error(t, err)
	require.Contains(t, res.Recipients[0].Err, "not whitelisted", "a refused send still carries its reasons")

	require.NoError(t, b.ctl.Delete(FolderInbox, inbox[0].ID))
	inbox, err = b.ctl.List(FolderInbox)
	require.NoError(t, err)
	require.Empty(t, inbox)
}

// TestServiceOutboxDeliversOnceTheMailboxIsBack: B's app is stopped, so
// A keeps the mail in its Outbox and delivers it when B's app runs again.
func TestServiceOutboxDeliversOnceTheMailboxIsBack(t *testing.T) {
	clients, ctx := dmsgEnv(t, 2)
	a, b := newNode(t, ctx, clients[0]), newNode(t, ctx, clients[1])
	pkB := clients[1].LocalPK()

	b.stop()
	res, err := a.ctl.Send(Outgoing{To: []string{bobAt(pkB)}, Subject: "queued", Body: "x"})
	require.NoError(t, err)
	require.True(t, res.Recipients[0].Queued)
	out, err := a.ctl.List(FolderOutbox)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, []string{bobAt(pkB)}, out[0].Queue.Pending)

	b.start()
	// dmsg refuses a peer for about two seconds after a failed dial.
	require.Eventually(t, func() bool {
		require.NoError(t, a.ctl.Retry(""))
		inbox, err := b.ctl.List(FolderInbox)
		return err == nil && len(inbox) == 1
	}, 20*time.Second, time.Second)
	require.Eventually(t, func() bool {
		st, err := a.ctl.Status()
		return err == nil && st.Outbox == 0
	}, 5*time.Second, 50*time.Millisecond)
}

// TestServiceSealedEnvelopeRelayed: C carries mail A sealed for B over a
// real dmsg stream, and B files it under A with C as the relay.
func TestServiceSealedEnvelopeRelayed(t *testing.T) {
	clients, ctx := dmsgEnv(t, 3)
	b := newNode(t, ctx, clients[1])
	pkA, pkB, pkC := clients[0].LocalPK(), clients[1].LocalPK(), clients[2].LocalPK()
	require.NoError(t, b.ctl.SetWhitelist([]cipher.PubKey{pkA}))

	from := "alice@" + pkA.DNSLabel() + ".dmsg"
	sealed, err := Seal([]byte("From: "+from+"\r\nSubject: carried\r\n\r\nsealed for b\r\n"), clients[0].LocalSK(), pkB)
	require.NoError(t, err)
	err = skymailbridge.Relay(ctx, dmsgDialer{clients[2]},
		skymailbridge.Config{Suffix: ".dmsg", Mode: "b", HeloName: "c", RemotePort: Port},
		from, []skymailbridge.Recipient{{Original: bobAt(pkB), Forward: bobAt(pkB), PeerPK: pkB}}, sealed, logrus.New())
	require.NoError(t, err, "C is not whitelisted, but A, who signed it, is")

	inbox, err := b.ctl.List(FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.Equal(t, pkA.Hex(), inbox[0].PeerPK)
	require.Equal(t, pkC.Hex(), inbox[0].RelayPK)
	require.True(t, inbox[0].FromVerified)
}

// TestServiceSkynetAddressNeverFallsBackToDmsg: dmsg reaches B, but there
// is no skynet here, so B's .skynet address must not quietly use dmsg.
func TestServiceSkynetAddressNeverFallsBackToDmsg(t *testing.T) {
	clients, ctx := dmsgEnv(t, 2)
	a, b := newNode(t, ctx, clients[0]), newNode(t, ctx, clients[1])
	pkB := clients[1].LocalPK()

	res, err := a.ctl.Send(Outgoing{To: []string{"bob@" + pkB.DNSLabel() + ".skynet"}, Body: "x"})
	require.NoError(t, err, "no route yet is a reason to wait, not to give up")
	require.True(t, res.Recipients[0].Queued)
	require.NoError(t, a.ctl.Retry(""))
	time.Sleep(time.Second)
	inbox, err := b.ctl.List(FolderInbox)
	require.NoError(t, err)
	require.Empty(t, inbox, "not delivered over dmsg behind the sender's back")
}

// TestServiceSettingsReload: limits and helper lists in SettingsFile apply
// on Reload, without a restart, and the defaults follow the host's peers.
func TestServiceSettingsReload(t *testing.T) {
	clients, ctx := dmsgEnv(t, 2)
	a, b := newNode(t, ctx, clients[0]), newNode(t, ctx, clients[1])
	hv, _ := cipher.GenerateKeyPair()
	b.hvs.set(hv)

	st, err := b.ctl.Status()
	require.NoError(t, err)
	require.True(t, st.HelpersDefault)
	require.Empty(t, st.Helpers, "manages no visors")
	require.True(t, st.RelayForDefault)
	require.Equal(t, []cipher.PubKey{hv}, st.RelayFor)

	small := int64(2048)
	helper, _ := cipher.GenerateKeyPair()
	b.settings(func(s *Settings) {
		s.MaxMessageSize = &small
		s.Helpers = &[]cipher.PubKey{helper}
		s.RelayFor = &[]cipher.PubKey{}
	})
	st, err = b.ctl.Status()
	require.NoError(t, err)
	require.Equal(t, small, st.Limits.MaxMessageSize)
	require.Equal(t, DefaultMaxTotalSize, st.Limits.MaxTotalSize, "unset fields keep their default")
	require.False(t, st.HelpersDefault)
	require.Equal(t, []cipher.PubKey{helper}, st.Helpers)
	require.False(t, st.RelayForDefault)
	require.Empty(t, st.RelayFor, "an empty list is none, not the default")

	res, _ := a.ctl.Send(Outgoing{To: []string{bobAt(clients[1].LocalPK())}, Body: strings.Repeat("z", 4000)}) //nolint:errcheck
	require.NotNil(t, res)
	require.Contains(t, res.Recipients[0].Err, "552", "the new size limit applies at once")

	b.stop()
	b.start()
	st, err = b.ctl.Status()
	require.NoError(t, err)
	require.Equal(t, small, st.Limits.MaxMessageSize, "and survives a restart")
}

// TestServiceHelperDelivers: A hands its queued mail to H, and H delivers
// it once B's app runs, filed under A with H as the relay.
func TestServiceHelperDelivers(t *testing.T) {
	clients, ctx := dmsgEnv(t, 3)
	a, h, b := newNode(t, ctx, clients[0]), newNode(t, ctx, clients[1]), newNode(t, ctx, clients[2])
	pkA, pkH, pkB := clients[0].LocalPK(), clients[1].LocalPK(), clients[2].LocalPK()
	a.peers.set(pkH) // A is H's hypervisor, so H is A's default helper
	h.hvs.set(pkA)   // and H carries A's mail by default
	b.stop()

	res, err := a.ctl.Send(Outgoing{To: []string{bobAt(pkB)}, Subject: "carried", Body: "x"})
	require.NoError(t, err)
	require.True(t, res.Recipients[0].Queued)
	require.Eventually(t, func() bool {
		out, err := h.ctl.List(FolderOutbox)
		return err == nil && len(out) == 1 && out[0].Queue.CarriedFor == pkA.Hex()
	}, 20*time.Second, 200*time.Millisecond, "H takes A's mail")

	b.start()
	require.Eventually(t, func() bool {
		require.NoError(t, h.ctl.Retry(""))
		inbox, err := b.ctl.List(FolderInbox)
		return err == nil && len(inbox) == 1
	}, 20*time.Second, time.Second, "H delivers")
	inbox, err := b.ctl.List(FolderInbox)
	require.NoError(t, err)
	require.Equal(t, pkA.Hex(), inbox[0].PeerPK)
	require.Equal(t, pkH.Hex(), inbox[0].RelayPK)
}

// TestControlToken: over TCP the control surface wants its token.
func TestControlToken(t *testing.T) {
	clients, ctx := dmsgEnv(t, 1)
	n := newNode(t, ctx, clients[0])
	srv := httptest.NewServer(n.svc.Handler("secret"))
	t.Cleanup(srv.Close)
	call := func(token string) error {
		return Control{Do: func(req *http.Request) (*http.Response, error) {
			req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
			if token != "" {
				req.Header.Set(TokenHeader, token)
			}
			return http.DefaultClient.Do(req)
		}}.Reload()
	}
	require.ErrorContains(t, call(""), "bad control token")
	require.ErrorContains(t, call("wrong"), "bad control token")
	require.NoError(t, call("secret"))
}

// TestSettingsReadTheWasmNumber: the wasm visor wrote max_age as
// nanoseconds; native ones write text. Both read back.
func TestSettingsReadTheWasmNumber(t *testing.T) {
	var s Settings
	require.NoError(t, json.Unmarshal([]byte(`{"max_age":3600000000000}`), &s))
	require.Equal(t, time.Hour, time.Duration(*s.MaxAge))
	require.NoError(t, json.Unmarshal([]byte(`{"max_age":"2h0m0s"}`), &s))
	require.Equal(t, 2*time.Hour, time.Duration(*s.MaxAge))
}
