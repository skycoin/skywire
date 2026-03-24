// rpc_client_mock.go contains the mock RPC client for testing.
package visor

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/util/cipherutil"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// MockRPCClient mocks API.
type mockRPCClient struct {
	startedAt time.Time
	o         *Overview
	tpTypes   []types.Type
	rt        routing.Table
	logS      appcommon.LogStore
	sync.RWMutex
}

// NewMockRPCClient creates a new mock API.
func NewMockRPCClient(r *rand.Rand, maxTps int, maxRules int) (cipher.PubKey, API, error) {
	log := logging.MustGetLogger("mock-rpc-client")

	types := []types.Type{"messaging", "native"}
	localPK, _ := cipher.GenerateKeyPair()

	log.Infof("generating mock client with: localPK(%s) maxTps(%d) maxRules(%d)", localPK, maxTps, maxRules)

	tps := make([]*TransportSummary, r.Intn(maxTps+1))
	for i := range tps {
		remotePK, _ := cipher.GenerateKeyPair()
		tps[i] = &TransportSummary{
			ID:     transport.MakeTransportID(localPK, remotePK, types[r.Int()%len(types)]),
			Local:  localPK,
			Remote: remotePK,
			Type:   types[r.Int()%len(types)],
			Log:    transport.NewLogEntry(),
		}
		log.Infof("tp[%2d]: %v", i, tps[i])
	}

	rt := routing.NewTable(log)
	ruleKeepAlive := router.DefaultRouteKeepAlive

	for i := 0; i < r.Intn(maxRules+1); i++ {
		remotePK, _ := cipher.GenerateKeyPair()
		var lpRaw, rpRaw [2]byte

		if _, err := r.Read(lpRaw[:]); err != nil {
			return cipher.PubKey{}, nil, err
		}

		if _, err := r.Read(rpRaw[:]); err != nil {
			return cipher.PubKey{}, nil, err
		}

		lp := routing.Port(binary.BigEndian.Uint16(lpRaw[:]))
		rp := routing.Port(binary.BigEndian.Uint16(rpRaw[:]))

		fwdRID, err := rt.ReserveKeys(1)
		if err != nil {
			panic(err)
		}

		keys := cipherutil.GenKeyPairs(2)

		fwdRule := routing.ForwardRule(ruleKeepAlive, fwdRID[0], routing.RouteID(r.Uint32()), uuid.New(), keys[0].PK, keys[1].PK, 0, 0)
		if err := rt.SaveRule(fwdRule); err != nil {
			panic(err)
		}

		appRID, err := rt.ReserveKeys(1)
		if err != nil {
			panic(err)
		}

		consumeRule := routing.ConsumeRule(ruleKeepAlive, appRID[0], localPK, remotePK, lp, rp)
		if err := rt.SaveRule(consumeRule); err != nil {
			panic(err)
		}

		log.Infof("rt[%2da]: %v %v", i, fwdRID, fwdRule.Summary().ForwardFields)
		log.Infof("rt[%2db]: %v %v", i, appRID[0], consumeRule.Summary().ConsumeFields)
	}

	log.Printf("rtCount: %d", rt.Count())

	client := &mockRPCClient{
		o: &Overview{
			PubKey:          localPK,
			BuildInfo:       buildinfo.Get(),
			AppProtoVersion: supportedProtocolVersion,
			Apps: []*appserver.AppState{
				{AppConfig: appserver.AppConfig{Name: "foo.v1.0", Binary: "foo.v1.0", AutoStart: false, Port: 10}},
				{AppConfig: appserver.AppConfig{Name: "bar.v2.0", Binary: "bar.v2.0", AutoStart: false, Port: 20}},
			},
			Transports:  tps,
			RoutesCount: rt.Count(),
		},
		tpTypes:   types,
		rt:        rt,
		startedAt: time.Now(),
	}

	return localPK, client, nil
}

