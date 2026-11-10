package visor

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/rpc"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/alerts"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func alertsTestVisor(t *testing.T) *Visor {
	pk, _ := cipher.GenerateKeyPair()
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: pk}, LocalPath: t.TempDir()}, opts: Options{NoCSRF: true}}
	svc, err := alerts.NewService(v.conf.LocalPath, nil, &alerts.Sender{Watcher: pk})
	require.NoError(t, err)
	svc.Targets = v.alertTargets
	v.alertSvc = svc
	return v
}

func TestHypervisorAlertTargets(t *testing.T) {
	hv := newKnownTestHV(t.TempDir())
	live, _ := cipher.GenerateKeyPair()
	stale, _ := cipher.GenerateKeyPair()
	gone, _ := cipher.GenerateKeyPair()
	fresh, _ := cipher.GenerateKeyPair()
	now := time.Now()
	hv.summaryCache[live] = cachedSummary{sum: knownVisorSummary(knownVisor{PK: live, Hostname: "a", PublicIP: "203.0.113.1"}), seenAt: now.Add(-20 * time.Second)}
	hv.summaryCache[stale] = cachedSummary{sum: knownVisorSummary(knownVisor{PK: stale}), seenAt: now.Add(-5 * time.Minute)}
	hv.summaryCache[gone] = cachedSummary{sum: knownVisorSummary(knownVisor{PK: gone}), seenAt: now.Add(-10 * time.Second)}
	hv.remoteVisors[live] = Conn{}
	hv.remoteVisors[stale] = Conn{}
	hv.remoteVisors[fresh] = Conn{}

	got := map[cipher.PubKey]alerts.Target{}
	for _, tg := range hv.alertTargets(now) {
		got[tg.PK] = tg
	}
	require.Len(t, got, 4)
	require.True(t, got[live].Online)
	require.Equal(t, "a", got[live].Hostname)
	require.Equal(t, "203.0.113.1", got[live].PublicIP)
	require.False(t, got[stale].Online, "connected but not answering the poll")
	require.False(t, got[gone].Online, "disconnected")
	require.True(t, got[fresh].Online, "just connected, no summary yet")
}

func TestAlertsOverRPCNeverReturnPassword(t *testing.T) {
	v := alertsTestVisor(t)
	srv := rpc.NewServer()
	require.NoError(t, srv.RegisterName(visorapi.RPCPrefix, &RPC{visor: v, log: logging.New()}))
	sc, cc := net.Pipe()
	go srv.ServeConn(sc)
	api := visorapi.NewRPCClient(nil, cc, visorapi.RPCPrefix, 30*time.Second)

	watch, _ := cipher.GenerateKeyPair()
	set := alerts.Settings{
		EmailTo:      []string{"ops@example.com"},
		OfflineAfter: alerts.Duration(15 * time.Minute),
		SMTP:         &alerts.SMTP{Host: "smtp.example.com", Port: 587, Username: "u", Password: "hunter2", StartTLS: true},
		Watch:        []cipher.PubKey{watch},
	}
	require.NoError(t, api.AlertsSet(set))
	st, err := api.AlertsStatus()
	require.NoError(t, err)
	require.Equal(t, "15m0s", st.OfflineAfter)
	require.Empty(t, st.Settings.SMTP.Password)
	require.True(t, st.Settings.SMTP.PasswordSet)
	require.Equal(t, []cipher.PubKey{watch}, st.Settings.Watch)
	require.Equal(t, "hunter2", v.alertSvc.Settings().SMTP.Password)

	// Writing back what was read keeps the password.
	require.NoError(t, api.AlertsSet(st.Settings))
	require.Equal(t, "hunter2", v.alertSvc.Settings().SMTP.Password)

	bad := set
	bad.OfflineAfter = alerts.Duration(2 * time.Hour)
	require.Error(t, api.AlertsSet(bad))

	// The watched key with no dmsg client counts as offline.
	ts := v.alertTargets(context.Background(), v.alertSvc.Settings())
	require.Len(t, ts, 1)
	require.Equal(t, watch, ts[0].PK)
	require.False(t, ts[0].Online)
	require.Equal(t, "watch", ts[0].Source)
}

