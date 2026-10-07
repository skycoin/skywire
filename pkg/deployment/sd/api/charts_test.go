package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/deployment/sd/store"
	"github.com/skycoin/skywire/pkg/geo"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

func TestCollectCharts(t *testing.T) {
	ctx := context.Background()
	db := store.NewMemoryStore(time.Hour)
	add := func(typ, cc, ver string) {
		pk, _ := cipher.GenerateKeyPair()
		s := &servicedisc.Service{Addr: servicedisc.NewSWAddr(pk, 3), Type: typ, Version: ver}
		if cc != "" {
			s.Geo = &geo.LocationData{Country: cc}
		}
		require.Nil(t, db.UpdateService(ctx, s))
	}
	add(servicedisc.ServiceTypeVPN, "DE", "v1.3.99")
	add(servicedisc.ServiceTypeVPN, "DE", "v1.3.99")
	add(servicedisc.ServiceTypeSkysocks, "US", "")
	add(servicedisc.ServiceTypeProxy, "", "v1.3.98")
	add(servicedisc.ServiceTypeVisor, "US", "v1.3.99")
	a := newTestAPI(t, db)

	v, err := a.collectCharts(ctx)
	require.NoError(t, err)
	require.Equal(t, 2.0, v[chartTypePrefix+servicedisc.ServiceTypeVPN])
	require.Equal(t, 2.0, v[chartTypePrefix+servicedisc.ServiceTypeProxy], "skysocks and proxy are one service")
	require.Equal(t, 1.0, v[chartTypePrefix+servicedisc.ServiceTypeVisor])
	require.Equal(t, 2.0, v[chartVPNCountry+"DE"])
	require.Equal(t, 1.0, v[chartProxyCountry+"US"])
	require.Equal(t, 1.0, v[chartProxyCountry+"unknown"])
	require.Equal(t, 3.0, v[chartVersionPrefix+"v1.3.99"])
	require.Equal(t, 1.0, v[chartVersionPrefix+"unknown"])

	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()
	a.StartCharts(ctx2, charts.NewMemoryStore(), logging.MustGetLogger("test"))
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "VPN servers by country")
}
