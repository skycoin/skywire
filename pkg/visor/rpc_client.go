package visor

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/rpc"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/buildinfo"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/snet/snettest"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/util/updater"
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

// ExtraSummary calls ExtraSummary.
func (rc *rpcClient) ExtraSummary() (*ExtraSummary, error) {
	out := new(ExtraSummary)
	err := rc.Call("ExtraSummary", &struct{}{}, out)
	return out, err
}

// Summary calls Summary.
func (rc *rpcClient) Summary() (*Summary, error) {
	out := new(Summary)
	err := rc.Call("Summary", &struct{}{}, out)
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

// Apps calls Apps.
func (rc *rpcClient) Apps() ([]*launcher.AppState, error) {
	states := make([]*launcher.AppState, 0)
	err := rc.Call("Apps", &struct{}{}, &states)
	return states, err
}

// StartApp calls StartApp.
func (rc *rpcClient) StartApp(appName string) error {
	return rc.Call("StartApp", &appName, &struct{}{})
}

// StopApp calls StopApp.
func (rc *rpcClient) StopApp(appName string) error {
	return rc.Call("StopApp", &appName, &struct{}{})
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
func (rc *rpcClient) AddTransport(remote cipher.PubKey, tpType string, public bool, timeout time.Duration) (*TransportSummary, error) {
	var summary TransportSummary
	err := rc.Call("AddTransport", &AddTransportIn{
		RemotePK: remote,
		TpType:   tpType,
		Public:   public,
		Timeout:  timeout,
	}, &summary)

	return &summary, err
}

// RemoveTransport calls RemoveTransport.
func (rc *rpcClient) RemoveTransport(tid uuid.UUID) error {
	return rc.Call("RemoveTransport", &tid, &struct{}{})
}

func (rc *rpcClient) DiscoverTransportsByPK(pk cipher.PubKey) ([]*transport.EntryWithStatus, error) {
	entries := make([]*transport.EntryWithStatus, 0)
	err := rc.Call("DiscoverTransportsByPK", &pk, &entries)
	return entries, err
}

func (rc *rpcClient) DiscoverTransportByID(id uuid.UUID) (*transport.EntryWithStatus, error) {
	var entry transport.EntryWithStatus
	err := rc.Call("DiscoverTransportByID", &id, &entry)
	return &entry, err
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

// Exec calls Exec.
func (rc *rpcClient) Exec(command string) ([]byte, error) {
	output := make([]byte, 0)
	err := rc.Call("Exec", &command, &output)
	return output, err
}

// Update calls Update.
func (rc *rpcClient) Update(config updater.UpdateConfig) (bool, error) {
	var updated bool
	err := rc.Call("Update", &config, &updated)
	return updated, err
}

// StatusMessage defines a status of visor update.
type StatusMessage struct {
	Text    string
	IsError bool
}

// UpdateWithStatus combines results of Update and UpdateStatus.
func (rc *rpcClient) UpdateWithStatus(config updater.UpdateConfig) <-chan StatusMessage {
	ch := make(chan StatusMessage, 512)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				var status string

				err := rc.Call("UpdateStatus", &struct{}{}, &status)
				if err != nil {
					rc.log.WithError(err).Errorf("Failed to check update status")
					status = ""
				}

				select {
				case <-ctx.Done():
					return
				default:
					switch status {
					case "", io.EOF.Error():

					default:
						ch <- StatusMessage{
							Text: status,
						}
					}
					time.Sleep(100 * time.Millisecond)
				}
			}
		}
	}()

	go func() {
		defer func() {
			cancel()
			close(ch)
		}()

		var updated bool

		if err := rc.Call("Update", &config, &updated); err != nil {
			ch <- StatusMessage{
				Text:    err.Error(),
				IsError: true,
			}
		} else if updated {
			ch <- StatusMessage{
				Text: "Finished",
			}
		} else {
			ch <- StatusMessage{
				Text: "No update found",
			}
		}
	}()

	return ch
}

// UpdateAvailable calls UpdateAvailable.
func (rc *rpcClient) UpdateAvailable(channel updater.Channel) (*updater.Version, error) {
	var version, empty updater.Version
	err := rc.Call("UpdateAvailable", &channel, &version)
	if err != nil {
		return nil, err
	}

	if version == empty {
		return nil, nil
	}

	return &version, err
}

// UpdateAvailable calls UpdateAvailable.
func (rc *rpcClient) UpdateStatus() (string, error) {
	var result string
	err := rc.Call("UpdateStatus", &struct{}{}, &result)
	if err != nil {
		return "", err
	}

	return result, err
}

// MockRPCClient mocks API.
type mockRPCClient struct {
	startedAt time.Time
	s         *Summary
	tpTypes   []string
	rt        routing.Table
	logS      appcommon.LogStore
	sync.RWMutex
}

