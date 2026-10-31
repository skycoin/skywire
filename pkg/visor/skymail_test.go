package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/skymailbridge"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestSkymailDefaults(t *testing.T) {
	conf := &visorconfig.V1{LocalPath: "/opt/skywire/local"}
	require.True(t, skymailEnabled(conf), "no section: on, with the default limits")
	require.Equal(t, filepath.FromSlash("/opt/skywire/mail"), skymailDir(conf), "beside local/, which the wasm visor never persists")

	conf.Skymail = &visorconfig.SkymailConfig{Enable: true, Dir: "/srv/mail"}
	require.True(t, skymailEnabled(conf))
	require.Equal(t, "/srv/mail", skymailDir(conf))
	conf.Skymail.Enable = false
	require.False(t, skymailEnabled(conf))
}

// mailVisor starts the mailbox of a visor whose only network is c.
func mailVisor(t *testing.T, ctx context.Context, c *dmsg.Client) *Visor {
	t.Helper()
	v := &Visor{
		ctx: ctx,
		conf: &visorconfig.V1{
			Common:    &visorconfig.Common{PK: c.LocalPK(), SK: c.LocalSK()},
			LocalPath: filepath.Join(t.TempDir(), "local"),
			Skymail:   &visorconfig.SkymailConfig{Enable: true},
		},
		dmsgC:          c,
		forwardedPorts: NewForwardedPorts(""),
		initLock:       new(sync.RWMutex),
	}
	require.NoError(t, initSkymail(ctx, v, logging.MustGetLogger("skymail")))
	// The launcher starts the app on a real visor; this one has none.
	require.NoError(t, v.startSkymail())
	return v
}

func bobAt(pk cipher.PubKey) string {
	return "bob@" + pk.DNSLabel() + ".dmsg"
}

// TestMailboxOverRealDmsg sends between two visors over dmsg streams,
// so the sender PK the mailbox records is the one dmsg authenticated.
func TestMailboxOverRealDmsg(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 3, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	clients := env.AllClients()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a, b := mailVisor(t, ctx, clients[0]), mailVisor(t, ctx, clients[1])
	pkA, pkB, pkC := clients[0].LocalPK(), clients[1].LocalPK(), clients[2].LocalPK()

	st, err := b.MailStatus()
	require.NoError(t, err)
	require.True(t, st.Running)
	require.Equal(t, "mail@"+pkB.DNSLabel()+".skynet", st.Address)

	res, err := a.MailSend(skymail.Outgoing{From: "alice", To: []string{bobAt(pkB)}, Subject: "over dmsg", Body: "hello"})
	require.NoError(t, err)
	require.Empty(t, res.Recipients[0].Err)

	inbox, err := b.MailList(skymail.FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.Equal(t, pkA.Hex(), inbox[0].PeerPK, "dmsg's authenticated remote PK")
	require.True(t, inbox[0].FromVerified)

	st, err = b.MailStatus()
	require.NoError(t, err)
	require.Equal(t, 1, st.Unread)
	m, err := b.MailRead(skymail.FolderInbox, inbox[0].ID)
	require.NoError(t, err)
	require.Equal(t, "hello\r\n", m.Text)
	st, err = b.MailStatus()
	require.NoError(t, err)
	require.Equal(t, 0, st.Unread, "reading marks it seen")

	require.NoError(t, b.MailSetWhitelist([]cipher.PubKey{pkC}))
	res, err = a.MailSend(skymail.Outgoing{To: []string{bobAt(pkB)}, Body: "again"})
	require.Error(t, err)
	require.Contains(t, res.Recipients[0].Err, "not whitelisted")
}

// TestOutboxDeliversOverRealDmsgOnceTheMailboxIsBack: B cannot take mail,
// so A keeps it in the Outbox and delivers it over dmsg when B can.
func TestOutboxDeliversOverRealDmsgOnceTheMailboxIsBack(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 2, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	clients := env.AllClients()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a, b := mailVisor(t, ctx, clients[0]), mailVisor(t, ctx, clients[1])

	off, on := false, true
	require.NoError(t, b.MailSetSettings(visorapi.MailSettingsUpdate{Enable: &off}))
	res, err := a.MailSend(skymail.Outgoing{To: []string{bobAt(clients[1].LocalPK())}, Subject: "queued", Body: "x"})
	require.NoError(t, err)
	require.True(t, res.Recipients[0].Queued)
	st, err := a.MailStatus()
	require.NoError(t, err)
	require.True(t, st.HasOutbox)
	require.Equal(t, 1, st.Outbox)
	out, err := a.MailList(skymail.FolderOutbox)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, []string{bobAt(clients[1].LocalPK())}, out[0].Queue.Pending)

	require.NoError(t, b.MailSetSettings(visorapi.MailSettingsUpdate{Enable: &on}))
	// dmsg refuses a peer for about two seconds after a failed dial.
	require.Eventually(t, func() bool {
		require.NoError(t, a.MailRetry(""))
		inbox, err := b.MailList(skymail.FolderInbox)
		return err == nil && len(inbox) == 1
	}, 20*time.Second, time.Second)
	inbox, err := b.MailList(skymail.FolderInbox)
	require.NoError(t, err)
	require.Equal(t, "queued", inbox[0].Subject)
	require.Equal(t, clients[0].LocalPK().Hex(), inbox[0].PeerPK)
	require.Eventually(t, func() bool {
		st, err := a.MailStatus()
		return err == nil && st.Outbox == 0
	}, 5*time.Second, 50*time.Millisecond)
	sent, err := a.MailList(skymail.FolderSent)
	require.NoError(t, err)
	require.Len(t, sent, 1)
}

