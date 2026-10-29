package visor

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// fakeLevers records the switch positions a mode leaves.
type fakeLevers struct {
	mu          sync.Mutex
	autoconnect bool // paused
	transit     bool // paused
	relay       bool // paused
	restrict    transport.CreateRestriction
	closed      int // closeDirect calls
	dmsgCap     int
	dmsgDial    bool // paused
}

func (f *fakeLevers) pauseAutoconnect(on bool) { f.mu.Lock(); f.autoconnect = on; f.mu.Unlock() }
func (f *fakeLevers) pauseTransit(on bool)     { f.mu.Lock(); f.transit = on; f.mu.Unlock() }
func (f *fakeLevers) pauseRelay(on bool)       { f.mu.Lock(); f.relay = on; f.mu.Unlock() }
func (f *fakeLevers) restrictCreate(r transport.CreateRestriction) {
	f.mu.Lock()
	f.restrict = r
	f.mu.Unlock()
}
func (f *fakeLevers) closeDirect() int      { f.mu.Lock(); f.closed++; f.mu.Unlock(); return 0 }
func (f *fakeLevers) capDmsgSessions(n int) { f.mu.Lock(); f.dmsgCap = n; f.mu.Unlock() }
func (f *fakeLevers) pauseDmsgDial(on bool) { f.mu.Lock(); f.dmsgDial = on; f.mu.Unlock() }
func (f *fakeLevers) snapshot() fakeLevers {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeLevers{autoconnect: f.autoconnect, transit: f.transit, relay: f.relay,
		restrict: f.restrict, closed: f.closed, dmsgCap: f.dmsgCap, dmsgDial: f.dmsgDial}
}

func meterVisor(t *testing.T) (*Visor, *fakeLevers) {
	t.Helper()
	resetAdvertisePolicy(t)
	conf, _ := newTestConfig(t)
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}
	f := &fakeLevers{}
	v.meter.levers = f
	v.meter.routes = func() []netutil.IfaceAddr { return nil }
	return v, f
}

func boolp(b bool) *bool { return &b }

func TestMeteredModesPullTheirLevers(t *testing.T) {
	v, f := meterVisor(t)
	set := func(mode string) {
		t.Helper()
		require.NoError(t, v.NetworkSet(visorapi.NetworkSettings{MeteredMode: &mode}))
	}
	on := "true"
	require.NoError(t, v.NetworkSet(visorapi.NetworkSettings{Metered: &on}))
	require.Equal(t, visorapi.ModeLean, v.NetworkState().Mode, "a metered network defaults to lean")
	l := f.snapshot()
	require.True(t, l.autoconnect && l.transit && l.relay)
	require.Equal(t, transport.RestrictNone, l.restrict, "lean keeps creating transports")
	require.Zero(t, l.closed, "lean keeps the transports it has")
	require.Zero(t, l.dmsgCap)
	require.False(t, l.dmsgDial)
	require.True(t, v.meter.lean.Load())

	set(visorapi.ModeDmsgOnly)
	l = f.snapshot()
	require.Equal(t, transport.RestrictDirect, l.restrict)
	require.Equal(t, 1, l.closed, "entering dmsg-only closes the direct transports")
	require.Equal(t, dmsgOnlySessions, l.dmsgCap)
	require.False(t, l.dmsgDial)

	set(visorapi.ModeOffline)
	l = f.snapshot()
	require.Equal(t, transport.RestrictAll, l.restrict)
	require.Equal(t, 1, l.closed, "already closed when coming from dmsg-only")
	require.Zero(t, l.dmsgCap, "offline restores the cap but dials nothing")
	require.True(t, l.dmsgDial)

	off := "false"
	require.NoError(t, v.NetworkSet(visorapi.NetworkSettings{Metered: &off}))
	st := v.NetworkState()
	require.Equal(t, visorapi.ModeNormal, st.Mode)
	require.Equal(t, visorapi.ModeOffline, st.MeteredMode, "the configured mode is kept for next time")
	l = f.snapshot()
	require.False(t, l.autoconnect || l.transit || l.relay || l.dmsgDial)
	require.Equal(t, transport.RestrictNone, l.restrict)
	require.False(t, v.meter.lean.Load())

	require.Error(t, v.NetworkSet(visorapi.NetworkSettings{MeteredMode: strPtr("frugal")}))
	require.Error(t, v.NetworkSet(visorapi.NetworkSettings{Metered: strPtr("maybe")}))
}

func strPtr(s string) *string { return &s }

