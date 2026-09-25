package visor

import (
	"context"
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
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestSkymailDefaults(t *testing.T) {
	conf := &visorconfig.V1{LocalPath: "/opt/skywire/local"}
	require.True(t, skymailEnabled(conf), "no section: on, with the default limits")
	require.Equal(t, "/opt/skywire/mail", skymailDir(conf), "beside local/, which the wasm visor never persists")

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
			Common:    &visorconfig.Common{PK: c.LocalPK()},
			LocalPath: filepath.Join(t.TempDir(), "local"),
			Skymail:   &visorconfig.SkymailConfig{Enable: true},
		},
		dmsgC:          c,
		forwardedPorts: NewForwardedPorts(""),
		initLock:       new(sync.RWMutex),
	}
	require.NoError(t, initSkymail(ctx, v, logging.MustGetLogger("skymail")))
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

	path := filepath.Join(t.TempDir(), "config.json")
	_, sk := cipher.GenerateKeyPair()
	common, err := visorconfig.NewCommon(logging.NewMasterLogger(), path, &sk)
	require.NoError(t, err)
	common.PK = clients[1].LocalPK()
	b.conf.Common = common

	off, on := false, true
	require.NoError(t, b.MailSetSettings(visorapi.MailSettingsUpdate{Enable: &off}))
	st, err := b.MailStatus()
	require.NoError(t, err)
	require.False(t, st.Running)
	require.Equal(t, "disabled", st.Reason)
	_, err = a.MailSend(skymail.Outgoing{To: []string{bobAt(clients[1].LocalPK())}, Body: "x"})
	require.Error(t, err, "nothing listens on 25 while it is off")

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
	var res *skymail.SendResult
	require.Eventually(t, func() bool {
		res, _ = a.MailSend(skymail.Outgoing{To: []string{bobAt(clients[1].LocalPK())}, Body: strings.Repeat("z", 4000)}) //nolint:errcheck
		return res != nil && strings.Contains(res.Recipients[0].Err, "552")
	}, 10*time.Second, 200*time.Millisecond, "the new size limit applies at once")
	res, err = a.MailSend(skymail.Outgoing{To: []string{bobAt(clients[1].LocalPK())}, Body: "fits"})
	require.NoError(t, err)
	require.Equal(t, "dmsg", res.Recipients[0].Via)

	saved, err := visorconfig.ReadFile(path)
	require.NoError(t, err)
	require.NotNil(t, saved.Skymail)
	require.True(t, saved.Skymail.Enable)
	require.Equal(t, small, saved.Skymail.MaxMessageSize)
	require.Equal(t, visorconfig.Duration(age), saved.Skymail.MaxAge)
}
