// Package visor pkg/visor/hypervisor_park_test.go c3-vis-core
package visor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgasync"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// A change a hypervisor records for an offline visor waits on the visor's
// inbox, and the visor applies it when it fetches.
func TestHypervisorParkedChange(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 2, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cs := env.AllClients()
	hvC, mvC := cs[0], cs[1]
	on, off := true, false
	hv := asyncVisor(t, ctx, hvC, &visorconfig.DmsgAsyncConfig{Host: visorconfig.DmsgAsyncHostConfig{
		Enable: &on, Store: dmsgasync.StoreMemory, Hosted: []cipher.PubKey{mvC.LocalPK()},
	}})
	mv := asyncVisor(t, ctx, mvC, &visorconfig.DmsgAsyncConfig{Inbox: visorconfig.DmsgAsyncInboxConfig{Publish: &off}}, hvC.LocalPK())
	mv.conf.SetLogger(logging.NewMasterLogger())
	require.NoError(t, mv.DmsgAsyncSync())

	api, rec := visorapi.NewRecordingRPCClient(hvParkAllows)
	require.NoError(t, api.SetIsPublic(true))
	_, err := api.Summary()
	require.ErrorIs(t, err, visorapi.ErrNotDeferrable)
	require.Len(t, rec.Frames(), 1)

	host, err := hv.parkForVisor(mvC.LocalPK(), rec.Frames())
	require.NoError(t, err)
	require.Equal(t, hvC.LocalPK(), host)

	require.False(t, mv.conf.IsPublic)
	require.NoError(t, mv.DmsgAsyncSync())
	require.True(t, mv.conf.IsPublic, "the parked change did not apply")

	// A key that is not one of the visor's hypervisors cannot leave changes.
	mv.conf.IsPublic = false
	_, strangerSK := cipher.GenerateKeyPair()
	body := []byte(`{"id":"x1","frames":[]}`)
	sealed, err := skymail.Seal(body, strangerSK, mvC.LocalPK())
	require.NoError(t, err)
	require.NoError(t, mv.takeHypervisorParked(dmsgasync.Message{Port: skyenv.DmsgHypervisorPort, Payload: sealed}))
	require.True(t, mv.hvParkedFirst("x1"), "a stranger's change was remembered")

	// A change applies once, and only calls a hypervisor may leave are served.
	require.False(t, mv.hvParkedFirst("x1"))
	all, rec2 := visorapi.NewRecordingRPCClient(func(string) bool { return true })
	require.NoError(t, all.Shutdown())
	_, err = mv.serveParkedCall(hvC.LocalPK(), rec2.Frames()[0])
	require.Error(t, err)
}

// The hypervisor answers a change for a known visor that is offline with 202
// and the host it left it on, and still refuses one that needs the visor.
func TestHypervisorParksForOfflineVisor(t *testing.T) {
	env := dmsgtest.NewEnv(t, 10*time.Second)
	require.NoError(t, env.Startup(0, 1, 2, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cs := env.AllClients()
	hvC, mvC := cs[0], cs[1]
	on, off := true, false
	hvV := asyncVisor(t, ctx, hvC, &visorconfig.DmsgAsyncConfig{Host: visorconfig.DmsgAsyncHostConfig{
		Enable: &on, Store: dmsgasync.StoreMemory, Hosted: []cipher.PubKey{mvC.LocalPK()},
	}})
	hvV.opts.NoCSRF = true
	mv := asyncVisor(t, ctx, mvC, &visorconfig.DmsgAsyncConfig{Inbox: visorconfig.DmsgAsyncInboxConfig{Publish: &off}}, hvC.LocalPK())
	require.NoError(t, mv.DmsgAsyncSync())

	hv := &Hypervisor{visor: hvV, mu: new(sync.RWMutex), logger: logging.MustGetLogger("hv-park-test"), summaryCache: map[cipher.PubKey]cachedSummary{mvC.LocalPK(): {seenAt: time.Now()}}}
	do := func(h http.HandlerFunc, method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/visors/"+mvC.LocalPK().Hex()+"/x", strings.NewReader(body))
		req.SetPathValue("pk", mvC.LocalPK().Hex())
		rr := httptest.NewRecorder()
		h(rr, req)
		return rr
	}
	rr := do(hv.putIsPublic(), http.MethodPut, `{"is_public":true}`)
	require.Equal(t, http.StatusAccepted, rr.Code, rr.Body.String())
	require.Equal(t, hvC.LocalPK().Hex(), rr.Header().Get(hvParkedHeader))

	req := httptest.NewRequest(http.MethodPut, "/x", strings.NewReader(`{"autostart":true}`))
	req.SetPathValue("pk", mvC.LocalPK().Hex())
	req.SetPathValue("app", "skychat")
	rr = httptest.NewRecorder()
	hv.putApp()(rr, req)
	require.Equal(t, http.StatusAccepted, rr.Code, rr.Body.String())
	require.Contains(t, rr.Body.String(), `"auto_start":true`)

	rr = do(hv.restart(), http.MethodPost, "")
	require.Equal(t, http.StatusServiceUnavailable, rr.Code, rr.Body.String())
}

// A change waits for the visor to finish starting, since most calls need the
// app launcher.
func TestHypervisorParkedWaitsForStartup(t *testing.T) {
	v := &Visor{}
	require.True(t, v.waitStarted(time.Millisecond), "a visor without a startup signal counts as started")
	v.startupComplete = make(chan struct{})
	require.False(t, v.waitStarted(10*time.Millisecond))
	close(v.startupComplete)
	require.True(t, v.waitStarted(time.Millisecond))
}