// TestSealedEnvelopeRelayedOverRealDmsg: C carries mail A sealed for B
// over a real dmsg stream, and B files it under A with C as the relay.
func TestSealedEnvelopeRelayedOverRealDmsg(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 3, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	clients := env.AllClients()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b := mailVisor(t, ctx, clients[1])
	pkA, pkB, pkC := clients[0].LocalPK(), clients[1].LocalPK(), clients[2].LocalPK()
	require.NoError(t, b.MailSetWhitelist([]cipher.PubKey{pkA}))

	from := "alice@" + pkA.DNSLabel() + ".dmsg"
	msg := "From: " + from + "\r\nSubject: carried\r\n\r\nsealed for b\r\n"
	sealed, err := skymail.Seal([]byte(msg), clients[0].LocalSK(), pkB)
	require.NoError(t, err)
	err = skymailbridge.Relay(ctx, &visorDmsgDialer{c: clients[2]},
		skymailbridge.Config{Suffix: ".dmsg", Mode: "b", HeloName: "c", RemotePort: skymailPort},
		from, []skymailbridge.Recipient{{Original: bobAt(pkB), Forward: bobAt(pkB), PeerPK: pkB}}, sealed, logrus.New())
	require.NoError(t, err, "C is not whitelisted, but A, who signed it, is")

	inbox, err := b.MailList(skymail.FolderInbox)
	require.NoError(t, err)
	require.Len(t, inbox, 1)
	require.Equal(t, pkA.Hex(), inbox[0].PeerPK)
	require.Equal(t, pkC.Hex(), inbox[0].RelayPK)
	require.True(t, inbox[0].FromVerified)
	m, err := b.MailRead(skymail.FolderInbox, inbox[0].ID)
	require.NoError(t, err)
	require.Equal(t, "sealed for b\r\n", m.Text)
	require.Equal(t, pkC.Hex(), m.RelayPK)
}

func TestMailboxYieldsForwardedPort25(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 1, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	c := env.AllClients()[0]
	v := &Visor{
		conf: &visorconfig.V1{
			Common:    &visorconfig.Common{PK: c.LocalPK()},
			LocalPath: filepath.Join(t.TempDir(), "local"),
			Skymail:   &visorconfig.SkymailConfig{Enable: true},
		},
		dmsgC:          c,
		forwardedPorts: NewForwardedPorts(""),
		initLock:       new(sync.RWMutex),
	}
	require.NoError(t, v.forwardedPorts.Register(visorapi.ForwardedPort{Port: 25, Label: "postfix"}))
	require.NoError(t, initSkymail(context.Background(), v, logging.MustGetLogger("skymail")))
	require.Error(t, v.startSkymail(), "port 25 belongs to the forwarded MTA")
	st, err := v.MailStatus()
	require.NoError(t, err)
	require.False(t, st.Running, "Postfix keeps port 25")
	_, err = v.MailList("")
	require.ErrorIs(t, err, errMailboxOff)
}