// NewMockRPCClient creates a new mock API.
func NewMockRPCClient(r *rand.Rand, maxTps int, maxRules int) (cipher.PubKey, API, error) {
	log := logging.MustGetLogger("mock-rpc-client")

	types := []string{"messaging", "native"}
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
			Log:    new(transport.LogEntry),
		}
		log.Infof("tp[%2d]: %v", i, tps[i])
	}

	rt := routing.NewTable()
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

		keys := snettest.GenKeyPairs(2)

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
		s: &Summary{
			PubKey:          localPK,
			BuildInfo:       buildinfo.Get(),
			AppProtoVersion: supportedProtocolVersion,
			Apps: []*launcher.AppState{
				{AppConfig: launcher.AppConfig{Name: "foo.v1.0", AutoStart: false, Port: 10}},
				{AppConfig: launcher.AppConfig{Name: "bar.v2.0", AutoStart: false, Port: 20}},
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

// Summary implements API.
func (mc *mockRPCClient) Summary() (*Summary, error) {
	var out Summary
	err := mc.do(false, func() error {
		out = *mc.s
		for _, a := range mc.s.Apps {
			a := a
			out.Apps = append(out.Apps, a)
		}
		for _, tp := range mc.s.Transports {
			tp := tp
			out.Transports = append(out.Transports, tp)
		}
		out.RoutesCount = mc.s.RoutesCount
		return nil
	})
	return &out, err
}

// ExtraSummary implements API.
func (mc *mockRPCClient) ExtraSummary() (*ExtraSummary, error) {
	summary, err := mc.Summary()
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

	extraSummary := &ExtraSummary{
		Summary: summary,
		Health:  health,
		Uptime:  uptime,
		Routes:  extraRoutes,
	}

	return extraSummary, nil
}

// Health implements API
func (mc *mockRPCClient) Health() (*HealthInfo, error) {
	hi := &HealthInfo{
		TransportDiscovery: http.StatusOK,
		RouteFinder:        http.StatusOK,
		SetupNode:          http.StatusOK,
		UptimeTracker:      http.StatusOK,
		AddressResolver:    http.StatusOK,
	}

	return hi, nil
}

// Uptime implements API
func (mc *mockRPCClient) Uptime() (float64, error) {
	return time.Since(mc.startedAt).Seconds(), nil
}

// Apps implements API.
func (mc *mockRPCClient) Apps() ([]*launcher.AppState, error) {
	var apps []*launcher.AppState
	err := mc.do(false, func() error {
		for _, a := range mc.s.Apps {
			a := a
			apps = append(apps, a)
		}
		return nil
	})
	return apps, err
}

// StartApp implements API.
func (*mockRPCClient) StartApp(string) error {
	return nil
}

// StopApp implements API.
func (*mockRPCClient) StopApp(string) error {
	return nil
}

// SetAutoStart implements API.
func (mc *mockRPCClient) SetAutoStart(appName string, autostart bool) error {
	return mc.do(true, func() error {
		for _, a := range mc.s.Apps {
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

		for i := range mc.s.Apps {
			if mc.s.Apps[i].Name == socksName {
				return nil
			}
		}

		return fmt.Errorf("app of name '%s' does not exist", socksName)
	})
}

// SetAppPK implements API.
func (mc *mockRPCClient) SetAppPK(string, cipher.PubKey) error {
	return mc.do(true, func() error {
		const socksName = "skysocks-client"

		for i := range mc.s.Apps {
			if mc.s.Apps[i].Name == socksName {
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

// TransportTypes implements API.
func (mc *mockRPCClient) TransportTypes() ([]string, error) {
	return mc.tpTypes, nil
}

// Transports implements API.
func (mc *mockRPCClient) Transports(types []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error) {
	var summaries []*TransportSummary
	err := mc.do(false, func() error {
		for _, tp := range mc.s.Transports {
			tp := tp
			if types != nil {
				for _, reqT := range types {
					if tp.Type == reqT {
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
		for _, tp := range mc.s.Transports {
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
func (mc *mockRPCClient) AddTransport(remote cipher.PubKey, tpType string, _ bool, _ time.Duration) (*TransportSummary, error) {
	summary := &TransportSummary{
		ID:     transport.MakeTransportID(mc.s.PubKey, remote, tpType),
		Local:  mc.s.PubKey,
		Remote: remote,
		Type:   tpType,
		Log:    new(transport.LogEntry),
	}
	return summary, mc.do(true, func() error {
		mc.s.Transports = append(mc.s.Transports, summary)
		return nil
	})
}

// RemoveTransport implements API.
func (mc *mockRPCClient) RemoveTransport(tid uuid.UUID) error {
	return mc.do(true, func() error {
		for i, tp := range mc.s.Transports {
			if tp.ID == tid {
				mc.s.Transports = append(mc.s.Transports[:i], mc.s.Transports[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("transport of id '%s' is not found", tid)
	})
}

func (mc *mockRPCClient) DiscoverTransportsByPK(cipher.PubKey) ([]*transport.EntryWithStatus, error) {
	return nil, ErrNotImplemented
}

func (mc *mockRPCClient) DiscoverTransportByID(uuid.UUID) (*transport.EntryWithStatus, error) {
	return nil, ErrNotImplemented
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

// Exec implements API.
func (mc *mockRPCClient) Exec(string) ([]byte, error) {
	return []byte("mock"), nil
}

// Update implements API.
func (mc *mockRPCClient) Update(_ updater.UpdateConfig) (bool, error) {
	return false, nil
}

// UpdateWithStatus implements API.
func (mc *mockRPCClient) UpdateWithStatus(_ updater.UpdateConfig) <-chan StatusMessage {
	return make(chan StatusMessage)
}

// UpdateAvailable implements API.
func (mc *mockRPCClient) UpdateAvailable(_ updater.Channel) (*updater.Version, error) {
	return nil, nil
}

// UpdateStatus implements API.
func (mc *mockRPCClient) UpdateStatus() (string, error) {
	return "", nil
}