func (mc *mockRPCClient) do(write bool, f func() error) error {
	if write {
		mc.Lock()
		defer mc.Unlock()
	} else {
		mc.RLock()
		defer mc.RUnlock()
	}
	return f()
}

// Overview implements API.
func (mc *mockRPCClient) Overview() (*Overview, error) {
	var out Overview
	err := mc.do(false, func() error {
		out = *mc.o
		for _, a := range mc.o.Apps {
			a := a
			out.Apps = append(out.Apps, a)
		}
		for _, tp := range mc.o.Transports {
			tp := tp
			out.Transports = append(out.Transports, tp)
		}
		out.RoutesCount = mc.o.RoutesCount
		return nil
	})
	return &out, err
}

// Summary implements API.
func (mc *mockRPCClient) Summary() (*Summary, error) {
	overview, err := mc.Overview()
	if err != nil {
		return nil, err
	}

	health, err := mc.Health()
	if err != nil {
		return nil, err
	}

	uptime, err := mc.Uptime()
	if err != nil {
		return nil, err
	}

	routes, err := mc.RoutingRules()
	if err != nil {
		return nil, err
	}

	extraRoutes := make([]routingRuleResp, 0, len(routes))
	for _, route := range routes {
		extraRoutes = append(extraRoutes, routingRuleResp{
			Key:     route.KeyRouteID(),
			Rule:    hex.EncodeToString(route),
			Summary: route.Summary(),
		})
	}

	summary := &Summary{
		Overview: overview,
		Health:   health,
		Uptime:   uptime,
		Routes:   extraRoutes,
	}

	return summary, nil
}

// Health implements API
func (mc *mockRPCClient) Health() (*HealthInfo, error) {
	hi := &HealthInfo{
		ServicesHealth: "healthy",
	}

	return hi, nil
}

// Uptime implements API
func (mc *mockRPCClient) Uptime() (float64, error) {
	return time.Since(mc.startedAt).Seconds(), nil
}

// SetRewardAddress implements API
func (mc *mockRPCClient) SetRewardAddress(_ string) (string, error) {
	return "", nil
}

// GetRewardAddress implements API.
func (mc *mockRPCClient) GetRewardAddress() (string, error) {
	return "", nil
}

// DeleteRewardAddress implements API.
func (mc *mockRPCClient) DeleteRewardAddress() error {
	return nil
}

// Apps implements API.
func (mc *mockRPCClient) Apps() ([]*appserver.AppState, error) {
	var apps []*appserver.AppState
	err := mc.do(false, func() error {
		for _, a := range mc.o.Apps {
			a := a
			apps = append(apps, a)
		}
		return nil
	})
	return apps, err
}

// App implements API.
func (mc *mockRPCClient) App(appName string) (*appserver.AppState, error) {
	var app *appserver.AppState
	err := mc.do(false, func() error {
		for _, a := range mc.o.Apps {
			if a.Name == appName {
				app = a
				break
			}
		}
		return nil
	})
	return app, err
}

// StartApp implements API.
func (*mockRPCClient) StartApp(string) error {
	return nil
}

// StartAppWithMode implements API.
func (*mockRPCClient) StartAppWithMode(string, string) error {
	return nil
}

// AddApp implement API.
func (*mockRPCClient) AddApp(string, string) error {
	return nil
}

// RegisterApp implements API.
func (*mockRPCClient) RegisterApp(appcommon.ProcConfig) (appcommon.ProcKey, error) {
	return appcommon.ProcKey{}, nil
}

// DeregisterApp implements API.
func (*mockRPCClient) DeregisterApp(appcommon.ProcKey) error {
	return nil
}

// StopApp implements API.
func (*mockRPCClient) StopApp(string) error {
	return nil
}

// KillApp implements API.
func (*mockRPCClient) KillApp(string) error {
	return nil
}

// StartVPNClient implements API.
func (*mockRPCClient) StartVPNClient(cipher.PubKey) error {
	return nil
}