// TestMailSendRPCCarriesTheReasons: net/rpc drops the reply of a call
// that returns an error, which once turned "B refused A's PK" into a
// bare "not delivered to any recipient" at the CLI.
func TestMailSendRPCCarriesTheReasons(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 3, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	clients := env.AllClients()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
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

// TestMailSettingsApplyLiveAndPersist: off, on again (port 25 re-binds
// at once) and new limits, all without a restart, and all in the file.
func TestMailSettingsApplyLiveAndPersist(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 2, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	clients := env.AllClients()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a, b := mailVisor(t, ctx, clients[0]), mailVisor(t, ctx, clients[1])

	off, on := false, true
	require.NoError(t, b.MailSetSettings(visorapi.MailSettingsUpdate{Enable: &off}))
	st, err := b.MailStatus()
	require.NoError(t, err)
	require.False(t, st.Running)
	require.Equal(t, "disabled", st.Reason)
	res, err := a.MailSend(skymail.Outgoing{To: []string{bobAt(clients[1].LocalPK())}, Body: "x"})
	require.NoError(t, err)
	require.True(t, res.Recipients[0].Queued, "nothing listens on 25 while it is off, so it waits")

	small, age := int64(2048), time.Hour
	require.NoError(t, b.MailSetSettings(visorapi.MailSettingsUpdate{Enable: &on, MaxMessageSize: &small, MaxAge: &age}))
	st, err = b.MailStatus()
	require.NoError(t, err)
	require.True(t, st.Running, "port 25 bound again straight away")
	require.Equal(t, small, st.Limits.MaxMessageSize)
	require.Equal(t, skymail.DefaultMaxTotalSize, st.Limits.MaxTotalSize, "unset fields keep their default")

	// The send made while it was off leaves dmsg refusing this peer for
	// about two seconds (error 202), so wait for the answer to come from
	// the mailbox itself.
	require.Eventually(t, func() bool {
		res, _ = a.MailSend(skymail.Outgoing{To: []string{bobAt(clients[1].LocalPK())}, Body: strings.Repeat("z", 4000)}) //nolint:errcheck
		return res != nil && strings.Contains(res.Recipients[0].Err, "552")
	}, 10*time.Second, 200*time.Millisecond, "the new size limit applies at once")
	res, err = a.MailSend(skymail.Outgoing{To: []string{bobAt(clients[1].LocalPK())}, Body: "fits"})
	require.NoError(t, err)
	require.Equal(t, "dmsg", res.Recipients[0].Via)

	// A restart, or a tab reload that regenerates the config: a fresh
	// visor on the same mail directory and a config that knows nothing of
	// the change still comes up with it.
	fresh := &Visor{
		conf: &visorconfig.V1{
			Common:    &visorconfig.Common{PK: clients[1].LocalPK()},
			LocalPath: b.conf.LocalPath,
		},
		initLock: new(sync.RWMutex),
	}
	enabled, limits, err := skymailEffective(fresh.conf)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, small, limits.MaxMessageSize)
	require.Equal(t, age, limits.MaxAge)

	require.NoError(t, b.MailSetSettings(visorapi.MailSettingsUpdate{Enable: &off}))
	enabled, _, err = skymailEffective(fresh.conf)
	require.NoError(t, err)
	require.False(t, enabled, "off survives a restart too")
}

// TestMailSettingsOverRPC: "off" and "default" are false and 0, which a
// pointer loses in gob; over the real RPC they must still arrive.
func TestMailSettingsOverRPC(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 1, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b := mailVisor(t, ctx, env.AllClients()[0])

	srv := rpc.NewServer()
	require.NoError(t, srv.RegisterName(visorapi.RPCPrefix, &RPC{visor: b, log: logrus.New()}))
	sc, cc := net.Pipe()
	go srv.ServeConn(sc)
	api := visorapi.NewRPCClient(nil, cc, visorapi.RPCPrefix, 30*time.Second)

	small, zero, off, on := int64(4096), int64(0), false, true
	require.NoError(t, api.MailSetSettings(visorapi.MailSettingsUpdate{MaxMessageSize: &small}))
	st, err := api.MailStatus()
	require.NoError(t, err)
	require.Equal(t, small, st.Limits.MaxMessageSize)

	require.NoError(t, api.MailSetSettings(visorapi.MailSettingsUpdate{MaxMessageSize: &zero}))
	st, err = api.MailStatus()
	require.NoError(t, err)
	require.Equal(t, skymail.DefaultMaxMessageSize, st.Limits.MaxMessageSize, "0 over RPC means default")

	require.NoError(t, api.MailSetSettings(visorapi.MailSettingsUpdate{Enable: &off}))
	st, err = api.MailStatus()
	require.NoError(t, err)
	require.False(t, st.Running, "false over RPC turns it off")
	require.NoError(t, api.MailSetSettings(visorapi.MailSettingsUpdate{Enable: &on}))
	st, err = api.MailStatus()
	require.NoError(t, err)
	require.True(t, st.Running)
}