// The operator's override beats an app's report, which beats the rule, and
// "auto" hands control back.
func TestMeteredSignalPrecedence(t *testing.T) {
	v, _ := meterVisor(t)
	route := "wlan0"
	v.meter.routes = func() []netutil.IfaceAddr {
		return []netutil.IfaceAddr{{Name: route, IP: net.ParseIP("10.0.0.2")}}
	}
	ifs := []string{"wwan*"}
	require.NoError(t, v.NetworkSet(visorapi.NetworkSettings{MeteredInterfaces: &ifs}))
	require.Equal(t, visorapi.ModeNormal, v.NetworkState().Mode)

	route = "wwan0"
	v.meterEvaluate()
	st := v.NetworkState()
	require.True(t, st.Metered)
	require.Equal(t, "rule wwan0", st.MeteredSource)
	require.Equal(t, visorapi.ModeLean, st.Mode)

	require.NoError(t, v.NetworkSet(visorapi.NetworkSettings{Metered: strPtr("false")}))
	require.Equal(t, visorapi.ModeNormal, v.NetworkState().Mode, "the override beats the rule")

	require.NoError(t, v.NetworkSet(visorapi.NetworkSettings{Metered: strPtr("auto")}))
	require.Equal(t, visorapi.ModeLean, v.NetworkState().Mode, "auto follows the rule again")

	route = "wlan0"
	require.NoError(t, v.NetworkEvent(visorapi.NetworkEvent{Event: "changed", Metered: boolp(true)}))
	st = v.NetworkState()
	require.Equal(t, "android", st.MeteredSource)
	require.Equal(t, visorapi.ModeLean, st.Mode)

	require.NoError(t, v.NetworkEvent(visorapi.NetworkEvent{Event: "changed", Metered: boolp(false)}))
	require.Equal(t, visorapi.ModeNormal, v.NetworkState().Mode)

	// A range entry matches the route's source address.
	cidr := []string{"10.0.0.0/8"}
	require.NoError(t, v.NetworkSet(visorapi.NetworkSettings{MeteredInterfaces: &cidr}))
	require.Equal(t, visorapi.ModeLean, v.NetworkState().Mode)
}

func TestMeteredModePersists(t *testing.T) {
	v, _ := meterVisor(t)
	mode := visorapi.ModeDmsgOnly
	ifs := []string{"rmnet*"}
	require.NoError(t, v.NetworkSet(visorapi.NetworkSettings{MeteredMode: &mode, MeteredInterfaces: &ifs}))
	require.Equal(t, &visorconfig.NetworkPolicy{Metered: mode, MeteredInterfaces: ifs}, v.conf.Transport.Network)

	// Changing the deny list keeps the metered settings.
	deny := []string{"docker*"}
	require.NoError(t, v.NetworkSet(visorapi.NetworkSettings{Deny: &deny}))
	require.Equal(t, mode, v.conf.Transport.Network.Metered)
	require.Equal(t, ifs, v.conf.Transport.Network.MeteredInterfaces)
}

// An app's "moved" event runs the full network refresh once.
func TestNetworkEventMovedRefreshes(t *testing.T) {
	fastNetMove(t)
	v, _ := meterVisor(t)
	ran := make(chan string, 4)
	v.netMove.refresh = func(r string) { ran <- r }
	require.NoError(t, v.NetworkEvent(visorapi.NetworkEvent{Event: "moved", Interface: "wlan0"}))
	require.Equal(t, "android", <-ran)
	require.Error(t, v.NetworkEvent(visorapi.NetworkEvent{Event: "exploded"}))
}

// Lean refuses new automatic inbound transports like resource pressure does,
// and still takes the operator's own.
func TestLeanRefusesAutomaticInbound(t *testing.T) {
	v, _ := meterVisor(t)
	pk, _ := cipher.GenerateKeyPair()
	require.True(t, v.inboundGate(pk, transport.LabelAutomatic))
	require.NoError(t, v.NetworkSet(visorapi.NetworkSettings{Metered: strPtr("true")}))
	require.False(t, v.inboundGate(pk, transport.LabelAutomatic))
	require.True(t, v.inboundGate(pk, transport.LabelUser))
}

func TestPostNetworkEndpoint(t *testing.T) {
	fastNetMove(t)
	v, _ := meterVisor(t)
	refreshed := make(chan string, 1)
	v.netMove.refresh = func(r string) { refreshed <- r }
	hv := &Hypervisor{visor: v, logger: logging.MustGetLogger("hv-test")}

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/network", strings.NewReader(body))
		rec := httptest.NewRecorder()
		hv.postNetwork()(rec, req)
		return rec
	}

	rec := post(`{"event":"moved","metered":true,"ifname":"rmnet_data0"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var st visorapi.NetworkState
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
	require.True(t, st.Metered)
	require.Equal(t, visorapi.ModeLean, st.Mode)
	require.Equal(t, "android", <-refreshed, "a move runs one refresh")

	rec = post(`{"event":"changed","metered":false}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
	require.Equal(t, visorapi.ModeNormal, st.Mode)
	select {
	case r := <-refreshed:
		t.Fatalf("a property change must not refresh, got %q", r)
	default:
	}

	require.Equal(t, http.StatusBadRequest, post(`{"event":"bogus"}`).Code)
	require.Equal(t, http.StatusBadRequest, post(`not json`).Code)
}
