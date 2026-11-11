package visor

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/rpc"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func newKnownTestHV(dir string) *Hypervisor {
	hv := &Hypervisor{
		c:            visorconfig.HypervisorConfig{DBPath: filepath.Join(dir, "users.db")},
		remoteVisors: map[cipher.PubKey]Conn{},
		mu:           new(sync.RWMutex),
		logger:       logging.MustGetLogger("test"),
		summaryCache: map[cipher.PubKey]cachedSummary{},
	}
	hv.loadKnownVisors()
	return hv
}

func TestKnownVisorsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	pk, _ := cipher.GenerateKeyPair()
	seen := time.Now().Add(-time.Hour).UTC()

	hv := newKnownTestHV(dir)
	sum := knownVisorSummary(knownVisor{PK: pk, Hostname: "site-a", PublicIP: "203.0.113.7", Version: "v1.3.107"})
	hv.summaryCache[pk] = cachedSummary{sum: sum, seenAt: seen}
	hv.saveKnownVisors()

	hv2 := newKnownTestHV(dir)
	entries := hv2.offlineKnownEntries(nil)
	require.Len(t, entries, 1)
	e := entries[0]
	require.Equal(t, pk, e.PK)
	require.False(t, e.Online)
	require.Equal(t, "site-a", e.Hostname)
	require.Equal(t, "203.0.113.7", e.PublicIP)
	require.Equal(t, "v1.3.107", e.Version)
	require.NotNil(t, e.LastSeenAt)
	require.WithinDuration(t, seen, *e.LastSeenAt, time.Minute)

	require.Empty(t, hv2.offlineKnownEntries(map[cipher.PubKey]bool{pk: true}))

	summaries := hv2.snapshotKnownVisors()
	require.Len(t, summaries, 1)
}

func TestForgetVisor(t *testing.T) {
	dir := t.TempDir()
	offline, _ := cipher.GenerateKeyPair()
	online, _ := cipher.GenerateKeyPair()

	hv := newKnownTestHV(dir)
	for _, pk := range []cipher.PubKey{offline, online} {
		hv.summaryCache[pk] = cachedSummary{sum: knownVisorSummary(knownVisor{PK: pk}), seenAt: time.Now()}
	}
	hv.remoteVisors[online] = Conn{}
	hv.saveKnownVisors()

	require.Error(t, hv.ForgetVisor(online))
	require.NoError(t, hv.ForgetVisor(offline))
	require.Error(t, hv.ForgetVisor(offline))

	hv2 := newKnownTestHV(dir)
	_, ok := hv2.summaryCache[offline]
	require.False(t, ok)
	_, ok = hv2.summaryCache[online]
	require.True(t, ok)
}

func TestForgetVisorOverRPCAndHTTP(t *testing.T) {
	hv := newKnownTestHV(t.TempDir())
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	for _, pk := range []cipher.PubKey{a, b} {
		hv.summaryCache[pk] = cachedSummary{sum: knownVisorSummary(knownVisor{PK: pk}), seenAt: time.Now().Add(-time.Hour)}
	}
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{}}, opts: Options{NoCSRF: true}, hvInstance: hv}
	hv.visor = v

	srv := rpc.NewServer()
	require.NoError(t, srv.RegisterName(visorapi.RPCPrefix, &RPC{visor: v, log: logging.New()}))
	sc, cc := net.Pipe()
	go srv.ServeConn(sc)
	api := visorapi.NewRPCClient(nil, cc, visorapi.RPCPrefix, 30*time.Second)
	require.NoError(t, api.HVForgetVisor(a))
	require.Error(t, api.HVForgetVisor(a))

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /visors/{pk}/known", hv.deleteKnownVisor())
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/visors/"+b.Hex()+"/known", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/visors/"+b.Hex()+"/known", nil))
	require.Equal(t, http.StatusConflict, rr.Code)
	require.Empty(t, hv.offlineKnownEntries(nil))
}
