package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// The bind handler prefers the visor's reach declaration over the address
// list when deciding whether the visor is dialable where the AR sees it.
// The request comes from 203.0.113.5 (authReq).
func TestBindPrefersDeclaredAccepts(t *testing.T) {
	bind := func(t *testing.T, a *API, pk cipher.PubKey, addrs ...string) int {
		t.Helper()
		body, _ := json.Marshal(addrresolver.LocalAddresses{Port: "30000", Addresses: addrs}) //nolint:errcheck
		rec := httptest.NewRecorder()
		a.bind(rec, authReq(t, http.MethodPost, "/bind/stcpr", body, &pk, nil))
		return rec.Code
	}

	t.Run("accepts the type: binds without the NAT IP in the list", func(t *testing.T) {
		a := newTestAPI(t)
		pk, _ := cipher.GenerateKeyPair()
		a.reach.noteDecl(pk, arfeed.ReachDecl{NAT: arfeed.NATPortRestricted, Accepts: []string{arfeed.TypeSTCPR, arfeed.TypeSUDPH}})
		require.Equal(t, http.StatusOK, bind(t, a, pk, "192.168.1.5"))
		vd, err := a.store.Resolve(context.Background(), types.STCPR, pk)
		require.NoError(t, err)
		require.Equal(t, "203.0.113.5", vd.RemoteAddr)
	})

	// A first declaration can predate a transport client, so leaving the type
	// out falls back to the address test rather than refusing.
	t.Run("type not declared: address test decides", func(t *testing.T) {
		a := newTestAPI(t)
		pk, _ := cipher.GenerateKeyPair()
		a.reach.noteDecl(pk, arfeed.ReachDecl{Accepts: []string{arfeed.TypeSUDPH}})
		require.Equal(t, http.StatusOK, bind(t, a, pk, "192.168.1.5", "203.0.113.5"))
		require.Equal(t, http.StatusBadRequest, bind(t, a, pk, "192.168.1.5"))
	})

	t.Run("old declaration without accepts: address test as before", func(t *testing.T) {
		a := newTestAPI(t)
		pk, _ := cipher.GenerateKeyPair()
		a.reach.noteDecl(pk, arfeed.ReachDecl{NAT: arfeed.NATFullCone})
		require.Equal(t, http.StatusOK, bind(t, a, pk, "192.168.1.5", "203.0.113.5"))
		require.Equal(t, http.StatusBadRequest, bind(t, a, pk, "192.168.1.5"))
	})

	t.Run("no declaration: address test as before", func(t *testing.T) {
		a := newTestAPI(t)
		pk, _ := cipher.GenerateKeyPair()
		require.Equal(t, http.StatusOK, bind(t, a, pk, "203.0.113.5"))
		require.Equal(t, http.StatusBadRequest, bind(t, a, pk, "192.168.1.5"))
	})

	t.Run("busy visor (accepts none): address test, so manual dials still resolve", func(t *testing.T) {
		a := newTestAPI(t)
		pk, _ := cipher.GenerateKeyPair()
		a.reach.noteDecl(pk, arfeed.ReachDecl{Accepts: []string{arfeed.AcceptsNone}})
		require.Equal(t, http.StatusOK, bind(t, a, pk, "203.0.113.5"))
	})

	t.Run("stale declaration is ignored", func(t *testing.T) {
		a := newTestAPI(t)
		pk, _ := cipher.GenerateKeyPair()
		a.reach.noteDecl(pk, arfeed.ReachDecl{Accepts: []string{arfeed.TypeSTCPR}})
		a.reach.mu.Lock()
		a.reach.peers[pk].declSeen = time.Now().Add(-2 * declTTL)
		a.reach.mu.Unlock()
		require.Equal(t, http.StatusBadRequest, bind(t, a, pk, "192.168.1.5"), "a stale yes does not count")
	})
}

// A reach declaration from a visor that predates the accepts field decodes
// with an empty list, and one that has it round-trips.
func TestReachDeclWireCompat(t *testing.T) {
	var old arfeed.ReachDecl
	require.NoError(t, json.Unmarshal([]byte(`{"nat":"full-cone","in":{"stcpr":1700000000}}`), &old))
	require.Empty(t, old.Accepts)

	b, err := json.Marshal(arfeed.ReachDecl{NAT: "open", Accepts: []string{"stcpr"}})
	require.NoError(t, err)
	require.JSONEq(t, `{"nat":"open","acc":["stcpr"]}`, string(b))
}