// TestSkynetAddressNeverFallsBackToDmsg: dmsg reaches B, but there is no
// skynet networker here, so B's .skynet address must fail — the suffix
// names the network, and the mailbox does not quietly swap it.
func TestSkynetAddressNeverFallsBackToDmsg(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 2, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	clients := env.AllClients()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a, b := mailVisor(t, ctx, clients[0]), mailVisor(t, ctx, clients[1])

	res, err := a.MailSend(skymail.Outgoing{To: []string{"bob@" + clients[1].LocalPK().DNSLabel() + ".skynet"}, Body: "x"})
	require.NoError(t, err, "no route yet is a reason to wait, not to give up")
	require.NotEmpty(t, res.Recipients[0].Err)
	require.True(t, res.Recipients[0].Queued)
	inbox, err := b.MailList(skymail.FolderInbox)
	require.NoError(t, err)
	require.Empty(t, inbox, "not delivered over dmsg behind the sender's back")
	require.NoError(t, a.MailRetry(""))
	time.Sleep(time.Second)
	inbox, err = b.MailList(skymail.FolderInbox)
	require.NoError(t, err)
	require.Empty(t, inbox, "nor on a retry")

	res, err = a.MailSend(skymail.Outgoing{To: []string{bobAt(clients[1].LocalPK())}, Body: "x"})
	require.NoError(t, err, ".dmsg still reaches it")
	require.Equal(t, "dmsg", res.Recipients[0].Via)
}

// TestMailHelpersDefaultToTheHypervisorRelation: with no lists set, a
// visor carries mail for its hypervisors; explicit lists replace that,
// an empty one turns it off, and both survive in settings.json.
func TestMailHelpersDefaultToTheHypervisorRelation(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 1, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	v := mailVisor(t, ctx, env.AllClients()[0])
	hvPK, _ := cipher.GenerateKeyPair()
	v.conf.Hypervisors = []cipher.PubKey{hvPK}

	st, err := v.MailStatus()
	require.NoError(t, err)
	require.True(t, st.HelpersDefault)
	require.Empty(t, st.Helpers, "manages no visors")
	require.True(t, st.RelayForDefault)
	require.Equal(t, []cipher.PubKey{hvPK}, st.RelayFor)

	helper, _ := cipher.GenerateKeyPair()
	none := []cipher.PubKey{}
	require.NoError(t, v.MailSetSettings(visorapi.MailSettingsUpdate{Helpers: &[]cipher.PubKey{helper}, RelayFor: &none}))
	st, err = v.MailStatus()
	require.NoError(t, err)
	require.False(t, st.HelpersDefault)
	require.Equal(t, []cipher.PubKey{helper}, st.Helpers)
	require.False(t, st.RelayForDefault)
	require.Empty(t, st.RelayFor, "an empty list is none, not the default")

	// What a restart reads.
	saved, err := loadSkymailSaved(skymailDir(v.conf))
	require.NoError(t, err)
	require.Equal(t, []cipher.PubKey{helper}, *saved.Helpers)
	require.Empty(t, *saved.RelayFor)

	// Over the RPC an empty list must not turn back into "unset".
	got := visorapi.MailSettingsUpdate{RelayFor: &none, HelpersDefault: true}.Wire().Update()
	require.NotNil(t, got.RelayFor)
	require.True(t, got.HelpersDefault)

	require.NoError(t, v.MailSetSettings(visorapi.MailSettingsUpdate{HelpersDefault: true, RelayForDefault: true}))
	st, err = v.MailStatus()
	require.NoError(t, err)
	require.True(t, st.HelpersDefault)
	require.True(t, st.RelayForDefault)
	require.Equal(t, []cipher.PubKey{hvPK}, st.RelayFor)
}

