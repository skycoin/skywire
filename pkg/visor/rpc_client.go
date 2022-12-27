// Package visor pkg/visor/rpc_client.go
package visor

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/rpc"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/util/cipherutil"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	// ErrAlreadyServing is returned when an operation fails due to an operation
	// that is currently running.
	ErrAlreadyServing = errors.New("already serving")

	// ErrTimeout represents a timed-out call.
	ErrTimeout = errors.New("rpc client timeout")
)

// API provides methods to call an RPC Server.
// It implements API
type rpcClient struct {
	log     logrus.FieldLogger
	timeout time.Duration
	conn    io.ReadWriteCloser
	client  *rpc.Client
	prefix  string
	FixGob  bool
}

// NewRPCClient creates a new API.
func NewRPCClient(log logrus.FieldLogger, conn io.ReadWriteCloser, prefix string, timeout time.Duration) API {
	if log == nil {
		log = logging.MustGetLogger("visor_rpc_client")
	}
	return &rpcClient{
		log:     log,
		timeout: timeout,
		conn:    conn,
		client:  rpc.NewClient(conn),
		prefix:  prefix,
	}
}

// Call calls the internal rpc.Client with the serviceMethod arg prefixed.
func (rc *rpcClient) Call(method string, args, reply interface{}) error {
	ctx := context.Background()
	timeout := rc.timeout

	switch method {
	case "AddTransport":
		timeout = skyenv.TransportRPCTimeout
	case "Update":
		timeout = skyenv.UpdateRPCTimeout
	}

	if timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, time.Now().Add(timeout))
		defer cancel()
	}

	select {
	case call := <-rc.client.Go(rc.prefix+"."+method, args, reply, nil).Done:
		return call.Error
	case <-ctx.Done():
		if err := rc.conn.Close(); err != nil {
			rc.log.WithError(err).Warn("Failed to close rpc client after timeout error.")
		}
		return ctx.Err()
	}
}

// Summary calls Summary.
func (rc *rpcClient) Summary() (*Summary, error) {
	out := new(Summary)
	err := rc.Call("Summary", &struct{}{}, out)
	return out, err
}

// Overview calls Overview.
func (rc *rpcClient) Overview() (*Overview, error) {
	out := new(Overview)
	err := rc.Call("Overview", &struct{}{}, out)
	return out, err
}

// Health calls Health
func (rc *rpcClient) Health() (*HealthInfo, error) {
	hi := &HealthInfo{}
	err := rc.Call("Health", &struct{}{}, hi)
	return hi, err
}

// Uptime calls Uptime
func (rc *rpcClient) Uptime() (float64, error) {
	var out float64
	err := rc.Call("Uptime", &struct{}{}, &out)
	return out, err
}

// SetRewardAddress implements API.
func (rc *rpcClient) SetRewardAddress(r string) (rConfig string, err error) {
	err = rc.Call("SetRewardAddress", &r, &rConfig)
	if err != nil {
		return "", err
	}
	return rConfig, err
}

// GetRewardAddress implements API.
func (rc *rpcClient) GetRewardAddress() (rConfig string, err error) {
	err = rc.Call("GetRewardAddress", &struct{}{}, &rConfig)
	return rConfig, err
}

// Apps calls Apps.
func (rc *rpcClient) Apps() ([]*appserver.AppState, error) {
	states := make([]*appserver.AppState, 0)
	err := rc.Call("Apps", &struct{}{}, &states)
	return states, err
}

// App calls App.
func (rc *rpcClient) App(appName string) (*appserver.AppState, error) {
	var state *appserver.AppState
	err := rc.Call("App", appName, &state)
	return state, err
}

// StartApp calls StartApp.
func (rc *rpcClient) StartApp(appName string) error {
	return rc.Call("StartApp", &appName, &struct{}{})
}

// StopApp calls StopApp.
func (rc *rpcClient) StopApp(appName string) error {
	return rc.Call("StopApp", &appName, &struct{}{})
}

// StartVPNClient calls StartVPNClient.
func (rc *rpcClient) StartVPNClient(pk cipher.PubKey) error {
	return rc.Call("StartVPNClient", &pk, &struct{}{})
}

// StopVPNClient calls StopVPNClient.
func (rc *rpcClient) StopVPNClient(appName string) error {
	return rc.Call("StopVPNClient", &appName, &struct{}{})
}

// SetAppDetailedStatus sets app's detailed state.
func (rc *rpcClient) SetAppDetailedStatus(appName, status string) error {
	return rc.Call("SetAppDetailedStatus", &SetAppStatusIn{
		AppName: appName,
		Status:  status,
	}, &struct{}{})
}

// SetAppError sets app's error.
func (rc *rpcClient) SetAppError(appName, appErr string) error {
	return rc.Call("SetAppError", &SetAppErrorIn{
		AppName: appName,
		Err:     appErr,
	}, &struct{}{})
}