// StartVPNClientWithMode implements API.
func (*mockRPCClient) StartVPNClientWithMode(cipher.PubKey, string) error {
	return nil
}

// StopVPNClient implements API.
func (*mockRPCClient) StopVPNClient(string) error {
	return nil
}

// FetchUptimeTrackerData implements API.
func (*mockRPCClient) FetchUptimeTrackerData(string) ([]byte, error) {
	return []byte{}, nil
}

// StartSkysocksClient implements API.
func (*mockRPCClient) StartSkysocksClient(string) error {
	return nil
}

// StopSkysocksClients implements API.
func (*mockRPCClient) StopSkysocksClients() error {
	return nil
}

// SetAppDetailedStatus sets app's detailed state.
func (mc *mockRPCClient) SetAppDetailedStatus(appName, status string) error {
	return mc.do(true, func() error {
		for _, a := range mc.o.Apps {
			if a.Name == appName {
				a.DetailedStatus = status
				return nil
			}
		}

		return fmt.Errorf("app of name '%s' does not exist", appName)
	})
}

// SetAppError sets app's error.
func (mc *mockRPCClient) SetAppError(appName, appErr string) error {
	return mc.do(true, func() error {
		for _, a := range mc.o.Apps {
			if a.Name == appName {
				a.DetailedStatus = appErr
				return nil
			}
		}

		return fmt.Errorf("app of name '%s' does not exist", appName)
	})
}

// RestartApp implements API.
func (*mockRPCClient) RestartApp(string) error {
	return nil
}

// SetAutoStart implements API.
func (mc *mockRPCClient) SetAutoStart(appName string, autostart bool) error {
	return mc.do(true, func() error {
		for _, a := range mc.o.Apps {
			if a.Name == appName {
				a.AutoStart = autostart
				return nil
			}
		}
		return fmt.Errorf("app of name '%s' does not exist", appName)
	})
}

// SetAppPassword implements API.
func (mc *mockRPCClient) SetAppPassword(string, string) error {
	return mc.do(true, func() error {
		const socksName = "skysocks"

		for i := range mc.o.Apps {
			if mc.o.Apps[i].Name == socksName {
				return nil
			}
		}

		return fmt.Errorf("app of name '%s' does not exist", socksName)
	})
}

// SetAppPassword implements API.
func (mc *mockRPCClient) SetAppNetworkInterface(string, string) error {
	return mc.do(true, func() error {
		const vpnServerName = "vpn-server"

		for i := range mc.o.Apps {
			if mc.o.Apps[i].Name == vpnServerName {
				return nil
			}
		}

		return fmt.Errorf("app of name '%s' does not exist", vpnServerName)
	})
}

// SetAppPK implements API.
func (mc *mockRPCClient) SetAppPK(string, cipher.PubKey) error {
	return mc.do(true, func() error {
		const socksName = "skysocks-client"

		for i := range mc.o.Apps {
			if mc.o.Apps[i].Name == socksName {
				return nil
			}
		}

		return fmt.Errorf("app of name '%s' does not exist", socksName)
	})
}

// SetAppKillswitch implements API.
func (mc *mockRPCClient) SetAppKillswitch(_ string, _ bool) error {
	return mc.do(true, func() error {
		const socksName = "skysocks"

		for i := range mc.o.Apps {
			if mc.o.Apps[i].Name == socksName {
				return nil
			}
		}

		return fmt.Errorf("app of name '%s' does not exist", socksName)
	})
}

// SetAppSecure implements API.
func (mc *mockRPCClient) SetAppSecure(_ string, _ bool) error {
	return mc.do(true, func() error {
		const socksName = "skysocks"

		for i := range mc.o.Apps {
			if mc.o.Apps[i].Name == socksName {
				return nil
			}
		}

		return fmt.Errorf("app of name '%s' does not exist", socksName)
	})
}

