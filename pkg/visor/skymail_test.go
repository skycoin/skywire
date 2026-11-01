package visor

import (
	"context"
	"errors"
	"net"
	"net/rpc"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/skymailbridge"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestSkymailDefaults(t *testing.T) {
	conf := &visorconfig.V1{LocalPath: "/opt/skywire/local"}
	require.True(t, skymailEnabled(conf), "no section: on, with the default limits")
	require.Equal(t, filepath.FromSlash("/opt/skywire/mail"), skymailDir(conf), "beside local/, which the wasm visor never persists")
	dir, err := filepath.Abs(filepath.FromSlash("/opt/skywire/mail"))
	require.NoError(t, err)
	require.Equal(t, []string{"--dir", dir}, skymailArgs(conf))

	conf.Skymail = &visorconfig.SkymailConfig{Enable: true, Dir: "/srv/mail", MaxMessageSize: 4096, MaxTotalSize: -1, MaxAge: visorconfig.Duration(time.Hour)}
	require.True(t, skymailEnabled(conf))
	require.Equal(t, "/srv/mail", skymailDir(conf))
	dir, err = filepath.Abs("/srv/mail")
	require.NoError(t, err)
	require.Equal(t, []string{"--dir", dir, "--max-message-size", "4096", "--max-total-size", "none", "--max-age", "1h0m0s"}, skymailArgs(conf),
		"the config section is the app's baseline")
	conf.Skymail.Enable = false
	require.False(t, skymailEnabled(conf))
}

// TestMailHostServesOnlySkymail: only the mailbox app may open the visor's
// mail with its key.
func TestMailHostServesOnlySkymail(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: pk, SK: sk}}}
	require.Nil(t, v.appMailHost("skychat"))
	h := v.appMailHost(skymailApp)
	require.NotNil(t, h)

	other, otherSK := cipher.GenerateKeyPair()
	env, err := skymail.Seal([]byte("Subject: x\r\n\r\nhi\r\n"), otherSK, pk)
	require.NoError(t, err)
	msg, signer, err := h.MailOpen(env)
	require.NoError(t, err)
	require.Equal(t, other, signer)
	require.Contains(t, string(msg), "hi")

	env, err = h.MailSeal([]byte("Subject: y\r\n\r\nback\r\n"), other)
	require.NoError(t, err)
	_, signer, err = skymail.OpenSealed(env, otherSK)
	require.NoError(t, err)
	require.Equal(t, pk, signer, "sealed as the visor")
}

// mailVisor is a visor whose skymail app runs on c, the way the launcher
// would run it, with the visor's MailHost as its key and peers. hvs are its
// hypervisors, set before the mailbox starts reading them.
func mailVisor(t *testing.T, ctx context.Context, c *dmsg.Client, hvs ...cipher.PubKey) *Visor {
	t.Helper()
	v := &Visor{
		ctx: ctx,
		conf: &visorconfig.V1{
			Common:    &visorconfig.Common{PK: c.LocalPK(), SK: c.LocalSK()},
			LocalPath: filepath.Join(t.TempDir(), "local"),
			Skymail:   &visorconfig.SkymailConfig{Enable: true},
		},
		forwardedPorts: NewForwardedPorts(""),
		initLock:       new(sync.RWMutex),
	}
	v.conf.Hypervisors = hvs
	host := v.appMailHost(skymailApp)
	svc, err := skymail.StartService(ctx, skymail.ServiceConfig{
		Host: skymail.Host{
			PK: c.LocalPK(),
			Listen: func(network string) (net.Listener, error) {
				if network != skymail.NetDmsg {
					return nil, errors.New("no skynet here")
				}
				return c.Listen(skymail.Port)
			},
			Dialers: map[string]skymailbridge.Dialer{".dmsg": &visorDmsgDialer{c: c}},
			PeerPK: func(conn net.Conn) (cipher.PubKey, bool) {
				return remotePK(conn.RemoteAddr())
			},
			Sealer: hostSealer{host},
			Peers: func() ([]cipher.PubKey, []cipher.PubKey) {
				info := host.MailHostInfo()
				return info.Managed, info.Hypervisors
			},
			Log: logrus.New(),
		},
		Dir:    skymailDir(v.conf),
		Outbox: true,
	})
	require.NoError(t, err)
	t.Cleanup(svc.Close)
	v.mailHandler = svc.Handler("")
	return v
}

type hostSealer struct {
	h interface {
		MailSeal([]byte, cipher.PubKey) ([]byte, error)
		MailOpen([]byte) ([]byte, cipher.PubKey, error)
	}
}

func (s hostSealer) Seal(msg []byte, rcpt cipher.PubKey) ([]byte, error) {
	return s.h.MailSeal(msg, rcpt)
}
func (s hostSealer) Open(body []byte) ([]byte, cipher.PubKey, error) { return s.h.MailOpen(body) }

