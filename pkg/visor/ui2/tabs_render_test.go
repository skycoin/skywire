package ui2

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/appserver/spec"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func renderEN(t *testing.T, c templ.Component) string {
	t.Helper()
	var b strings.Builder
	require.NoError(t, c.Render(With(context.Background(), Page{Base: "../../../", Lang: "en"}, NewCatalog(os.DirFS("../static"))), &b))
	return b.String()
}

func TestInfoRender(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	hv, _ := cipher.GenerateKeyPair()
	srv, _ := cipher.GenerateKeyPair()
	no := false
	i := Info{PK: pk.Hex(), Sum: visorapi.Summary{
		Overview: &visorapi.Overview{
			PubKey: pk, BuildInfo: &buildinfo.Info{Version: "v1.3.104", OS: "linux", Arch: "amd64"},
			Apps: []*appserver.AppState{
				{AppConfig: spec.AppConfig{Name: "skychat", AutoStart: true}, Status: appserver.AppStatusRunning},
				{AppConfig: spec.AppConfig{Name: "vpn-client", AutoStart: true}, Status: appserver.AppStatusErrored},
			},
			Hypervisors: []cipher.PubKey{hv}, ConnectedHypervisor: []cipher.PubKey{hv},
		},
		Health:         &visorapi.HealthInfo{ServicesHealth: "healthy"},
		Uptime:         7200,
		DMSGServers:    []visorapi.DMSGServerInfo{{PK: srv, Latency: 900 * time.Millisecond, Protocol: "wss"}},
		RewardAddress:  "xpub",
		RewardEligible: &no, RewardIneligibleReason: "old version",
		ConfigVersion: "v1.3.100",
	}, Ports: []KV{{"cli", "3435"}}}
	out := renderEN(t, InfoPage(i))
	require.Contains(t, out, pk.Hex())
	require.Contains(t, out, pk.DNSLabel()+".skynet/")
	require.Contains(t, out, hv.Hex())
	require.Contains(t, out, srv.Hex())
	require.Contains(t, out, "900 ms")
	require.Contains(t, out, "vpn-client failed.")
	require.Contains(t, out, "This visor has no transports.")
	require.Contains(t, out, "Not eligible for rewards: old version")
	require.Contains(t, out, "The config was made by v1.3.100")
	require.Contains(t, out, "2 hours")
	require.Contains(t, out, `hx-post="ui2/visors/`+pk.Hex()+`/info/restart"`)
	require.True(t, SameRelease("v1.3.103-122-gabc", "v1.3.103-122-gabc-abc1234"))
}

func TestDmsgRender(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	srv, _ := cipher.GenerateKeyPair()
	cl, n := DmsgOf(&visorapi.DmsgClientSessions{Main: &visorapi.DmsgClientSessionInfo{
		PK: pk, Role: "main", Count: 1, Servers: []cipher.PubKey{srv},
		Sessions: []visorapi.DmsgServerSession{{PK: srv, Protocol: "quic"}},
	}})
	require.Equal(t, 1, n)
	out := renderEN(t, DmsgPage(Dmsg{PK: pk.Hex(), Clients: cl, Count: n}))
	require.Contains(t, out, "Main visor")
	require.Contains(t, out, srv.Hex())
	require.Contains(t, out, "quic")
	require.Contains(t, out, `name="count"`)
	require.Contains(t, out, `value="1"`)
}

func TestUptimeOf(t *testing.T) {
	now := time.Date(2026, 11, 3, 1, 0, 0, 0, time.UTC) // slot 12
	today := strings.Repeat(".", 6) + strings.Repeat(" ", 6)
	days, sum := UptimeOf(&visorapi.LocalUptimeResponse{Tiers: map[string]map[string]string{
		"process": {"2026-11-03": today, "2026-11-02": strings.Repeat(".", slotsPerDay)},
	}}, now)
	require.Len(t, days, 2)
	require.True(t, days[0].Today)
	p := days[0].Tiers[0]
	require.Equal(t, []string{"on", "off", "future"}, []string{p.Runs[0].State, p.Runs[1].State, p.Runs[2].State})
	require.InDelta(t, 50, p.Pct, 0.01)
	require.True(t, days[0].Tiers[1].Empty)
	require.InDelta(t, float64(slotsPerDay+6)/float64(slotsPerDay+12)*100, sum[0].Pct, 0.01)
	out := renderEN(t, UptimeBars(Uptime{PK: "pk", Days: 7, List: days, Summary: sum}))
	require.Contains(t, out, "flex-grow:6")
	require.Contains(t, out, "Process")
}

func TestRewardHistory(t *testing.T) {
	list, total, err := ParseRewardHistory([]byte(`{"history":[{"date":"2026-11-02","amount":1.5,"share":0.1,"sent":true,"txid":"abcdef0123456789"},{"date":"2026-11-01","amount":0}]}`))
	require.NoError(t, err)
	require.InDelta(t, 1.5, total, 0.001)
	out := renderEN(t, RewardHistoryView(RewardHistory{PK: "pk", Days: 7, List: list, Total: total}))
	require.Contains(t, out, "Total: 1.50 SKY")
	require.Contains(t, out, "Nov 2, 2026")
	require.Contains(t, out, "abcdef012345…")
	require.Contains(t, out, "No reward")

	days, err := ParseServiceUptimes([]byte(`[{"pk":"AB","daily":{"2026-11-01":"99.5","2026-11-02":100}}]`), "ab")
	require.NoError(t, err)
	require.Equal(t, "2026-11-02", days[0].Name)
	require.InDelta(t, 99.5, days[1].Pct, 0.01)
}