// SetAppAddress implements API.
func (mc *mockRPCClient) SetAppAddress(_ string, _ string) error {
	return mc.do(true, func() error {
		const chatName = "skychat"

		for i := range mc.o.Apps {
			if mc.o.Apps[i].Name == chatName {
				return nil
			}
		}

		return fmt.Errorf("app of name '%s' does not exist", chatName)
	})
}

// SetAppDNS implements API.
func (mc *mockRPCClient) SetAppDNS(string, string) error {
	return mc.do(true, func() error {
		const socksName = "vpn-client"

		for i := range mc.o.Apps {
			if mc.o.Apps[i].Name == socksName {
				return nil
			}
		}

		return fmt.Errorf("app of name '%s' does not exist", socksName)
	})
}

// DoCustomSetting implents API.
func (mc *mockRPCClient) DoCustomSetting(appName string, _ map[string]any) error {
	return mc.do(true, func() error {
		for i := range mc.o.Apps {
			if mc.o.Apps[i].Name == appName {
				return nil
			}
		}

		return fmt.Errorf("app of name '%s' does not exist", appName)
	})
}

// LogsSince implements API. Manually set (*mockRPPClient).logS before calling this function
func (mc *mockRPCClient) LogsSince(timestamp time.Time, _ string) ([]string, error) {
	return mc.logS.LogsSince(timestamp)
}

func (mc *mockRPCClient) GetAppStats(_ string) (appserver.AppStats, error) {
	return appserver.AppStats{}, nil
}

func (mc *mockRPCClient) GetAppError(_ string) (string, error) {
	return "", nil
}

// GetAppConnectionsSummary get connections stats for the app.
func (mc *mockRPCClient) GetAppConnectionsSummary(_ string) ([]appserver.ConnectionSummary, error) {
	return nil, nil
}

// TransportTypes implements API.
func (mc *mockRPCClient) TransportTypes() ([]string, error) {
	var res []string
	for _, tptype := range mc.tpTypes {
		res = append(res, string(tptype))
	}
	return res, nil
}

// Transports implements API.
func (mc *mockRPCClient) Transports(types []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error) {
	var summaries []*TransportSummary
	err := mc.do(false, func() error {
		for _, tp := range mc.o.Transports {
			tp := tp
			if types != nil {
				for _, reqT := range types {
					if string(tp.Type) == reqT {
						goto TypeOK
					}
				}
				continue
			}
		TypeOK:
			if pks != nil {
				for _, reqPK := range pks {
					if tp.Remote == reqPK || tp.Local == reqPK {
						goto PubKeyOK
					}
				}
				continue
			}
		PubKeyOK:
			if !logs {
				temp := *tp
				temp.Log = nil
				summaries = append(summaries, &temp)
			} else {
				summaries = append(summaries, tp)
			}
		}
		return nil
	})
	return summaries, err
}

// Transport implements API.
func (mc *mockRPCClient) Transport(tid uuid.UUID) (*TransportSummary, error) {
	var summary TransportSummary
	err := mc.do(false, func() error {
		for _, tp := range mc.o.Transports {
			if tp.ID == tid {
				summary = *tp
				return nil
			}
		}
		return fmt.Errorf("transport of id '%s' is not found", tid)
	})
	return &summary, err
}

// AddTransport implements API.
func (mc *mockRPCClient) AddTransport(remote cipher.PubKey, tpType string, _ time.Duration, _ string, _ bool, _ bool) (*TransportSummary, error) {
	summary := &TransportSummary{
		ID:     transport.MakeTransportID(mc.o.PubKey, remote, types.Type(tpType)),
		Local:  mc.o.PubKey,
		Remote: remote,
		Type:   types.Type(tpType),
		Log:    transport.NewLogEntry(),
	}
	return summary, mc.do(true, func() error {
		mc.o.Transports = append(mc.o.Transports, summary)
		return nil
	})
}

// SetSTCPAddr implements API.
func (mc *mockRPCClient) SetSTCPAddr(_ cipher.PubKey, _ string) error {
	return nil
}

