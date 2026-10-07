package api

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func TestResolveOverUDP(t *testing.T) {
	a := newTestAPI(t)
	requester, _ := cipher.GenerateKeyPair()
	target, _ := cipher.GenerateKeyPair()
	unknown, _ := cipher.GenerateKeyPair()
	require.NoError(t, a.store.Bind(t.Context(), types.SUDPH, target, addrresolver.VisorData{RemoteAddr: "1.2.3.4:5"}))

	ask := func(pk cipher.PubKey, id uint32) addrresolver.UDPResolveReply {
		arEnd, visorEnd := net.Pipe()
		defer arEnd.Close()    //nolint:errcheck
		defer visorEnd.Close() //nolint:errcheck
		got := make(chan addrresolver.UDPResolveReply, 1)
		go func() {
			buf := make([]byte, 4096)
			n, err := visorEnd.Read(buf)
			if err != nil {
				return
			}
			var r addrresolver.UDPResolveReply
			_ = json.Unmarshal(buf[:n], &r) //nolint:errcheck
			got <- r
		}()
		require.NoError(t, a.resolveOverUDP(arEnd, requester, addrresolver.UDPResolveRequest{Resolve: pk.Hex(), ID: id}))
		return <-got
	}

	r := ask(target, 3)
	require.Equal(t, uint32(3), r.Resolved)
	require.True(t, r.Found)
	require.Equal(t, "1.2.3.4:5", r.Data.RemoteAddr)

	r = ask(unknown, 4)
	require.Equal(t, uint32(4), r.Resolved)
	require.False(t, r.Found)

	n := a.counters.snapshot()
	require.Equal(t, uint64(1), n[resolveFound+".sudph"])
	require.Equal(t, uint64(1), n[resolveNotFound+".sudph"])
	require.Equal(t, uint64(2), n[chartLookupUDP])
}

func TestControlLoopAnswersLookups(t *testing.T) {
	a := newTestAPI(t)
	visor, _ := cipher.GenerateKeyPair()
	target, _ := cipher.GenerateKeyPair()
	require.NoError(t, a.store.Bind(t.Context(), types.SUDPH, target, addrresolver.VisorData{RemoteAddr: "1.2.3.4:5"}))

	arEnd, visorEnd := net.Pipe()
	defer visorEnd.Close() //nolint:errcheck
	go a.bindSUDPH(arEnd, "203.0.113.20:40000", visor.Hex())

	bind, err := json.Marshal(addrresolver.LocalAddresses{Port: "40000", Addresses: []string{"192.168.1.5"}})
	require.NoError(t, err)
	_, err = visorEnd.Write(bind)
	require.NoError(t, err)

	req, err := json.Marshal(addrresolver.UDPResolveRequest{Resolve: target.Hex(), ID: 9})
	require.NoError(t, err)
	_, err = visorEnd.Write(req)
	require.NoError(t, err)

	buf := make([]byte, 4096)
	n, err := visorEnd.Read(buf)
	require.NoError(t, err)
	var r addrresolver.UDPResolveReply
	require.NoError(t, json.Unmarshal(buf[:n], &r))
	require.Equal(t, uint32(9), r.Resolved)
	require.True(t, r.Found)

	bound, err := a.store.Resolve(t.Context(), types.SUDPH, visor)
	require.NoError(t, err)
	require.Equal(t, "40000", bound.Port, "the lookup did not disturb the visor's own binding")
}
