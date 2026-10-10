package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/geo"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore(time.Minute)

	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	a := &servicedisc.Service{Addr: servicedisc.NewSWAddr(pkA, 3), Type: "vpn", Version: "v1",
		Geo: &geo.LocationData{Country: "DE"}, LocalIPs: []string{"10.0.0.1"}}
	b := &servicedisc.Service{Addr: servicedisc.NewSWAddr(pkB, 3), Type: "vpn", Version: "v2", DisplayNodeIP: true,
		LocalIPs: []string{"10.0.0.2"}}
	require.Nil(t, s.UpdateServiceAndHeartbeat(ctx, a, "v1"))
	require.Nil(t, s.UpdateService(ctx, b))

	all, herr := s.Services(ctx, "vpn", "", "")
	require.Nil(t, herr)
	require.Len(t, all, 2)
	for _, svc := range all {
		require.False(t, svc.DisplayNodeIP)
		if svc.Addr.PubKey() == pkA {
			require.Nil(t, svc.LocalIPs, "local IPs hidden unless the entry asks to show them")
		} else {
			require.EqualValues(t, []string{"10.0.0.2"}, svc.LocalIPs)
		}
	}
	byVersion, _ := s.Services(ctx, "vpn", "v2", "") //nolint:errcheck
	require.Len(t, byVersion, 1)
	byCountry, _ := s.Services(ctx, "vpn", "", "DE") //nolint:errcheck
	require.Len(t, byCountry, 1)

	got, herr := s.Service(ctx, "vpn", a.Addr)
	require.Nil(t, herr)
	require.Equal(t, "v1", got.Version)
	n, err := s.CountServices(ctx, "vpn")
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	types, err := s.CountServiceTypes(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, types)
	mine, _ := s.ServicesByPK(ctx, pkA) //nolint:errcheck
	require.Len(t, mine, 1)

	sums, err := s.GetAllVisorSummaries(ctx, true, true)
	require.NoError(t, err)
	require.Len(t, sums, 1, "only heartbeating visors are summarized")
	require.True(t, sums[0].Online)
	require.Equal(t, "v1", sums[0].Version)
	require.Len(t, sums[0].Daily, 1)
	require.Len(t, sums[0].Timeline, 1)

	require.Nil(t, s.DeleteService(ctx, "vpn", b.Addr))
	_, herr = s.Service(ctx, "vpn", b.Addr)
	require.NotNil(t, herr)

	// An entry past its ttl is gone.
	ms := s.(*memoryStore)
	ms.mu.Lock()
	e := ms.services["vpn"][pkA.String()]
	e.expires = time.Now().Add(-time.Second)
	ms.services["vpn"][pkA.String()] = e
	ms.mu.Unlock()
	all, _ = s.Services(ctx, "vpn", "", "") //nolint:errcheck
	require.Empty(t, all)
	sums, _ = s.GetAllVisorSummaries(ctx, false, false) //nolint:errcheck
	require.False(t, sums[0].Online)
}
