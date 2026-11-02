// Package visor pkg/visor/dmsg_async_test.go c3-vis-core
package visor

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgasync"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func asyncVisor(t *testing.T, ctx context.Context, c *dmsg.Client, conf *visorconfig.DmsgAsyncConfig, hvs ...cipher.PubKey) *Visor {
	t.Helper()
	v := &Visor{
		ctx:   ctx,
		dmsgC: c,
		conf: &visorconfig.V1{
			Common:    &visorconfig.Common{PK: c.LocalPK(), SK: c.LocalSK()},
			LocalPath: filepath.Join(t.TempDir(), "local"),
			DmsgAsync: conf,
		},
		initLock: new(sync.RWMutex),
	}
	v.conf.Hypervisors = hvs
	require.NoError(t, initDmsgAsync(ctx, v, logging.MustGetLogger("dmsg-async-test")))
	return v
}

// A hypervisor holds for a visor it is configured to accept. The visor
// registers with it, a sender that has the visor's record leaves a message,
// and the visor takes it, or its in-visor handler gets it on sync.
func TestDmsgAsyncVisor(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 3, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cs := env.AllClients()
	hostC, rcptC, senderC := cs[0], cs[1], cs[2]

	on := true
	off := false
	host := asyncVisor(t, ctx, hostC, &visorconfig.DmsgAsyncConfig{Host: visorconfig.DmsgAsyncHostConfig{
		Enable: &on,
		Hosted: []cipher.PubKey{rcptC.LocalPK()},
	}})
	rcpt := asyncVisor(t, ctx, rcptC, &visorconfig.DmsgAsyncConfig{Inbox: visorconfig.DmsgAsyncInboxConfig{
		Publish: &off,
		Allow:   []cipher.PubKey{senderC.LocalPK()},
	}}, hostC.LocalPK())
	sender := asyncVisor(t, ctx, senderC, nil)

	require.NoError(t, rcpt.DmsgAsyncSync())
	st, err := rcpt.DmsgAsyncStatus()
	require.NoError(t, err)
	require.True(t, st.Inbox.Enabled)
	require.NotNil(t, st.Inbox.Record)
	require.Len(t, st.Inbox.Hosts, 1)
	require.Empty(t, st.Inbox.Hosts[0].Error)
	require.False(t, st.Inbox.Hosts[0].Registered.IsZero())

	hst, err := host.DmsgAsyncStatus()
	require.NoError(t, err)
	require.NotNil(t, hst.Host)
	require.Equal(t, []cipher.PubKey{rcptC.LocalPK()}, hst.Host.Registered)

	// The sender has the record already, as a contact would.
	sender.async.mu.Lock()
	sender.async.cache[rcptC.LocalPK()] = cachedRecord{rec: *st.Inbox.Record, at: time.Now()}
	sender.async.mu.Unlock()

	res, err := sender.DmsgAsyncSend(visorapi.DmsgAsyncSendRequest{To: rcptC.LocalPK(), Port: 1, Payload: []byte("hi")})
	require.NoError(t, err)
	require.Equal(t, hostC.LocalPK(), res.Host)

	msgs, err := rcpt.DmsgAsyncTake([]uint16{1})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, senderC.LocalPK(), msgs[0].From)
	require.Equal(t, []byte("hi"), msgs[0].Payload)

	got := make(chan dmsgasync.Message, 1)
	rcpt.AsyncHandle(2, func(m dmsgasync.Message) error {
		got <- m
		return nil
	})
	_, err = rcpt.DmsgAsyncTake([]uint16{2})
	require.Error(t, err, "take refuses a port the visor handles")
	_, err = sender.DmsgAsyncSend(visorapi.DmsgAsyncSendRequest{To: rcptC.LocalPK(), Port: 2, Payload: []byte("to the handler")})
	require.NoError(t, err)
	require.NoError(t, rcpt.DmsgAsyncSync())
	select {
	case m := <-got:
		require.Equal(t, []byte("to the handler"), m.Payload)
	default:
		t.Fatal("the handler did not get the message")
	}

	// The host leaving a message for a visor it holds for deposits on its
	// own store, which dmsg cannot loop back to. The recipient's allow list
	// does not name the host, so it is refused.
	host.async.mu.Lock()
	host.async.cache[rcptC.LocalPK()] = cachedRecord{rec: *st.Inbox.Record, at: time.Now()}
	host.async.mu.Unlock()
	_, err = host.DmsgAsyncSend(visorapi.DmsgAsyncSendRequest{To: rcptC.LocalPK(), Port: 1, Payload: []byte("x")})
	require.ErrorIs(t, err, dmsgasync.ErrNotAllowed)
}

// With the default policy the host can leave a message for a visor it holds
// for, on its own store.
func TestDmsgAsyncHostSendsToItsVisor(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 2, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cs := env.AllClients()
	hostC, rcptC := cs[0], cs[1]
	on, off := true, false
	host := asyncVisor(t, ctx, hostC, &visorconfig.DmsgAsyncConfig{Host: visorconfig.DmsgAsyncHostConfig{
		Enable: &on, Store: dmsgasync.StoreMemory, Hosted: []cipher.PubKey{rcptC.LocalPK()},
	}})
	rcpt := asyncVisor(t, ctx, rcptC, &visorconfig.DmsgAsyncConfig{Inbox: visorconfig.DmsgAsyncInboxConfig{Publish: &off}}, hostC.LocalPK())
	require.NoError(t, rcpt.DmsgAsyncSync())
	st, err := rcpt.DmsgAsyncStatus()
	require.NoError(t, err)

	host.async.mu.Lock()
	host.async.cache[rcptC.LocalPK()] = cachedRecord{rec: *st.Inbox.Record, at: time.Now()}
	host.async.mu.Unlock()
	res, err := host.DmsgAsyncSend(visorapi.DmsgAsyncSendRequest{To: rcptC.LocalPK(), Port: 1, Payload: []byte("from your hypervisor")})
	require.NoError(t, err)
	require.Equal(t, hostC.LocalPK(), res.Host)

	msgs, err := rcpt.DmsgAsyncTake([]uint16{1})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, hostC.LocalPK(), msgs[0].From)
}
