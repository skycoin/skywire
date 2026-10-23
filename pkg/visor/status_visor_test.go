package visor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/charts"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

func TestVisorChartsPage(t *testing.T) {
	// A visor still starting has none of what is sampled.
	c := &visorCharts{v: &Visor{}, prev: map[uuid.UUID]transport.BandwidthData{}}
	v, err := c.collect(context.Background())
	require.NoError(t, err)
	require.Empty(t, v)

	ctx := context.Background()
	st := charts.NewMemoryStore()
	now := time.Now().UTC().Truncate(charts.Interval)
	for i := 3; i > 0; i-- {
		require.NoError(t, st.Add(ctx, charts.Sample{At: now.Add(-time.Duration(i) * charts.Interval), V: map[string]float64{
			chartTpCount + "stcpr": 2, chartTpOut + "stcpr": 1 << 20, chartTpIn + "stcpr": 2 << 20,
			chartRoutes: 4, chartDmsgSess: 3,
		}}))
	}
	page := &charts.Page{Title: "Skywire visor", Store: st, Build: func(ctx context.Context, r charts.Range, now time.Time) (charts.Content, error) {
		return buildVisorCharts(ctx, st, r, now)
	}}
	rec := httptest.NewRecorder()
	page.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status/visor", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	for _, s := range []string{"Bytes sent over transports, by type", "Transports, by type", "Routes and sessions", "stcpr"} {
		require.Contains(t, string(body), s)
	}
}

type fakeChartsRouter struct {
	router.Router
	sts   []router.RouteStatus
	infos []router.MuxInfo
	mc    router.MuxCounters
}

func (f *fakeChartsRouter) RoutesCount() int                          { return 2 }
func (f *fakeChartsRouter) ActiveRouteStatuses() []router.RouteStatus { return f.sts }
func (f *fakeChartsRouter) RouteGroupMuxInfoAll() []router.MuxInfo    { return f.infos }
func (f *fakeChartsRouter) MuxCounters() router.MuxCounters           { return f.mc }

func TestVisorChartsCollectDetail(t *testing.T) {
	local, _ := cipher.GenerateKeyPair()
	exit, _ := cipher.GenerateKeyPair()
	client, _ := cipher.GenerateKeyPair()
	// A skysocks-client dial (ephemeral local port, named by the dialer) and
	// an accepted route to a local server port, named by its listening port.
	dialed := routing.NewRouteDescriptor(exit, local, 3, 49200)
	accepted := routing.NewRouteDescriptor(client, local, 50000, 44)
	fr := &fakeChartsRouter{
		sts: []router.RouteStatus{
			{LocalPK: local, RemotePK: exit, LocalPort: 49200, RemotePort: 3, BandwidthSent: 100, BandwidthReceived: 1000},
			{LocalPK: local, RemotePK: client, LocalPort: 44, RemotePort: 50000, BandwidthSent: 7, BandwidthReceived: 9},
		},
		infos: []router.MuxInfo{
			{Desc: dialed, AppName: "skysocks-client", MuxEnabled: true, TunnelRole: "active", Legs: []router.MuxLeg{
				{LegStats: router.LegStats{SentBytes: 60, RecvBytes: 600}, TransportID: "a", Direct: true},
				{LegStats: router.LegStats{SentBytes: 40, RecvBytes: 400}, TransportID: "b"},
			}},
			{Desc: accepted, TunnelRole: "standby", Legs: []router.MuxLeg{{TransportID: "c"}}},
		},
		mc: router.MuxCounters{TunnelPromotions: 1, LegRehomesSent: 2},
	}
	c := &visorCharts{v: &Visor{router: fr}}
	appByPort := func(p routing.Port) (string, bool) { return map[routing.Port]string{44: "vpn-server"}[p], p == 44 }

	out := map[string]float64{}
	c.sampleRouteGroups(out, fr.sts, fr.infos, appByPort)
	c.sampleMux(out, fr.infos, fr.mc)
	require.Equal(t, 100.0, out[chartAppOut+"skysocks-client"])
	require.Equal(t, 1000.0, out[chartAppIn+"skysocks-client"])
	require.Equal(t, 9.0, out[chartAppIn+"vpn-server"])
	require.Equal(t, 2.0, out[chartMuxGroups])
	require.Equal(t, 3.0, out[chartMuxLegs])
	require.Equal(t, 1.0, out[chartMuxStandby])
	require.Equal(t, 1.0, out[chartMuxStandbyLegs])
	require.Equal(t, 600.0, out[chartMuxLegIn+"direct"])
	require.Equal(t, 40.0, out[chartMuxLegOut+"multihop"])
	require.Equal(t, 1.0, out[chartMuxEvent+"promotions"])

	// The next sample counts only what changed.
	fr.sts[0].BandwidthReceived = 1500
	fr.infos[0].Legs[1].RecvBytes = 900
	fr.mc.LegRehomesSent = 5
	out = map[string]float64{}
	c.sampleRouteGroups(out, fr.sts, fr.infos, appByPort)
	c.sampleMux(out, fr.infos, fr.mc)
	require.Equal(t, 500.0, out[chartAppIn+"skysocks-client"])
	require.Equal(t, 0.0, out[chartAppIn+"vpn-server"])
	require.Equal(t, 500.0, out[chartMuxLegIn+"multihop"])
	require.Equal(t, 0.0, out[chartMuxEvent+"promotions"])
	require.Equal(t, 3.0, out[chartMuxEvent+"rehome.sent"])

	// The whole collector reads the router.
	v, err := c.collect(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2.0, v[chartRoutes])
	require.Contains(t, v, chartMuxGroups)
}

