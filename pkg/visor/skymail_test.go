package visor

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

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
	require.Equal(t, runtime.GOOS == "js", skymailEnabled(conf), "no section: on only where no MTA can run")
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

func dmsgAddr(local string, pk cipher.PubKey) string {
	return local + "@" + pk.DNSLabel() + ".dmsg"
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

	res, err := a.MailSend(skymail.Outgoing{From: "alice", To: []string{dmsgAddr("bob", pkB)}, Subject: "over dmsg", Body: "hello"})
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
	res, err = a.MailSend(skymail.Outgoing{To: []string{dmsgAddr("bob", pkB)}, Body: "again"})
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