// RestartApp calls `RestartApp`.
func (rc *rpcClient) RestartApp(appName string) error {
	return rc.Call("RestartApp", &appName, &struct{}{})
}

// SetAutoStart calls SetAutoStart.
func (rc *rpcClient) SetAutoStart(appName string, autostart bool) error {
	return rc.Call("SetAutoStart", &SetAutoStartIn{
		AppName:   appName,
		AutoStart: autostart,
	}, &struct{}{})
}

// SetAppPassword calls SetAppPassword.
func (rc *rpcClient) SetAppPassword(appName, password string) error {
	return rc.Call("SetAppPassword", &SetAppPasswordIn{
		AppName:  appName,
		Password: password,
	}, &struct{}{})
}

// SetAppPK calls SetAppPK.
func (rc *rpcClient) SetAppPK(appName string, pk cipher.PubKey) error {
	return rc.Call("SetAppPK", &SetAppPKIn{
		AppName: appName,
		PK:      pk,
	}, &struct{}{})
}

// SetAppKillswitch implements API.
func (rc *rpcClient) SetAppKillswitch(appName string, killswitch bool) error {
	return rc.Call("SetAppKillswitch", &SetAppBoolIn{
		AppName: appName,
		Val:     killswitch,
	}, &struct{}{})
}

// SetAppKillswitch implements API.
func (rc *rpcClient) SetAppNetworkInterface(appName, netifc string) error {
	return rc.Call("SetAppNetworkInterface", &SetAppNetworkInterfaceIn{
		AppName: appName,
		NetIfc:  netifc,
	}, &struct{}{})
}

// SetAppSecure implements API.
func (rc *rpcClient) SetAppSecure(appName string, isSecure bool) error {
	return rc.Call("SetAppSecure", &SetAppBoolIn{
		AppName: appName,
		Val:     isSecure,
	}, &struct{}{})
}

// SetAppDNS implements API.
func (rc *rpcClient) SetAppDNS(appName string, dnsAddr string) error {
	return rc.Call("SetAppDNS", &SetAppStringIn{
		AppName: appName,
		Val:     dnsAddr,
	}, &struct{}{})
}

// LogsSince calls LogsSince
func (rc *rpcClient) LogsSince(timestamp time.Time, appName string) ([]string, error) {
	res := make([]string, 0)

	err := rc.Call("LogsSince", &AppLogsRequest{
		TimeStamp: timestamp,
		AppName:   appName,
	}, &res)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (rc *rpcClient) GetAppStats(appName string) (appserver.AppStats, error) {
	var stats appserver.AppStats

	if err := rc.Call("GetAppStats", &appName, &stats); err != nil {
		return appserver.AppStats{}, err
	}

	return stats, nil
}

func (rc *rpcClient) GetAppError(appName string) (string, error) {
	var appErr string

	if err := rc.Call("GetAppError", &appName, &appErr); err != nil {
		return appErr, err
	}

	return appErr, nil
}

// GetAppConnectionsSummary get connections stats for the app.
func (rc *rpcClient) GetAppConnectionsSummary(appName string) ([]appserver.ConnectionSummary, error) {
	var summary []appserver.ConnectionSummary

	if err := rc.Call("GetAppConnectionsSummary", &appName, &summary); err != nil {
		return nil, err
	}

	return summary, nil
}

// TransportTypes calls TransportTypes.
func (rc *rpcClient) TransportTypes() ([]string, error) {
	var types []string
	err := rc.Call("TransportTypes", &struct{}{}, &types)
	return types, err
}

// Transports calls Transports.
func (rc *rpcClient) Transports(types []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error) {
	transports := make([]*TransportSummary, 0)
	err := rc.Call("Transports", &TransportsIn{
		FilterTypes:   types,
		FilterPubKeys: pks,
		ShowLogs:      logs,
	}, &transports)
	return transports, err
}

// Transport calls Transport.
func (rc *rpcClient) Transport(tid uuid.UUID) (*TransportSummary, error) {
	var summary TransportSummary
	err := rc.Call("Transport", &tid, &summary)
	return &summary, err
}

// AddTransport calls AddTransport.
func (rc *rpcClient) AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration) (*TransportSummary, error) {
	var summary TransportSummary
	err := rc.Call("AddTransport", &AddTransportIn{
		RemotePK: remote,
		TpType:   tpType,
		Timeout:  timeout,
	}, &summary)

	return &summary, err
}

// RemoveTransport calls RemoveTransport.
func (rc *rpcClient) RemoveTransport(tid uuid.UUID) error {
	return rc.Call("RemoveTransport", &tid, &struct{}{})
}

