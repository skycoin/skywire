package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func TestReachCounts(t *testing.T) {
	b, fp := newTestBook(t, map[string]bool{"203.0.113.7:7777": true})
	open, _ := cipher.GenerateKeyPair()
	closed, _ := cipher.GenerateKeyPair()
	b.noteBind(types.STCPR, open, addrresolver.VisorData{RemoteAddr: "203.0.113.7", LocalAddresses: addrresolver.LocalAddresses{Port: "7777"}})
	waitProbe(t, fp)
	b.noteBind(types.STCPR, closed, addrresolver.VisorData{RemoteAddr: "203.0.113.8", LocalAddresses: addrresolver.LocalAddresses{Port: "7777"}})
	waitProbe(t, fp)
	b.setLive(closed, true)
	eventually(t, func() bool {
		c := b.counts()
		return c.open["stcpr"] == 1
	})
	c := b.counts()
	require.Equal(t, 2, c.peers)
	require.Equal(t, 2, c.bound["stcpr"])
	require.Equal(t, 1, c.live)
	require.Zero(t, c.closed["stcpr"], "one miss does not close a binding")
}

func TestLookupsAndBindsAreCounted(t *testing.T) {
	a := newTestAPI(t)
	sender, _ := cipher.GenerateKeyPair()
	receiver, _ := cipher.GenerateKeyPair()
	resolve := func() int {
		rec := httptest.NewRecorder()
		a.resolve(rec, authReq(t, http.MethodGet, "/resolve/stcpr/"+receiver.Hex(), nil,
			&sender, map[string]string{"type": "stcpr", "pk": receiver.Hex()}))
		return rec.Code
	}
	require.Equal(t, http.StatusNotFound, resolve())
	require.NoError(t, a.store.Bind(t.Context(), types.STCPR, receiver, addrresolver.VisorData{RemoteAddr: "203.0.113.9"}))
	require.Equal(t, http.StatusOK, resolve())

	n := a.counters.snapshot()
	require.Equal(t, uint64(1), n[resolveNotFound+".stcpr"])
	require.Equal(t, uint64(1), n[resolveFound+".stcpr"])
	require.Equal(t, uint64(1), n[chartBinds+"stcpr"])

	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusNotFound, rec.Code, "no page before charts start")
}
