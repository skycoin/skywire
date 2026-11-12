package ui2

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/appserver/spec"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func renderString(t *testing.T, f func(ctx context.Context, b *strings.Builder) error) string {
	t.Helper()
	ctx := With(context.Background(), Page{Base: "../../../", Lang: "en"}, NewCatalog(nil))
	var b strings.Builder
	require.NoError(t, f(ctx, &b))
	return b.String()
}

func TestAppsOf(t *testing.T) {
	states := []*appserver.AppState{
		{AppConfig: spec.AppConfig{Name: "skysocks-client-2", Port: 13, Args: []string{"-srv", "a b"}}, Status: appserver.AppStatusRunning},
		{AppConfig: spec.AppConfig{Name: "mine", AutoStart: true, Env: []string{"A=1", "B=2"}}, Status: appserver.AppStatusErrored, DetailedStatus: "boom"},
		{AppConfig: spec.AppConfig{Name: "skychat"}},
	}
	a := AppsOf("pk", states)
	require.Equal(t, []string{"skychat", "skysocks-client-2"}, []string{a.Official[0].Name, a.Official[1].Name})
	require.Equal(t, "mine", a.Other[0].Name)
	require.Equal(t, `-srv "a b"`, a.Official[1].Args)
	require.Equal(t, "A=1\nB=2", a.Other[0].Env)
	require.Equal(t, []string{"A=1", "B=2"}, EnvLines(" A=1\n\n B=2 \n"))
	require.True(t, a.Official[1].Running())

	out := renderString(t, func(ctx context.Context, b *strings.Builder) error { return AppsRows(a).Render(ctx, b) })
	require.Contains(t, out, `hx-put="visors/pk/apps/skysocks-client-2/state"`)
	require.Contains(t, out, `{"run":""}`, "a running app offers stop")
	require.Contains(t, out, `{"run":"1"}`, "a stopped app offers start")
	require.Contains(t, out, "boom")

	x, ok := a.Find("mine")
	require.True(t, ok)
	out = renderString(t, func(ctx context.Context, b *strings.Builder) error { return AppSettings("pk", x, "").Render(ctx, b) })
	require.Contains(t, out, `hx-put="visors/pk/apps/mine/settings"`)
	require.Contains(t, out, `name="autostart" value="1" checked`)
}

func TestTransportsOf(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	sent, recv := uint64(2048), uint64(10)
	tps := []*visorapi.TransportSummary{
		{ID: uuid.New(), Remote: a, Type: "dmsg", LatencyMS: 12.4, Log: &transport.LogEntry{SentBytes: &sent, RecvBytes: &recv}},
	}
	pts := []transport.PersistentTransports{{PK: a, NetType: "dmsg"}, {PK: b, NetType: "stcpr"}}
	tv := TransportsOf("pk", tps, pts)
	require.Len(t, tv.List, 2)
	require.True(t, tv.List[0].Persistent)
	require.Equal(t, "12 ms", tv.List[0].Latency)
	require.Equal(t, "2.0 KiB", tv.List[0].Sent)
	require.True(t, tv.List[1].Offline, "a persistent transport that is down is listed")
	require.Equal(t, 1, tv.Online())
	require.Equal(t, []TypeCount{{"dmsg", 1}}, tv.ByType)

	out := renderString(t, func(ctx context.Context, b *strings.Builder) error { return TransportsBody(tv).Render(ctx, b) })
	require.Contains(t, out, b.Hex(), "keys are shown whole")
	require.Contains(t, out, `hx-delete="visors/pk/transports/`+tps[0].ID.String()+`"`)
	require.Contains(t, out, "transports.offline")
}

func TestRulesOf(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	tp := uuid.New()
	rules := []routing.Rule{
		routing.IntermediaryForwardRule(time.Minute, 9, 4, tp),
		routing.ConsumeRule(time.Minute, 2, a, b, 3, 5),
	}
	rs := RulesOf(rules)
	require.Equal(t, "2", rs[0].Key)
	require.Equal(t, b.Hex(), rs[0].RemotePK)
	require.Equal(t, "5", rs[0].RemotePort)
	require.Equal(t, tp.String(), rs[1].NextTP)
	require.Equal(t, "4", rs[1].NextRID)
}