func (rc *rpcClient) DiscoverTransportsByPK(pk cipher.PubKey) ([]*transport.Entry, error) {
	entries := make([]*transport.Entry, 0)
	err := rc.Call("DiscoverTransportsByPK", &pk, &entries)
	return entries, err
}

func (rc *rpcClient) DiscoverTransportByID(id uuid.UUID) (*transport.Entry, error) {
	var entry transport.Entry
	err := rc.Call("DiscoverTransportByID", &id, &entry)
	return &entry, err
}

// SetPublicAutoconnect implements API.
func (rc *rpcClient) SetPublicAutoconnect(pAc bool) error {
	return rc.Call("SetPublicAutoconnect", &pAc, &struct{}{})
}

// RoutingRules calls RoutingRules.
func (rc *rpcClient) RoutingRules() ([]routing.Rule, error) {
	entries := make([]routing.Rule, 0)
	err := rc.Call("RoutingRules", &struct{}{}, &entries)
	return entries, err
}

// RoutingRule calls RoutingRule.
func (rc *rpcClient) RoutingRule(key routing.RouteID) (routing.Rule, error) {
	var rule routing.Rule
	err := rc.Call("RoutingRule", &key, &rule)
	return rule, err
}

// SaveRoutingRule calls SaveRoutingRule.
func (rc *rpcClient) SaveRoutingRule(rule routing.Rule) error {
	return rc.Call("SaveRoutingRule", &rule, &struct{}{})
}

// RemoveRoutingRule calls RemoveRoutingRule.
func (rc *rpcClient) RemoveRoutingRule(key routing.RouteID) error {
	return rc.Call("RemoveRoutingRule", &key, &struct{}{})
}

// RouteGroups calls RouteGroups.
func (rc *rpcClient) RouteGroups() ([]RouteGroupInfo, error) {
	var routegroups []RouteGroupInfo
	err := rc.Call("RouteGroups", &struct{}{}, &routegroups)
	return routegroups, err
}

// Restart calls Restart.
func (rc *rpcClient) Restart() error {
	return rc.Call("Restart", &struct{}{}, &struct{}{})
}

// Shutdown calls Shutdown.
func (rc *rpcClient) Shutdown() error {
	return rc.Call("Shutdown", &struct{}{}, &struct{}{})
}

// Exec calls Exec.
func (rc *rpcClient) Exec(command string) ([]byte, error) {
	output := make([]byte, 0)
	err := rc.Call("Exec", &command, &output)
	return output, err
}

// RuntimeLogs calls RuntimeLogs.
func (rc *rpcClient) RuntimeLogs() (string, error) {
	var logs string
	err := rc.Call("RuntimeLogs", &struct{}{}, &logs)
	return logs, err
}

// SetMinHops sets the min_hops from visor routing config
func (rc *rpcClient) SetMinHops(hops uint16) error {
	err := rc.Call("SetMinHops", &hops, &struct{}{})
	return err
}

// SetPersistentTransports sets the persistent_transports from visor routing config
func (rc *rpcClient) SetPersistentTransports(pts []transport.PersistentTransports) error {
	err := rc.Call("SetPersistentTransports", &pts, &struct{}{})
	return err
}

// GetPersistentTransports gets the persistent_transports from visor routing config
func (rc *rpcClient) GetPersistentTransports() ([]transport.PersistentTransports, error) {
	var tps []transport.PersistentTransports
	err := rc.Call("GetPersistentTransports", &struct{}{}, &tps)
	return tps, err
}

// SetLogRotationInterval sets the log_rotation_interval from visor config
func (rc *rpcClient) SetLogRotationInterval(d visorconfig.Duration) error {
	err := rc.Call("SetLogRotationInterval", &d, &struct{}{})
	return err
}

// GetLogRotationInterval gets the log_rotation_interval from visor config
func (rc *rpcClient) GetLogRotationInterval() (visorconfig.Duration, error) {
	var d visorconfig.Duration
	err := rc.Call("GetLogRotationInterval", &struct{}{}, &d)
	return d, err
}

// StatusMessage defines a status of visor update.
type StatusMessage struct {
	Text    string
	IsError bool
}

// VPNServers calls VPNServers.
func (rc *rpcClient) VPNServers(version, country string) ([]servicedisc.Service, error) {
	output := []servicedisc.Service{}
	err := rc.Call("VPNServers", &FilterVPNServersIn{ // nolint
		Version: version,
		Country: country,
	}, &output)
	return output, err
}

// RemoteVisors calls RemoteVisors.
func (rc *rpcClient) RemoteVisors() ([]string, error) {
	output := []string{}
	rc.Call("RemoteVisors", &struct{}{}, &output) // nolint
	return output, nil
}