// TestHelperDeliversOverRealDmsg: A hands its queued mail to H, and H
// delivers it once B can take mail, filed under A with H as the relay.
func TestHelperDeliversOverRealDmsg(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 3, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	clients := env.AllClients()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a, h, b := mailVisor(t, ctx, clients[0]), mailVisor(t, ctx, clients[1]), mailVisor(t, ctx, clients[2])
	pkA, pkH, pkB := clients[0].LocalPK(), clients[1].LocalPK(), clients[2].LocalPK()
	require.NoError(t, a.MailSetSettings(visorapi.MailSettingsUpdate{Helpers: &[]cipher.PubKey{pkH}}))
	require.NoError(t, h.MailSetSettings(visorapi.MailSettingsUpdate{RelayFor: &[]cipher.PubKey{pkA}}))
	off, on := false, true
	require.NoError(t, b.MailSetSettings(visorapi.MailSettingsUpdate{Enable: &off}))

	res, err := a.MailSend(skymail.Outgoing{To: []string{bobAt(pkB)}, Subject: "carried", Body: "x"})
	require.NoError(t, err)
	require.True(t, res.Recipients[0].Queued)
	require.Eventually(t, func() bool {
		out, err := h.MailList(skymail.FolderOutbox)
		return err == nil && len(out) == 1 && out[0].Queue.CarriedFor == pkA.Hex()
	}, 20*time.Second, 200*time.Millisecond, "H takes A's mail")
	out, err := a.MailList(skymail.FolderOutbox)
	require.NoError(t, err)
	require.Equal(t, []string{pkH.Hex()}, out[0].Queue.Handed)

	require.NoError(t, b.MailSetSettings(visorapi.MailSettingsUpdate{Enable: &on}))
	require.Eventually(t, func() bool {
		require.NoError(t, h.MailRetry(""))
		inbox, err := b.MailList(skymail.FolderInbox)
		return err == nil && len(inbox) == 1
	}, 20*time.Second, time.Second, "H delivers")
	inbox, err := b.MailList(skymail.FolderInbox)
	require.NoError(t, err)
	require.Equal(t, pkA.Hex(), inbox[0].PeerPK)
	require.Equal(t, pkH.Hex(), inbox[0].RelayPK)
	require.True(t, inbox[0].FromVerified)
}

// TestParkedMailOverRealDmsg: A parks mail for B on a dmsg-async server
// and stops; B, which was never online before, fetches it from the server
// when it starts, and files it as A's.
func TestParkedMailOverRealDmsg(t *testing.T) {
	env := dmsgtest.NewEnv(t, 30*time.Second)
	require.NoError(t, env.Startup(0, 0, 0, nil))
	t.Cleanup(env.Shutdown)
	srv, err := env.NewServerWithConfig(dmsg.ServerConfig{Async: &dmsg.AsyncStoreConfig{}})
	require.NoError(t, err)
	cA, err := env.NewClient(&dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a := mailVisor(t, ctx, cA)
	pkB, skB := cipher.GenerateKeyPair()

	res, err := a.MailSend(skymail.Outgoing{To: []string{bobAt(pkB)}, Subject: "parked", Body: "x"})
	require.NoError(t, err)
	require.True(t, res.Recipients[0].Queued)
	require.Eventually(t, func() bool {
		out, err := a.MailList(skymail.FolderOutbox)
		return err == nil && len(out) == 1 && out[0].Queue.Parked == 1
	}, 20*time.Second, 100*time.Millisecond, "A parks it")
	n, _, _, ok := srv.AsyncStats()
	require.True(t, ok)
	require.Equal(t, 1, n)
	a.stopSkymail("gone")

	cB, err := env.NewClientWithKeys(pkB, skB, &dmsg.Config{MinSessions: 1, Async: &dmsg.AsyncClientConfig{FetchInterval: time.Hour}})
	require.NoError(t, err)
	b := mailVisor(t, ctx, cB)
	require.Eventually(t, func() bool {
		inbox, err := b.MailList(skymail.FolderInbox)
		return err == nil && len(inbox) == 1
	}, 20*time.Second, 100*time.Millisecond, "B fetches it when it starts")
	inbox, err := b.MailList(skymail.FolderInbox)
	require.NoError(t, err)
	require.Equal(t, "parked", inbox[0].Subject)
	require.Equal(t, cA.LocalPK().Hex(), inbox[0].PeerPK)
	require.Equal(t, srv.LocalPK().Hex(), inbox[0].RelayPK)
	require.True(t, inbox[0].FromVerified)
	require.Eventually(t, func() bool { n, _, _, _ := srv.AsyncStats(); return n == 0 }, 5*time.Second, 50*time.Millisecond,
		"B's ack empties the server")
}

func TestAllErrorsAre(t *testing.T) {
	tooBig := fmt.Errorf("s1: %w", dmsg.ErrAsyncTooBig)
	require.True(t, allErrorsAre(errors.Join(tooBig, fmt.Errorf("s2: %w", dmsg.ErrAsyncTooBig)), dmsg.ErrAsyncTooBig))
	require.False(t, allErrorsAre(errors.Join(tooBig, errors.New("s2: timeout")), dmsg.ErrAsyncTooBig),
		"one server that failed otherwise may take it later")
	require.True(t, allErrorsAre(tooBig, dmsg.ErrAsyncTooBig))
	require.False(t, allErrorsAre(errors.Join(), dmsg.ErrAsyncTooBig))
}