func bobAt(pk cipher.PubKey) string {
	return "bob@" + pk.DNSLabel() + ".dmsg"
}

func mailEnv(t *testing.T, n int) ([]*dmsg.Client, context.Context) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, n, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return env.AllClients(), ctx
}

// TestMailAPIOverTheApp: the visor's mail API reaches the app's control
// surface for every call, and a sealed envelope relayed by a third visor
// opens with the visor's key.
func TestMailAPIOverTheApp(t *testing.T) {
	clients, ctx := mailEnv(t, 3)
	a, b := mailVisor(t, ctx, clients[0]), mailVisor(t, ctx, clients[1])
	pkA, pkB, pkC := clients[0].LocalPK(), clients[1].LocalPK(), clients[2].LocalPK()

	st, err := b.MailStatus()
	require.NoError(t, err)
	require.True(t, st.Running)
	require.Equal(t, "mail@"+pkB.DNSLabel()+".skynet", st.Address)
	require.Equal(t, skymailDir(b.conf), st.Dir)

	res, err := a.MailSend(skymail.Outgoing{From: "alice", To: []string{bobAt(pkB)}, Subject: "over dmsg", Body: "hello"})
	require.NoError(t, err)
	require.Empty(t, res.Recipients[0].Err)
	inbox, err := b.MailList(skymail.FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.Equal(t, pkA.Hex(), inbox[0].PeerPK)
	m, err := b.MailRead(skymail.FolderInbox, inbox[0].ID)
	require.NoError(t, err)
	require.Equal(t, "hello\r\n", m.Text)
	_, err = b.MailRaw(skymail.FolderInbox, "missing")
	require.ErrorIs(t, err, skymail.ErrNotFound)

	from := "alice@" + pkA.DNSLabel() + ".dmsg"
	sealed, err := skymail.Seal([]byte("From: "+from+"\r\nSubject: carried\r\n\r\nsealed for b\r\n"), clients[0].LocalSK(), pkB)
	require.NoError(t, err)
	err = skymailbridge.Relay(ctx, &visorDmsgDialer{c: clients[2]},
		skymailbridge.Config{Suffix: ".dmsg", Mode: "b", HeloName: "c", RemotePort: skymail.Port},
		from, []skymailbridge.Recipient{{Original: bobAt(pkB), Forward: bobAt(pkB), PeerPK: pkB}}, sealed, logrus.New())
	require.NoError(t, err)
	inbox, err = b.MailList(skymail.FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 2)
	require.Equal(t, pkC.Hex(), inbox[0].RelayPK, "opened through the visor's MailHost")

	require.NoError(t, b.MailDelete(skymail.FolderInbox, inbox[0].ID))
	require.NoError(t, b.MailSetWhitelist([]cipher.PubKey{pkC}))
	res, err = a.MailSend(skymail.Outgoing{To: []string{bobAt(pkB)}, Body: "again"})
	require.Error(t, err)
	require.Contains(t, res.Recipients[0].Err, "not whitelisted")
}

func TestMailboxYieldsForwardedPort25(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	v := &Visor{
		conf: &visorconfig.V1{
			Common:    &visorconfig.Common{PK: pk, SK: sk},
			LocalPath: filepath.Join(t.TempDir(), "local"),
			Skymail:   &visorconfig.SkymailConfig{Enable: true},
		},
		forwardedPorts: NewForwardedPorts(""),
		initLock:       new(sync.RWMutex),
	}
	require.False(t, v.appMailHost(skymailApp).MailHostInfo().PortServed)
	require.NoError(t, v.forwardedPorts.Register(visorapi.ForwardedPort{Port: 25, Label: "postfix"}))
	require.True(t, v.appMailHost(skymailApp).MailHostInfo().PortServed, "the app reads this and stays down")
	st, err := v.MailStatus()
	require.NoError(t, err)
	require.False(t, st.Running, "Postfix keeps port 25")
	require.Equal(t, errHostMTA.Error(), st.Reason)
	_, err = v.MailList("")
	require.ErrorIs(t, err, errMailboxOff)
}

// TestMailSendRPCCarriesTheReasons: net/rpc drops the reply of a call
// that returns an error, which once turned "B refused A's PK" into a
// bare "not delivered to any recipient" at the CLI.
func TestMailSendRPCCarriesTheReasons(t *testing.T) {
	clients, ctx := mailEnv(t, 3)
	a, b := mailVisor(t, ctx, clients[0]), mailVisor(t, ctx, clients[1])
	require.NoError(t, b.MailSetWhitelist([]cipher.PubKey{clients[2].LocalPK()}))

	srv := rpc.NewServer()
	require.NoError(t, srv.RegisterName(visorapi.RPCPrefix, &RPC{visor: a, log: logrus.New()}))
	sc, cc := net.Pipe()
	go srv.ServeConn(sc)
	api := visorapi.NewRPCClient(nil, cc, visorapi.RPCPrefix, 30*time.Second)

	res, err := api.MailSend(skymail.Outgoing{To: []string{bobAt(clients[1].LocalPK())}, Body: "x"})
	require.NoError(t, err, "a send that reached nobody still answers")
	require.Len(t, res.Recipients, 1)
	require.Contains(t, res.Recipients[0].Err, "not whitelisted")

	_, err = api.MailSend(skymail.Outgoing{Body: "x"})
	require.Error(t, err, "a send with no result is still an error")
}

// TestMailSettingsApplyLiveAndPersist: new limits and helper lists apply
// at once through the app and are in the file a restart reads, and "off"
// survives a restart too.
func TestMailSettingsApplyLiveAndPersist(t *testing.T) {
	clients, ctx := mailEnv(t, 1)
	hv, _ := cipher.GenerateKeyPair()
	b := mailVisor(t, ctx, clients[0], hv)

	st, err := b.MailStatus()
	require.NoError(t, err)
	require.True(t, st.RelayForDefault)
	require.Equal(t, []cipher.PubKey{hv}, st.RelayFor, "the visor's hypervisors, from its MailHost")

	small, age := int64(2048), time.Hour
	helper, _ := cipher.GenerateKeyPair()
	none := []cipher.PubKey{}
	require.NoError(t, b.MailSetSettings(visorapi.MailSettingsUpdate{MaxMessageSize: &small, MaxAge: &age, Helpers: &[]cipher.PubKey{helper}, RelayFor: &none}))
	st, err = b.MailStatus()
	require.NoError(t, err)
	require.Equal(t, small, st.Limits.MaxMessageSize)
	require.Equal(t, skymail.DefaultMaxTotalSize, st.Limits.MaxTotalSize, "unset fields keep their default")
	require.Equal(t, []cipher.PubKey{helper}, st.Helpers)
	require.Empty(t, st.RelayFor, "an empty list is none, not the default")

	// A restart, or a tab reload that regenerates the config: a config
	// that knows nothing of the change still comes up with it.
	fresh := &visorconfig.V1{Common: &visorconfig.Common{PK: clients[0].LocalPK()}, LocalPath: b.conf.LocalPath}
	enabled, limits, err := skymailEffective(fresh)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, small, limits.MaxMessageSize)
	require.Equal(t, age, limits.MaxAge)

	off := false
	require.NoError(t, b.MailSetSettings(visorapi.MailSettingsUpdate{Enable: &off}))
	enabled, _, err = skymailEffective(fresh)
	require.NoError(t, err)
	require.False(t, enabled, "off survives a restart")
	b.mailHandler = nil // what stopping the app does
	st, err = b.MailStatus()
	require.NoError(t, err)
	require.False(t, st.Running)
	require.Equal(t, "disabled", st.Reason)
}