func TestAlertsLANProbeOverRPC(t *testing.T) {
	v := alertsTestVisor(t)
	srv := rpc.NewServer()
	require.NoError(t, srv.RegisterName(visorapi.RPCPrefix, &RPC{visor: v, log: logging.New()}))
	sc, cc := net.Pipe()
	go srv.ServeConn(sc)
	api := visorapi.NewRPCClient(nil, cc, visorapi.RPCPrefix, 30*time.Second)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	old := alerts.ProbePorts
	t.Cleanup(func() { alerts.ProbePorts = old })
	alerts.ProbePorts = []int{port}

	ok, err := api.AlertsLANProbe("127.0.0.1")
	require.NoError(t, err)
	require.True(t, ok, "a listening port answers")

	require.NoError(t, ln.Close())
	ok, err = api.AlertsLANProbe("127.0.0.1")
	require.NoError(t, err)
	require.True(t, ok, "a refused connection still proves the host is up")

	_, err = api.AlertsLANProbe("8.8.8.8")
	require.ErrorContains(t, err, "not a LAN address")
	_, err = api.AlertsLANProbe("nope")
	require.Error(t, err)

	// The watcher probes from itself when it is the neighbor.
	ok, err = v.alertProbe(context.Background(), v.conf.PK, "127.0.0.1")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestHypervisorAlertTargetsHealth(t *testing.T) {
	hv := newKnownTestHV(t.TempDir())
	pk, _ := cipher.GenerateKeyPair()
	now := time.Now()
	sum := knownVisorSummary(knownVisor{PK: pk, PublicIP: "203.0.113.1"})
	sum.Overview.LocalIP = "192.168.0.5"
	sum.Uptime = 60
	sum.Load = &visorapi.LoadStats{HostUptime: 3600, DiskUsedPercent: 93, RootReadOnly: true, StorageIOErrors: 2, LastStorageIOError: "I/O error, dev mmcblk0"}
	hv.summaryCache[pk] = cachedSummary{sum: sum, seenAt: now.Add(-10 * time.Second)}
	hv.remoteVisors[pk] = Conn{}

	ts := hv.alertTargets(now)
	require.Len(t, ts, 1)
	require.Equal(t, "192.168.0.5", ts[0].LocalIP)
	require.Equal(t, alerts.Duration(3610*time.Second), ts[0].Uptime, "host uptime, aged to now")
	require.Equal(t, &alerts.Storage{ReadOnlyRoot: true, IOErrors: 2, LastIOError: "I/O error, dev mmcblk0", DiskUsedPercent: 93}, ts[0].Storage)
}

func TestAlertsHTTP(t *testing.T) {
	v := alertsTestVisor(t)
	hv := newKnownTestHV(t.TempDir())
	hv.visor = v
	hv.c.PK = v.conf.PK
	hv.selfConn = Conn{API: v}
	v.hvInstance = hv

	mux := http.NewServeMux()
	mux.HandleFunc("GET /visors/{pk}/alerts", hv.getAlerts())
	mux.HandleFunc("PUT /visors/{pk}/alerts", hv.putAlerts())
	mux.HandleFunc("POST /visors/{pk}/alerts/test", hv.postAlertsTest())
	base := "/visors/" + v.conf.PK.Hex() + "/alerts"

	body := `{"email_to":["ops@example.com"],"offline_after":"1h","smtp":{"host":"smtp.example.com","password":"hunter2"}}`
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, base, strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.NotContains(t, rr.Body.String(), "hunter2")

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, base, nil))
	require.Equal(t, http.StatusOK, rr.Code)
	require.NotContains(t, rr.Body.String(), "hunter2")
	var st alerts.Status
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &st))
	require.Equal(t, "1h0m0s", st.OfflineAfter)
	require.True(t, st.Settings.SMTP.PasswordSet)

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, base, strings.NewReader(`{"email_to":["x@example.com"],"offline_after":"30s"}`)))
	require.Equal(t, http.StatusBadRequest, rr.Code, "below the 1m minimum")
}

func TestAlertsPushHTTP(t *testing.T) {
	v := alertsTestVisor(t)
	hv := newKnownTestHV(t.TempDir())
	hv.visor = v
	hv.c.PK = v.conf.PK
	hv.selfConn = Conn{API: v}
	v.hvInstance = hv

	mux := http.NewServeMux()
	mux.HandleFunc("GET /visors/{pk}/alerts", hv.getAlerts())
	mux.HandleFunc("POST /visors/{pk}/alerts/push", hv.postAlertsPush())
	mux.HandleFunc("DELETE /visors/{pk}/alerts/push/{id}", hv.deleteAlertsPush())
	base := "/visors/" + v.conf.PK.Hex() + "/alerts"

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, base, nil))
	var st alerts.Status
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &st))
	require.NotEmpty(t, st.PushPublicKey)

	// What the browser's PushSubscription.toJSON() gives, plus a label.
	sub := `{"endpoint":"https://fcm.googleapis.com/fcm/send/abc","expirationTime":null,"keys":{"p256dh":"BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4","auth":"BTBZMqHH6r4Tts7J_aSIgg"},"label":"phone"}`
	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, base+"/push", strings.NewReader(sub)))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.NotContains(t, rr.Body.String(), "BTBZMqHH6r4Tts7J_aSIgg")
	var got alerts.PushSubscription
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, alerts.PushID("https://fcm.googleapis.com/fcm/send/abc"), got.ID)

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, base, nil))
	require.NotContains(t, rr.Body.String(), "BTBZMqHH6r4Tts7J_aSIgg")
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &st))
	require.True(t, st.Enabled)
	require.Len(t, st.Settings.Push, 1)
	require.Equal(t, "phone", st.Settings.Push[0].Label)

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, base+"/push", strings.NewReader(`{"endpoint":"http://x/y","keys":{}}`)))
	require.Equal(t, http.StatusBadRequest, rr.Code)

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, base+"/push/"+got.ID, nil))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Empty(t, v.alertSvc.Settings().Push)
}