// Ports calls Ports.
func (rc *rpcClient) Ports() (map[string]PortDetail, error) {
	output := map[string]PortDetail{}
	rc.Call("Ports", &struct{}{}, &output) // nolint
	return output, nil
}

// IsDMSGClientReady return availability of dsmg client
func (rc *rpcClient) IsDMSGClientReady() (bool, error) {
	var out bool
	err := rc.Call("IsDMSGClientReady", &struct{}{}, &out)
	return out, err
}

// DialPing calls DialPing.
func (rc *rpcClient) DialPing(conf PingConfig) error {
	return rc.Call("DialPing", &conf, &struct{}{})
}

// Ping calls Ping.
func (rc *rpcClient) Ping(conf PingConfig) ([]time.Duration, error) {
	var latencies []time.Duration
	err := rc.Call("Ping", &conf, &latencies)
	return latencies, err
}

// StopPing calls StopPing.
func (rc *rpcClient) StopPing(pk cipher.PubKey) error {
	return rc.Call("StopPing", &pk, &struct{}{})
}

// TestVisor calls TestVisor.
func (rc *rpcClient) TestVisor(conf PingConfig) ([]TestResult, error) {
	var results []TestResult
	err := rc.Call("TestVisor", &conf, &results)
	return results, err
}

// MockRPCClient mocks API.
type mockRPCClient struct {
	startedAt time.Time
	o         *Overview
	tpTypes   []network.Type
	rt        routing.Table
	logS      appcommon.LogStore
	sync.RWMutex
}

// NewMockRPCClient creates a new mock API.
func NewMockRPCClient(r *rand.Rand, maxTps int, maxRules int) (cipher.PubKey, API, error) {
	log := logging.MustGetLogger("mock-rpc-client")

	types := []network.Type{"messaging", "native"}
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
				{AppConfig: appserver.AppConfig{Name: "foo.v1.0", AutoStart: false, Port: 10}},
				{AppConfig: appserver.AppConfig{Name: "bar.v2.0", AutoStart: false, Port: 20}},
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
func (mc *mockRPCClient) SetRewardAddress(p string) (string, error) {
	return "", nil
}

// GetRewardAddress implements API.
func (mc *mockRPCClient) GetRewardAddress() (string, error) {
	return "", nil
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

// StopApp implements API.
func (*mockRPCClient) StopApp(string) error {
	return nil
}

// StartVPNClient implements API.
func (*mockRPCClient) StartVPNClient(cipher.PubKey) error {
	return nil
}

// StopVPNClient implements API.
func (*mockRPCClient) StopVPNClient(string) error {
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
func (mc *mockRPCClient) SetAppKillswitch(appName string, killswitch bool) error {
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
func (mc *mockRPCClient) SetAppSecure(appName string, isSecure bool) error {
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
func (mc *mockRPCClient) AddTransport(remote cipher.PubKey, tpType string, _ time.Duration) (*TransportSummary, error) {
	summary := &TransportSummary{
		ID:     transport.MakeTransportID(mc.o.PubKey, remote, network.Type(tpType)),
		Local:  mc.o.PubKey,
		Remote: remote,
		Type:   network.Type(tpType),
		Log:    transport.NewLogEntry(),
	}
	return summary, mc.do(true, func() error {
		mc.o.Transports = append(mc.o.Transports, summary)
		return nil
	})
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
	for _, rule := range rules {
		if rule.Type() != routing.RuleReverse {
			continue
		}

		fwdRID := rule.NextRouteID()
		fwdRule, err := mc.rt.Rule(fwdRID)
		if err != nil {
			return nil, err
		}
		routeGroups = append(routeGroups, RouteGroupInfo{
			ConsumeRule: rule,
			FwdRule:     fwdRule,
		})
	}

	return routeGroups, nil
}

// Restart implements API.
func (mc *mockRPCClient) Restart() error {
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

// SetMinHops implements API
func (mc *mockRPCClient) SetMinHops(_ uint16) error {
	return nil
}

// SetPersistentTransports implements API
func (mc *mockRPCClient) SetPersistentTransports(_ []transport.PersistentTransports) error {
	return nil
}

// GetPersistentTransports implements API
func (mc *mockRPCClient) GetPersistentTransports() ([]transport.PersistentTransports, error) {
	return []transport.PersistentTransports{}, nil
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

// DialPing implements API.
func (mc *mockRPCClient) DialPing(_ PingConfig) error {
	return nil
}

// Ping implements API.
func (mc *mockRPCClient) Ping(_ PingConfig) ([]time.Duration, error) {
	return []time.Duration{}, nil
}

// StopPing implements API.
func (mc *mockRPCClient) StopPing(_ cipher.PubKey) error {
	return nil
}

// TestVisor implements API.
func (mc *mockRPCClient) TestVisor(_ PingConfig) ([]TestResult, error) {
	return []TestResult{}, nil
}