// RemoveTransport implements API.
func (mc *mockRPCClient) RemoveTransport(tid uuid.UUID) error {
	return mc.do(true, func() error {
		for i, tp := range mc.o.Transports {
			if tp.ID == tid {
				mc.o.Transports = append(mc.o.Transports[:i], mc.o.Transports[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("transport of id '%s' is not found", tid)
	})
}

// RemoveAllTransports implements API.
func (mc *mockRPCClient) RemoveAllTransports() error {
	return mc.do(true, func() error {
		mc.o.Transports = []*TransportSummary{}
		return nil
	})
}

func (mc *mockRPCClient) DiscoverTransportsByPK(cipher.PubKey) ([]*transport.Entry, error) {
	return nil, ErrNotImplemented
}

func (mc *mockRPCClient) DiscoverTransportByID(uuid.UUID) (*transport.Entry, error) {
	return nil, ErrNotImplemented
}

// SetPublicAutoconnect implements API.
func (mc *mockRPCClient) SetPublicAutoconnect(_ bool) error {
	return nil
}

// StartPublicAutoconnect implements API.
func (mc *mockRPCClient) StartPublicAutoconnect() error {
	return nil
}

// StopPublicAutoconnect implements API.
func (mc *mockRPCClient) StopPublicAutoconnect() error {
	return nil
}

// PublicAutoconnectStatus implements API.
func (mc *mockRPCClient) PublicAutoconnectStatus() (bool, error) {
	return false, nil
}

func (mc *mockRPCClient) SetExistingTPOnly(_ bool) error {
	return nil
}

func (mc *mockRPCClient) SetForceLocalRoutes(_ bool) error {
	return nil
}

func (mc *mockRPCClient) SetMuxRoutes(_ int) error {
	return nil
}

func (mc *mockRPCClient) SetMuxMode(_ string) error {
	return nil
}

func (mc *mockRPCClient) ActiveRoutes() ([]AppRouteStatus, error) {
	return nil, nil
}

func (mc *mockRPCClient) AddMuxRoute(_ string, _ uuid.UUID) error {
	return nil
}

func (mc *mockRPCClient) RemoveMuxRoute(_ string, _ uuid.UUID) error {
	return nil
}

// RoutingRules implements API.
func (mc *mockRPCClient) RoutingRules() ([]routing.Rule, error) {
	return mc.rt.AllRules(), nil
}

// RoutingRule implements API.
func (mc *mockRPCClient) RoutingRule(key routing.RouteID) (routing.Rule, error) {
	return mc.rt.Rule(key)
}

// SaveRoutingRule implements API.
func (mc *mockRPCClient) SaveRoutingRule(rule routing.Rule) error {
	return mc.rt.SaveRule(rule)
}

// RemoveRoutingRule implements API.
func (mc *mockRPCClient) RemoveRoutingRule(key routing.RouteID) error {
	mc.rt.DelRules([]routing.RouteID{key})
	return nil
}

// RouteGroups implements API.
func (mc *mockRPCClient) RouteGroups() ([]RouteGroupInfo, error) {
	var routeGroups []RouteGroupInfo

	rules := mc.rt.AllRules()
	for _, consumeRule := range rules {
		if consumeRule == nil || consumeRule.Type() != routing.RuleReverse {
			continue
		}

		fwdRID := consumeRule.NextRouteID()
		fwdRule, err := mc.rt.Rule(fwdRID)
		if err != nil || fwdRule == nil {
			continue
		}

		desc := consumeRule.RouteDescriptor()
		info := RouteGroupInfo{
			ConsumeRuleID: consumeRule.KeyRouteID(),
			FwdRuleID:     fwdRule.KeyRouteID(),
			Desc: routing.RouteDescriptorFields{
				DstPK:   desc.DstPK(),
				SrcPK:   desc.SrcPK(),
				DstPort: desc.DstPort(),
				SrcPort: desc.SrcPort(),
			},
		}
		if fwdRule.Summary() != nil && fwdRule.Summary().ForwardFields != nil {
			info.FwdNextTpID = fwdRule.Summary().ForwardFields.NextTID.String()
		}

		routeGroups = append(routeGroups, info)
	}

	return routeGroups, nil
}

// FetchServiceData implements API.
func (mc *mockRPCClient) FetchServiceData(_, _ string) ([]byte, error) {
	return nil, nil
}

// ServiceHealth implements API.
func (mc *mockRPCClient) ServiceHealth() ([]ServiceHealthEntry, error) {
	return nil, nil
}

// Reload implements API.
func (mc *mockRPCClient) Reload() error {
	return nil
}

// Shutdown implements API.
func (mc *mockRPCClient) Shutdown() error {
	return nil
}

// Exec implements API.
func (mc *mockRPCClient) Exec(string) ([]byte, error) {
	return []byte("mock"), nil
}

// RuntimeLogs implements API.
func (mc *mockRPCClient) RuntimeLogs() (string, error) {
	return "", nil
}

// RuntimeStats implements API.
func (mc *mockRPCClient) RuntimeStats() (*RuntimeStatsInfo, error) {
	return &RuntimeStatsInfo{
		NumGoroutine: 10,
		NumCPU:       4,
		GOMAXPROCS:   4,
		GoVersion:    "go1.21.0",
	}, nil
}

// SetMinHops implements API
func (mc *mockRPCClient) SetMinHops(_ uint16) error {
	return nil
}

// SetCalculateRoutes implements API
func (mc *mockRPCClient) SetCalculateRoutes(_ bool) error {
	return nil
}

// GetCalculateRoutes implements API
func (mc *mockRPCClient) GetCalculateRoutes() (bool, error) {
	return false, nil
}

// SetSyncTPDData implements API
func (mc *mockRPCClient) SetSyncTPDData(_ bool) error {
	return nil
}

// GetSyncTPDData implements API
func (mc *mockRPCClient) GetSyncTPDData() (bool, error) {
	return false, nil
}

// SetPersistentTransports implements API
func (mc *mockRPCClient) SetPersistentTransports(_ []transport.PersistentTransports) error {
	return nil
}

// GetPersistentTransports implements API
func (mc *mockRPCClient) GetPersistentTransports() ([]transport.PersistentTransports, error) {
	return []transport.PersistentTransports{}, nil
}

// GetTransportLogs implements API
func (mc *mockRPCClient) GetTransportLogs(_ int) ([]TransportLogEntry, error) {
	return []TransportLogEntry{}, nil
}

// SetLogRotationInterval implements API
func (mc *mockRPCClient) SetLogRotationInterval(_ visorconfig.Duration) error {
	return nil
}

// GetLogRotationInterval implements API
func (mc *mockRPCClient) GetLogRotationInterval() (visorconfig.Duration, error) {
	var d visorconfig.Duration
	return d, nil
}

// VPNServers implements API
func (mc *mockRPCClient) VPNServers(_, _ string) ([]servicedisc.Service, error) {
	return []servicedisc.Service{}, nil
}

// ProxyServers implements API
func (mc *mockRPCClient) ProxyServers(_, _ string) ([]servicedisc.Service, error) {
	return []servicedisc.Service{}, nil
}

// DeregisterService implements API
func (mc *mockRPCClient) DeregisterService(_ []cipher.PubKey, _ string) error {
	return nil
}

// RemoteVisors implements API
func (mc *mockRPCClient) RemoteVisors() ([]string, error) {
	return []string{}, nil
}

// Ports implements API
func (mc *mockRPCClient) Ports() (map[string]PortDetail, error) {
	return map[string]PortDetail{}, nil
}

// IsDMSGClientReady implements API.
func (mc *mockRPCClient) IsDMSGClientReady() (bool, error) {
	return false, nil
}

// DMSGServers implements API.
func (mc *mockRPCClient) DMSGServers() ([]DMSGServerInfo, error) {
	return []DMSGServerInfo{}, nil
}

// Connect implements API.
func (mc *mockRPCClient) Connect(_ cipher.PubKey, _, _ int) (uuid.UUID, error) {
	return uuid.UUID{}, nil
}

// Disconnect implements API.
func (mc *mockRPCClient) Disconnect(_ uuid.UUID) error {
	return nil
}

// List implements API.
func (mc *mockRPCClient) List() (map[uuid.UUID]*appnet.ForwardConn, error) {
	return nil, nil
}

// ConnectRawTCP implements API.
func (mc *mockRPCClient) ConnectRawTCP(_ cipher.PubKey, _, _ int) (uuid.UUID, error) {
	return uuid.UUID{}, nil
}

// DisconnectRawTCP implements API.
func (mc *mockRPCClient) DisconnectRawTCP(_ uuid.UUID) error {
	return nil
}

// ListRawTCP implements API.
func (mc *mockRPCClient) ListRawTCP() (map[uuid.UUID]*appnet.RawTCPForwardConn, error) {
	return nil, nil
}

// RegisterHTTPPort implements API.
func (mc *mockRPCClient) RegisterHTTPPort(_ int) error {
	return nil
}

// DeregisterHTTPPort implements API.
func (mc *mockRPCClient) DeregisterHTTPPort(_ int) error {
	return nil
}

// ListHTTPPorts implements API.
func (mc *mockRPCClient) ListHTTPPorts() ([]int, error) {
	return nil, nil
}

// DialPing implements API.
func (mc *mockRPCClient) DialPing(_ PingConfig) error {
	return nil
}

// Ping implements API.
func (mc *mockRPCClient) Ping(_ PingConfig) ([]time.Duration, error) {
	return []time.Duration{}, nil
}

// PingOnce implements API.
func (mc *mockRPCClient) PingOnce(_ PingConfig) (time.Duration, error) {
	return 0, nil
}

// StopPing implements API.
func (mc *mockRPCClient) StopPing(_ cipher.PubKey) error {
	return nil
}

// StopAllPings implements API.
func (mc *mockRPCClient) StopAllPings() (int, []string, error) {
	return 0, nil, nil
}

// DialDmsgPing implements API.
func (mc *mockRPCClient) DialDmsgPing(_ cipher.PubKey) error {
	return nil
}

// DmsgPing implements API.
func (mc *mockRPCClient) DmsgPing(_ PingConfig) ([]time.Duration, error) {
	return []time.Duration{}, nil
}

// DmsgPingOnce implements API.
func (mc *mockRPCClient) DmsgPingOnce(_ PingConfig) (time.Duration, error) {
	return 0, nil
}

// StopDmsgPing implements API.
func (mc *mockRPCClient) StopDmsgPing(_ cipher.PubKey) error {
	return nil
}

// DialDmsgPingViaServer implements API.
func (mc *mockRPCClient) DialDmsgPingViaServer(_ cipher.PubKey, _ cipher.PubKey) error {
	return nil
}

// GetDmsgPingServerPK implements API.
func (mc *mockRPCClient) GetDmsgPingServerPK(_ cipher.PubKey) (cipher.PubKey, error) {
	return cipher.PubKey{}, nil
}

// GetRemoteDmsgServers implements API.
func (mc *mockRPCClient) GetRemoteDmsgServers(_ cipher.PubKey) ([]cipher.PubKey, error) {
	return []cipher.PubKey{}, nil
}

// DialDmsgRPC implements API.
func (mc *mockRPCClient) DialDmsgRPC(_ cipher.PubKey) (net.Conn, error) {
	return nil, fmt.Errorf("mock: DialDmsgRPC not implemented")
}

// GetPreferredDmsgServer implements API.
func (mc *mockRPCClient) GetPreferredDmsgServer(_ cipher.PubKey) (cipher.PubKey, error) {
	return cipher.PubKey{}, nil
}

// BandwidthTest implements API.
func (mc *mockRPCClient) BandwidthTest(_ BandwidthTestConfig) (BandwidthResult, error) {
	return BandwidthResult{}, nil
}

// DmsgBandwidthTest implements API.
func (mc *mockRPCClient) DmsgBandwidthTest(_ BandwidthTestConfig) (BandwidthResult, error) {
	return BandwidthResult{}, nil
}

// TestVisor implements API.
func (mc *mockRPCClient) TestVisor(_ PingConfig) ([]TestResult, error) {
	return []TestResult{}, nil
}

// TestProxy implements API.
func (mc *mockRPCClient) TestProxy(_ ProxyTestConfig) ([]ProxyTestResult, error) {
	return []ProxyTestResult{}, nil
}

// ReinitiateModule implements API.
func (mc *mockRPCClient) ReinitiateModule(_ string) error {
	return nil
}

// StartUIServer implements API.
func (mc *mockRPCClient) StartUIServer(_ string) error {
	return nil
}

// StopUIServer implements API.
func (mc *mockRPCClient) StopUIServer() error {
	return nil
}

// UIServerStatus implements API.
func (mc *mockRPCClient) UIServerStatus() (*UIServerStatus, error) {
	return &UIServerStatus{}, nil
}

// DmsgHTTP implements API.
func (mc *mockRPCClient) DmsgHTTP(_ DmsgHTTPRequest) (*DmsgHTTPResponse, error) {
	return &DmsgHTTPResponse{StatusCode: 200, Status: "OK"}, nil
}

// TPSStatus implements API.
func (mc *mockRPCClient) TPSStatus() (*TPSStatus, error) {
	return &TPSStatus{Enabled: false}, nil
}

// TPSAddTransport implements API.
func (mc *mockRPCClient) TPSAddTransport(_, _ cipher.PubKey, _ string) (*TPSTransportResponse, error) {
	return nil, fmt.Errorf("TPS not available in mock")
}

// TPSRemoveTransport implements API.
func (mc *mockRPCClient) TPSRemoveTransport(_ cipher.PubKey, _ uuid.UUID) error {
	return fmt.Errorf("TPS not available in mock")
}

// TPSGetTransports implements API.
func (mc *mockRPCClient) TPSGetTransports(_ cipher.PubKey) ([]TPSTransportResponse, error) {
	return nil, fmt.Errorf("TPS not available in mock")
}

// GetTransportSetupNodes implements API.
func (mc *mockRPCClient) GetTransportSetupNodes() ([]cipher.PubKey, error) {
	return nil, nil
}

// GetTransportSetupNodesSorted implements API.
func (mc *mockRPCClient) GetTransportSetupNodesSorted() ([]cipher.PubKey, error) {
	return nil, nil
}

// GetRouteSetupNodesSorted implements API.
func (mc *mockRPCClient) GetRouteSetupNodesSorted() ([]cipher.PubKey, error) {
	return nil, nil
}

// GetTPSHealth implements API.
func (mc *mockRPCClient) GetTPSHealth() ([]NodeHealth, error) {
	return nil, nil
}

// GetRSNHealth implements API.
func (mc *mockRPCClient) GetRSNHealth() ([]NodeHealth, error) {
	return nil, nil
}

// TPSExternalHealthCheck implements API.
func (mc *mockRPCClient) TPSExternalHealthCheck(_ cipher.PubKey) error {
	return fmt.Errorf("external TPS not available in mock")
}

// TPSExternalAddTransport implements API.
func (mc *mockRPCClient) TPSExternalAddTransport(_, _, _ cipher.PubKey, _ string) (*TPSTransportResponse, error) {
	return nil, fmt.Errorf("external TPS not available in mock")
}

// TPSExternalGetTransports implements API.
func (mc *mockRPCClient) TPSExternalGetTransports(_, _ cipher.PubKey) ([]TPSTransportResponse, error) {
	return nil, fmt.Errorf("external TPS not available in mock")
}

// Close implements API.
func (mc *mockRPCClient) Close() error {
	return nil
}