// TestMailSettingsOverRPC: "off" and "default" are false and 0, which a
// pointer loses in gob; over the real RPC they must still arrive.
func TestMailSettingsOverRPC(t *testing.T) {
	clients, ctx := mailEnv(t, 1)
	b := mailVisor(t, ctx, clients[0])

	srv := rpc.NewServer()
	require.NoError(t, srv.RegisterName(visorapi.RPCPrefix, &RPC{visor: b, log: logrus.New()}))
	sc, cc := net.Pipe()
	go srv.ServeConn(sc)
	api := visorapi.NewRPCClient(nil, cc, visorapi.RPCPrefix, 30*time.Second)

	small, zero, off := int64(4096), int64(0), false
	require.NoError(t, api.MailSetSettings(visorapi.MailSettingsUpdate{MaxMessageSize: &small}))
	st, err := api.MailStatus()
	require.NoError(t, err)
	require.Equal(t, small, st.Limits.MaxMessageSize)

	require.NoError(t, api.MailSetSettings(visorapi.MailSettingsUpdate{MaxMessageSize: &zero}))
	st, err = api.MailStatus()
	require.NoError(t, err)
	require.Equal(t, skymail.DefaultMaxMessageSize, st.Limits.MaxMessageSize, "0 over RPC means default")

	require.NoError(t, api.MailSetSettings(visorapi.MailSettingsUpdate{Enable: &off}))
	enabled, _, err := skymailEffective(b.conf)
	require.NoError(t, err)
	require.False(t, enabled, "false over RPC turns it off")
}

// TestConfiguredHypervisorsWhileUpdated: the mailbox app reads the
// hypervisor list while an operator adds or removes one.
func TestConfiguredHypervisorsWhileUpdated(t *testing.T) {
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{}}}
	hv, _ := cipher.GenerateKeyPair()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = v.configuredHypervisors()
		}
	}()
	for i := 0; i < 100; i++ {
		_ = v.conf.UpdateHypervisors([]cipher.PubKey{hv}) //nolint:errcheck // no file to flush to
	}
	<-done
	require.Equal(t, []cipher.PubKey{hv}, v.configuredHypervisors())
}