func TestVisorChartsBusiestTransports(t *testing.T) {
	c := &visorCharts{}
	var tps []tpSample
	var top cipher.PubKey
	for i := 0; i < topTransports+2; i++ {
		pk, _ := cipher.GenerateKeyPair()
		if i == 0 {
			top = pk
		}
		tps = append(tps, tpSample{id: uuid.New(), typ: "stcpr", remote: pk, latency: 12, bw: transport.BandwidthData{SentBytes: uint64(1000 - i), RecvBytes: 1}})
	}
	out := map[string]float64{}
	c.sampleTransports(out, tps)
	n := 0
	for k := range out {
		if strings.HasPrefix(k, chartTpTop) {
			n++
		}
	}
	require.Equal(t, topTransports, n)
	require.Equal(t, 1001.0, out[chartTpTop+"stcpr "+top.Hex()])
	require.Equal(t, 12.0, out[chartTpLat+"stcpr "+top.Hex()])
	require.Equal(t, float64(1000-8+1+1000-9+1), out[chartTpTopOther])
	require.Equal(t, float64(topTransports+2), out[chartTpCount+"stcpr"])

	ctx := context.Background()
	st := charts.NewMemoryStore()
	now := time.Now().UTC().Truncate(charts.Interval)
	require.NoError(t, st.Add(ctx, charts.Sample{At: now.Add(-charts.Interval), V: out}))
	content, err := buildVisorCharts(ctx, st, charts.Ranges[0], now)
	require.NoError(t, err)
	var titles []string
	for _, ch := range content.Charts {
		titles = append(titles, ch.Title)
		if ch.Title == "Busiest transports" {
			require.Equal(t, "stcpr "+top.Hex(), ch.Series[0].Name)
			require.Equal(t, []string{"12 ms"}, ch.Series[0].Cells)
			require.Equal(t, "other", ch.Series[len(ch.Series)-1].Name)
		}
		require.NotEmpty(t, ch.Note, ch.Title)
	}
	for _, s := range []string{"Busiest transports", "Bytes sent by apps", "Bytes received by apps", "Route groups and mux legs", "Mux failover events", "Mux leg traffic, direct or multihop"} {
		require.Contains(t, titles, s)
	}
}

func TestVisorDmsgPortNames(t *testing.T) {
	for port, name := range map[uint16]string{80: "log server", 22: "pty", 46: "hypervisor", 65: "visor RPC", 50: "telemetry feed"} {
		require.Equal(t, name, visorDmsgPorts[port])
	}
}
