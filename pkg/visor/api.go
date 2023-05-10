// Package visor pkg/visor/api.go
package visor

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
	"github.com/skycoin/skywire/pkg/visor/ping"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// API represents visor API.
type API interface {
	//visor
	Overview() (*Overview, error)
	Summary() (*Summary, error)
	Health() (*HealthInfo, error)
	Uptime() (float64, error)
	Restart() error
	Reload() error
	Shutdown() error
	RuntimeLogs() (string, error)
	RemoteVisors() ([]string, error)
	GetLogRotationInterval() (visorconfig.Duration, error)
	SetLogRotationInterval(visorconfig.Duration) error
	IsDMSGClientReady() (bool, error)
	Ports() (map[string]PortDetail, error)

	//reward setting
	SetRewardAddress(string) (string, error)
	GetRewardAddress() (string, error)
	DeleteRewardAddress() error

	//app controls
	App(appName string) (*appserver.AppState, error)
	Apps() ([]*appserver.AppState, error)
	StartApp(appName string) error
	RegisterApp(procConf appcommon.ProcConfig) (appcommon.ProcKey, error)
	DeregisterApp(procKey appcommon.ProcKey) error
	StopApp(appName string) error
	SetAppDetailedStatus(appName, state string) error
	SetAppError(appName, stateErr string) error
	RestartApp(appName string) error
	SetAutoStart(appName string, autostart bool) error
	SetAppPassword(appName, password string) error
	SetAppPK(appName string, pk cipher.PubKey) error
	SetAppSecure(appName string, isSecure bool) error
	SetAppKillswitch(appName string, killswitch bool) error
	SetAppNetworkInterface(appName string, netifc string) error
	SetAppDNS(appName string, dnsaddr string) error
	DoCustomSetting(appName string, customSetting map[string]string) error
	LogsSince(timestamp time.Time, appName string) ([]string, error)
	GetAppStats(appName string) (appserver.AppStats, error)
	GetAppError(appName string) (string, error)
	GetAppConnectionsSummary(appName string) ([]appserver.ConnectionSummary, error)

	//vpn controls
	StartVPNClient(pk cipher.PubKey) error
	StopVPNClient(appName string) error
	VPNServers(version, country string) ([]servicedisc.Service, error)

	//skysocks-client controls
	StartSkysocksClient(pk string) error
	StopSkysocksClient() error
	ProxyServers(version, country string) ([]servicedisc.Service, error)

	//transports
	TransportTypes() ([]string, error)
	Transports(types []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error)
	Transport(tid uuid.UUID) (*TransportSummary, error)
	AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration) (*TransportSummary, error)
	RemoveTransport(tid uuid.UUID) error
	SetPublicAutoconnect(pAc bool) error
	GetPersistentTransports() ([]transport.PersistentTransports, error)
	SetPersistentTransports([]transport.PersistentTransports) error
	//transport discovery
	DiscoverTransportsByPK(pk cipher.PubKey) ([]*transport.Entry, error)
	DiscoverTransportByID(id uuid.UUID) (*transport.Entry, error)

	//routing
	RoutingRules() ([]routing.Rule, error)
	RoutingRule(key routing.RouteID) (routing.Rule, error)
	SaveRoutingRule(rule routing.Rule) error
	RemoveRoutingRule(key routing.RouteID) error
	RouteGroups() ([]RouteGroupInfo, error)
	SetMinHops(uint16) error

	RegisterHTTPPort(localPort int) error
	DeregisterHTTPPort(localPort int) error
	ListHTTPPorts() ([]int, error)
	Connect(remotePK cipher.PubKey, remotePort, localPort int) (uuid.UUID, error)
	Disconnect(id uuid.UUID) error
	List() (map[uuid.UUID]*appnet.ForwardConn, error)
	DialPing(config PingConfig) error
	Ping(config PingConfig) ([]time.Duration, error)
	StopPing(pk cipher.PubKey) error

	TestVisor(config PingConfig) ([]TestResult, error)
}

// HealthCheckable resource returns its health status as an integer
// that corresponds to HTTP status code returned from the resource
// 200 codes correspond to a healthy resource
type HealthCheckable interface {
	Health(ctx context.Context) (int, error)
}

// Overview provides a range of basic information about a Visor.
type Overview struct {
	PubKey              cipher.PubKey         `json:"local_pk"`
	BuildInfo           *buildinfo.Info       `json:"build_info"`
	AppProtoVersion     string                `json:"app_protocol_version"`
	Apps                []*appserver.AppState `json:"apps"`
	Transports          []*TransportSummary   `json:"transports"`
	RoutesCount         int                   `json:"routes_count"`
	LocalIP             string                `json:"local_ip"`
	PublicIP            string                `json:"public_ip"`
	IsSymmetricNAT      bool                  `json:"is_symmetic_nat"`
	Hypervisors         []cipher.PubKey       `json:"hypervisors"`
	ConnectedHypervisor []cipher.PubKey       `json:"connected_hypervisor"`
}

// Overview implements API.
func (v *Visor) Overview() (*Overview, error) {
	var tSummaries []*TransportSummary
	var publicIP string
	var isSymmetricNAT bool
	if v == nil {
		panic("v is nil")
	}
	if v.tpM == nil {
		panic("tpM is nil")
	}
	v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		tSummaries = append(tSummaries,
			newTransportSummary(v.tpM, tp, true, v.router.SetupIsTrusted(tp.Remote())))
		return true
	})

	if v.isStunReady() {
		switch v.stunClient.NATType {
		case stun.NATNone, stun.NATFull, stun.NATRestricted, stun.NATPortRestricted:
			publicIP = v.stunClient.PublicIP.IP()
			isSymmetricNAT = false
		case stun.NATSymmetric, stun.NATSymmetricUDPFirewall:
			isSymmetricNAT = true
		case stun.NATError, stun.NATUnknown, stun.NATBlocked:
			publicIP = v.stunClient.NATType.String()
			isSymmetricNAT = false
		}
	}

	apps := []*appserver.AppState{}
	if v.appL != nil {
		apps = v.appL.AppStates()
	}

	overview := &Overview{
		PubKey:          v.conf.PK,
		BuildInfo:       buildinfo.Get(),
		AppProtoVersion: supportedProtocolVersion,
		Apps:            apps,
		Transports:      tSummaries,
		RoutesCount:     v.router.RoutesCount(),
		PublicIP:        publicIP,
		IsSymmetricNAT:  isSymmetricNAT,
	}

	localIPs, err := netutil.DefaultNetworkInterfaceIPs()
	if err != nil {
		return nil, err
	}

	if len(localIPs) > 0 {
		// should be okay to have the first one, in the case of
		// active network interface, there's usually just a single IP
		overview.LocalIP = localIPs[0].String()
	}

	overview.Hypervisors = v.conf.Hypervisors

	for connectedHV := range v.connectedHypervisors {
		overview.ConnectedHypervisor = append(overview.ConnectedHypervisor, connectedHV)
	}

	return overview, nil
}

// Summary provides detailed info including overview and health of the visor.
type Summary struct {
	Overview             *Overview                        `json:"overview"`
	Health               *HealthInfo                      `json:"health"`
	Uptime               float64                          `json:"uptime"`
	Routes               []routingRuleResp                `json:"routes"`
	IsHypervisor         bool                             `json:"is_hypervisor,omitempty"`
	DmsgStats            *dmsgtracker.DmsgClientSummary   `json:"dmsg_stats"`
	Online               bool                             `json:"online"`
	MinHops              uint16                           `json:"min_hops"`
	PersistentTransports []transport.PersistentTransports `json:"persistent_transports"`
	SkybianBuildVersion  string                           `json:"skybian_build_version"`
	RewardAddress        string                           `json:"reward_address"`
	BuildTag             string                           `json:"build_tag"`
	PublicAutoconnect    bool                             `json:"public_autoconnect"`
}

// BuildTag variable that will set when building binary
var BuildTag string

// Summary implements API.
func (v *Visor) Summary() (*Summary, error) {
	overview, err := v.Overview()
	if err != nil {
		return nil, fmt.Errorf("overview")
	}

	health, err := v.Health()
	if err != nil {
		return nil, fmt.Errorf("health")
	}

	uptime, err := v.Uptime()
	if err != nil {
		return nil, fmt.Errorf("uptime")
	}

	routes, err := v.RoutingRules()
	if err != nil {
		return nil, fmt.Errorf("routes")
	}

	skybianBuildVersion := v.SkybianBuildVersion()

	extraRoutes := make([]routingRuleResp, 0, len(routes))
	for _, route := range routes {
		extraRoutes = append(extraRoutes, routingRuleResp{
			Key:     route.KeyRouteID(),
			Rule:    hex.EncodeToString(route),
			Summary: route.Summary(),
		})
	}

	pts, err := v.conf.GetPersistentTransports()
	if err != nil {
		return nil, fmt.Errorf("pts")
	}

	dmsgStatValue := &dmsgtracker.DmsgClientSummary{}
	if v.isDTMReady() {
		dmsgTracker, _ := v.dtm.Get(v.conf.PK) //nolint
		dmsgStatValue = &dmsgTracker
	}

	rewardAddress, err := v.GetRewardAddress()
	if err != nil {
		v.log.Warn(err)
	}

	summary := &Summary{
		Overview:             overview,
		Health:               health,
		Uptime:               uptime,
		Routes:               extraRoutes,
		MinHops:              v.conf.Routing.MinHops,
		PersistentTransports: pts,
		SkybianBuildVersion:  skybianBuildVersion,
		BuildTag:             BuildTag,
		RewardAddress:        rewardAddress,
		PublicAutoconnect:    v.conf.Transport.PublicAutoconnect,
		DmsgStats:            dmsgStatValue,
	}

	return summary, nil
}

// HealthInfo carries information about visor's services health represented as boolean value (i32 value)
type HealthInfo struct {
	ServicesHealth string `json:"services_health"`
}

// internalHealthInfo contains information of the status of the visor itself.
// It's thread-safe, and could be used in multiple goroutines
type internalHealthInfo int32

// newHealthInfo creates
func newInternalHealthInfo() *internalHealthInfo {
	return new(internalHealthInfo)
}

// init sets the internalHealthInfo status to initial value (2)
func (h *internalHealthInfo) init() {
	atomic.StoreInt32((*int32)(h), 2)
}

// set sets the internalHealthInfo status to true.
func (h *internalHealthInfo) set() {
	atomic.StoreInt32((*int32)(h), 1)
}

// unset sets the internalHealthInfo to false.
func (h *internalHealthInfo) unset() {
	atomic.StoreInt32((*int32)(h), 0)
}

// value gets the internalHealthInfo value
func (h *internalHealthInfo) value() string {
	val := atomic.LoadInt32((*int32)(h))
	switch val {
	case 0:
		return "connecting"
	case 1:
		return "healthy"
	default:
		return "connecting"
	}
}

// Health implements API.
func (v *Visor) Health() (*HealthInfo, error) {
	if v.isServicesHealthy == nil {
		return &HealthInfo{}, nil
	}
	return &HealthInfo{ServicesHealth: v.isServicesHealthy.value()}, nil
}

// Uptime implements API.
func (v *Visor) Uptime() (float64, error) {
	return time.Since(v.startedAt).Seconds(), nil
}

// SetRewardAddress implements API.
func (v *Visor) SetRewardAddress(p string) (string, error) {
	path := v.conf.LocalPath + "/" + visorconfig.RewardFile
	err := os.WriteFile(path, []byte(p), 0644) //nolint
	if err != nil {
		return p, fmt.Errorf("failed to write config to file. err=%v", err)
	}
	// generate survey after set/update reward address
	visorconfig.GenerateSurvey(v.conf, v.log, false, v.rawSurvey)
	return p, nil
}

// GetRewardAddress implements API.
func (v *Visor) GetRewardAddress() (string, error) {
	path := v.conf.LocalPath + "/" + visorconfig.RewardFile
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		file, err := os.Create(filepath.Clean(path))
		if err != nil {
			return "", fmt.Errorf("failed to create config file. err=%v", err)
		}
		err = file.Close()
		if err != nil {
			return "", fmt.Errorf("failed to close config file. err=%v", err)
		}
	}
	rConfig, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("failed to read config file. err=%v", err)
	}
	return string(rConfig), nil
}

// DeleteRewardAddress implements API.
func (v *Visor) DeleteRewardAddress() error {

	path := v.conf.LocalPath + "/" + visorconfig.RewardFile
	err := os.Remove(path)
	if err != nil {
		return fmt.Errorf("Error deleting file. err=%v", err)
	}
	return nil
}

// Apps implements API.
func (v *Visor) Apps() ([]*appserver.AppState, error) {
	// check app launcher availability
	if v.appL == nil {
		return nil, ErrAppLauncherNotAvailable
	}
	return v.appL.AppStates(), nil
}

// App implements API.
func (v *Visor) App(appName string) (*appserver.AppState, error) {
	// check app launcher availability
	if v.appL == nil {
		return nil, ErrAppLauncherNotAvailable
	}
	appState, ok := v.appL.AppState(appName)
	if !ok {
		return &appserver.AppState{}, ErrAppProcNotRunning
	}
	return appState, nil
}

// SkybianBuildVersion implements API.
func (v *Visor) SkybianBuildVersion() string {
	return os.Getenv("SKYBIAN_BUILD_VERSION")
}

// StartApp implements API.
func (v *Visor) StartApp(appName string) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	var envs []string
	var err error
	if appName == visorconfig.VPNClientName {
		// todo: can we use some kind of app start hook that will be used for both autostart
		// and start? Reason: this is also called in init for autostart

		// check transport manager availability
		if v.tpM == nil {
			return ErrTrpMangerNotAvailable
		}
		maker := vpnEnvMaker(v.conf, v.dmsgC, v.dmsgDC, v.tpM.STCPRRemoteAddrs())
		envs, err = maker()
		if err != nil {
			return err
		}

		if v.GetVPNClientAddress() == "" {
			return errors.New("VPN server pub key is missing")
		}
	}
	// check process manager availability
	if v.procM != nil {
		return v.appL.StartApp(appName, nil, envs)
	}
	return ErrProcNotAvailable
}

// RegisterApp implements API.
func (v *Visor) RegisterApp(procConf appcommon.ProcConfig) (appcommon.ProcKey, error) {
	// check process manager and app launcher availability
	if v.appL == nil {
		return appcommon.ProcKey{}, ErrAppLauncherNotAvailable
	}
	if v.procM != nil {
		return v.appL.RegisterApp(procConf)
	}
	return appcommon.ProcKey{}, ErrProcNotAvailable
}

// DeregisterApp implements API.
func (v *Visor) DeregisterApp(procKey appcommon.ProcKey) error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if v.procM != nil {
		return v.appL.DeregisterApp(procKey)
	}
	return ErrProcNotAvailable
}

// StopApp implements API.
func (v *Visor) StopApp(appName string) error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if v.procM != nil {
		_, err := v.appL.StopApp(appName) //nolint:errcheck
		return err
	}
	return ErrProcNotAvailable
}

// StartVPNClient implements API.
func (v *Visor) StartVPNClient(pk cipher.PubKey) error {
	var envs []string
	var err error
	if v.tpM == nil {
		return ErrTrpMangerNotAvailable
	}
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if len(v.conf.Launcher.Apps) == 0 {
		return errors.New("no vpn app configuration found")
	}

	for index, app := range v.conf.Launcher.Apps {
		if app.Name == visorconfig.VPNClientName {
			// we set the args in memory and pass it in `v.appL.StartApp`
			// unlike the api method `StartApp` where `nil` is passed in `v.appL.StartApp` as args
			// but the args are set in the config
			v.conf.Launcher.Apps[index].Args = []string{"-srv", pk.Hex()}
			maker := vpnEnvMaker(v.conf, v.dmsgC, v.dmsgDC, v.tpM.STCPRRemoteAddrs())
			envs, err = maker()
			if err != nil {
				return err
			}

			if v.GetVPNClientAddress() == "" {
				return errors.New("VPN server pub key is missing")
			}

			// check process manager availability
			if v.procM != nil {
				return v.appL.StartApp(visorconfig.VPNClientName, v.conf.Launcher.Apps[index].Args, envs)
			}
			return ErrProcNotAvailable
		}
	}
	return errors.New("no vpn app configuration found")
}

// StopVPNClient implements API.
func (v *Visor) StopVPNClient(appName string) error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if v.procM != nil {
		_, err := v.appL.StopApp(appName) //nolint:errcheck
		return err
	}
	return ErrProcNotAvailable
}

// StartSkysocksClient implements API.
func (v *Visor) StartSkysocksClient(serverKey string) error {
	var envs []string
	if v.tpM == nil {
		return ErrTrpMangerNotAvailable
	}
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if len(v.conf.Launcher.Apps) == 0 {
		return errors.New("no skysocks-client app configuration found")
	}

	for index, app := range v.conf.Launcher.Apps {
		if app.Name == visorconfig.SkysocksClientName {
			if v.GetSkysocksClientAddress() == "" && serverKey == "" {
				return errors.New("Skysocks server pub key is missing")
			}

			if serverKey != "" {
				var pk cipher.PubKey
				if err := pk.Set(serverKey); err != nil {
					return err
				}
				v.SetAppPK(visorconfig.SkysocksClientName, pk) //nolint
				// we set the args in memory and pass it in `v.appL.StartApp`
				// unlike the api method `StartApp` where `nil` is passed in `v.appL.StartApp` as args
				// but the args are set in the config
				v.conf.Launcher.Apps[index].Args = []string{"-srv", pk.Hex()}
			} else {
				var pk cipher.PubKey
				if err := pk.Set(v.GetSkysocksClientAddress()); err != nil {
					return err
				}
				v.conf.Launcher.Apps[index].Args = []string{"-srv", pk.Hex()}
			}

			// check process manager availability
			if v.procM != nil {
				return v.appL.StartApp(visorconfig.SkysocksClientName, v.conf.Launcher.Apps[index].Args, envs)
			}
			return ErrProcNotAvailable
		}
	}
	return errors.New("no skysocks-client app configuration found")
}

// StopSkysocksClient implements API.
func (v *Visor) StopSkysocksClient() error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if v.procM != nil {
		_, err := v.appL.StopApp(visorconfig.SkysocksClientName) //nolint:errcheck
		return err
	}
	return ErrProcNotAvailable
}

// SetAppDetailedStatus implements API.
func (v *Visor) SetAppDetailedStatus(appName, status string) error {
	proc, ok := v.procM.ProcByName(appName)
	if !ok {
		return ErrAppProcNotRunning
	}

	proc.SetDetailedStatus(status)

	return nil
}

// SetAppError implements API.
func (v *Visor) SetAppError(appName, appErr string) error {
	proc, ok := v.procM.ProcByName(appName)
	if !ok {
		return ErrAppProcNotRunning
	}

	v.log.Infof("Setting error %v for app %v", appErr, appName)
	proc.SetError(appErr)

	return nil
}

// RestartApp implements API.
func (v *Visor) RestartApp(appName string) error {
	// check app launcher availability
	if v.appL == nil {
		v.log.Warn("app launcher not ready yet")
		return ErrAppLauncherNotAvailable
	}
	if _, ok := v.procM.ProcByName(appName); ok { //nolint:errcheck
		v.log.Infof("Updated %v password, restarting it", appName)
		return v.appL.RestartApp(appName, appName)
	}

	return nil
}

// SetAutoStart implements API.
func (v *Visor) SetAutoStart(appName string, autoStart bool) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}

	if _, ok := v.appL.AppState(appName); !ok {
		return ErrAppProcNotRunning
	}

	v.log.Infof("Saving auto start = %v for app %v to config", autoStart, appName)
	return v.conf.UpdateAppAutostart(v.appL, appName, autoStart)
}

// SetAppPassword implements API.
func (v *Visor) SetAppPassword(appName, password string) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	allowedToChangePassword := func(appName string) bool {
		allowedApps := map[string]struct{}{
			visorconfig.SkysocksName:  {},
			visorconfig.VPNClientName: {},
			visorconfig.VPNServerName: {},
		}

		_, ok := allowedApps[appName]
		return ok
	}

	if !allowedToChangePassword(appName) {
		return fmt.Errorf("app %s is not allowed to change password", appName)
	}

	v.log.Infof("Changing %s password to %q", appName, password)

	const (
		passcodeArgName = "-passcode"
	)
	if err := v.conf.UpdateAppArg(v.appL, appName, passcodeArgName, password); err != nil {
		return err
	}

	v.log.Infof("Updated %v password", appName)

	return nil
}

// SetAppNetworkInterface implements API.
func (v *Visor) SetAppNetworkInterface(appName, netifc string) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}

	if visorconfig.VPNServerName != appName {
		return fmt.Errorf("app %s is not allowed to set network interface", appName)
	}

	v.log.Infof("Changing %s network interface to %v", appName, netifc)

	const (
		netifcArgName = "--netifc"
	)
	if err := v.conf.UpdateAppArg(v.appL, appName, netifcArgName, netifc); err != nil {
		return err
	}

	v.log.Infof("Updated %v network interface", appName)

	return nil
}

// SetAppKillswitch implements API.
func (v *Visor) SetAppKillswitch(appName string, killswitch bool) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}

	if appName != visorconfig.VPNClientName {
		return fmt.Errorf("app %s is not allowed to set killswitch", appName)
	}

	v.log.Infof("Setting %s killswitch to %v", appName, killswitch)

	const (
		killSwitchArg = "--killswitch"
	)
	if err := v.conf.UpdateAppArg(v.appL, appName, killSwitchArg, killswitch); err != nil {
		return err
	}

	v.log.Infof("Updated %v killswitch state", appName)

	return nil
}

// SetAppSecure implements API.
func (v *Visor) SetAppSecure(appName string, isSecure bool) error {
	if appName != visorconfig.VPNServerName {
		return fmt.Errorf("app %s is not allowed to change 'secure' parameter", appName)
	}

	v.log.Infof("Setting %s secure to %v", appName, isSecure)

	const (
		secureArgName = "--secure"
	)
	if err := v.conf.UpdateAppArg(v.appL, appName, secureArgName, isSecure); err != nil {
		return err
	}
	v.log.Infof("Updated %v secure state", appName)

	return nil
}

// SetAppPK implements API.
func (v *Visor) SetAppPK(appName string, pk cipher.PubKey) error {
	allowedToChangePK := func(appName string) bool {
		allowedApps := map[string]struct{}{
			visorconfig.SkysocksClientName: {},
			visorconfig.VPNClientName:      {},
		}

		_, ok := allowedApps[appName]
		return ok
	}

	if !allowedToChangePK(appName) {
		return fmt.Errorf("app %s is not allowed to change PK", appName)
	}

	v.log.Infof("Changing %s PK to %q", appName, pk)

	const (
		pkArgName = "-srv"
	)
	if err := v.conf.UpdateAppArg(v.appL, appName, pkArgName, pk.String()); err != nil {
		return err
	}

	v.log.Infof("Updated %v PK", appName)

	return nil
}

// SetAppDNS implements API.
func (v *Visor) SetAppDNS(appName string, dnsAddr string) error {
	allowedToChangePK := func(appName string) bool {
		allowedApps := map[string]struct{}{
			skyenv.VPNClientName: {},
		}

		_, ok := allowedApps[appName]
		return ok
	}

	if !allowedToChangePK(appName) {
		return fmt.Errorf("app %s is not allowed to change DNS Address", appName)
	}

	v.log.Infof("Changing %s DNS Address to %q", appName, dnsAddr)

	const (
		pkArgName = "-dns"
	)

	if err := v.conf.UpdateAppArg(v.appL, appName, pkArgName, dnsAddr); err != nil {
		return err
	}

	v.log.Infof("Updated %v DNS Address", appName)

	return nil
}

// DoCustomSetting implents API.
func (v *Visor) DoCustomSetting(appName string, customSetting map[string]string) error {

	v.log.Infof("Changing %s Settings to %q", appName, customSetting)
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if err := v.conf.DeleteAppArg(v.appL, appName); err != nil {
		v.log.Warn("An error occurs deleting old arguments.")
		return err
	}

	for field, value := range customSetting {
		if err := v.conf.UpdateAppArg(v.appL, appName, fmt.Sprintf("-%s", field), value); err != nil {
			return err
		}
	}

	v.log.Info("Updated Settings.")

	return nil
}

// LogsSince implements API.
func (v *Visor) LogsSince(timestamp time.Time, appName string) ([]string, error) {
	proc, ok := v.procM.ProcByName(appName)
	if !ok {
		return nil, fmt.Errorf("proc of app name '%s' is not found", appName)
	}

	res, err := proc.Logs().LogsSince(timestamp)
	if err != nil {
		return nil, err
	}

	return res, nil
}

// GetAppStats implements API.
func (v *Visor) GetAppStats(appName string) (appserver.AppStats, error) {
	stats, err := v.procM.Stats(appName)
	if err != nil {
		return appserver.AppStats{}, err
	}

	return stats, nil
}

// GetAppError implements API.
func (v *Visor) GetAppError(appName string) (string, error) {
	appErr, _ := v.procM.ErrorByName(appName)
	return appErr, nil
}

// GetAppConnectionsSummary implements API.
func (v *Visor) GetAppConnectionsSummary(appName string) ([]appserver.ConnectionSummary, error) {
	// check process manager availability
	if v.procM != nil {
		cSummary, err := v.procM.ConnectionsSummary(appName)
		if err != nil {
			return nil, err
		}
		return cSummary, nil
	}
	return nil, ErrProcNotAvailable
}

// VPNServers gets available public VPN server from service discovery URL
func (v *Visor) VPNServers(version, country string) ([]servicedisc.Service, error) {
	log := logging.MustGetLogger("vpnservers")
	vLog := logging.NewMasterLogger()
	vLog.SetLevel(logrus.InfoLevel)

	sdClient := servicedisc.NewClient(log, vLog, servicedisc.Config{
		Type:          servicedisc.ServiceTypeVPN,
		PK:            v.conf.PK,
		SK:            v.conf.SK,
		DiscAddr:      v.conf.Launcher.ServiceDisc,
		DisplayNodeIP: v.conf.Launcher.DisplayNodeIP,
	}, &http.Client{Timeout: time.Duration(20) * time.Second}, "")
	vpnServers, err := sdClient.Services(context.Background(), 0, version, country)
	if err != nil {
		v.log.Error("Error getting public vpn servers: ", err)
		return nil, err
	}
	return vpnServers, nil
}

// ProxyServers gets available public VPN server from service discovery URL
func (v *Visor) ProxyServers(version, country string) ([]servicedisc.Service, error) {
	log := logging.MustGetLogger("proxyservers")
	vLog := logging.NewMasterLogger()
	vLog.SetLevel(logrus.InfoLevel)

	sdClient := servicedisc.NewClient(log, vLog, servicedisc.Config{
		Type:          servicedisc.ServiceTypeProxy,
		PK:            v.conf.PK,
		SK:            v.conf.SK,
		DiscAddr:      v.conf.Launcher.ServiceDisc,
		DisplayNodeIP: v.conf.Launcher.DisplayNodeIP,
	}, &http.Client{Timeout: time.Duration(20) * time.Second}, "")
	proxyServers, err := sdClient.Services(context.Background(), 0, version, country)
	if err != nil {
		v.log.Error("Error getting public vpn servers: ", err)
		return nil, err
	}
	return proxyServers, nil
}

// PublicVisors gets available public public visors from service discovery URL
func (v *Visor) PublicVisors(version, country string) ([]servicedisc.Service, error) {
	log := logging.MustGetLogger("public_visors")
	vLog := logging.NewMasterLogger()
	vLog.SetLevel(logrus.InfoLevel)

	sdClient := servicedisc.NewClient(log, vLog, servicedisc.Config{
		Type:          servicedisc.ServiceTypeVisor,
		PK:            v.conf.PK,
		SK:            v.conf.SK,
		DiscAddr:      v.conf.Launcher.ServiceDisc,
		DisplayNodeIP: v.conf.Launcher.DisplayNodeIP,
	}, &http.Client{Timeout: time.Duration(20) * time.Second}, "")
	publicVisors, err := sdClient.Services(context.Background(), 0, version, country)
	if err != nil {
		v.log.Error("Error getting public vpn servers: ", err)
		return nil, err
	}
	return publicVisors, nil
}

// RemoteVisors return list of connected remote visors
func (v *Visor) RemoteVisors() ([]string, error) {
	var visors []string
	for _, conn := range v.remoteVisors {
		visors = append(visors, conn.Addr.PK.String())
	}
	return visors, nil
}

// PortDetail type of port details
type PortDetail struct {
	Port string
	Type string
}

// Ports return list of all ports used by visor services and apps
func (v *Visor) Ports() (map[string]PortDetail, error) {
	ctx := context.Background()
	var ports = make(map[string]PortDetail)

	if v.conf.Hypervisor != nil {
		ports["hypervisor"] = PortDetail{Port: fmt.Sprint(strings.Split(v.conf.Hypervisor.HTTPAddr, ":")[1]), Type: "TCP"}
	}

	ports["dmsg_pty"] = PortDetail{Port: fmt.Sprint(v.conf.Dmsgpty.DmsgPort), Type: "DMSG"}
	ports["cli_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(v.conf.CLIAddr, ":")[1]), Type: "TCP"}
	ports["proc_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(v.conf.Launcher.ServerAddr, ":")[1]), Type: "TCP"}
	ports["stcp_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(v.conf.STCP.ListeningAddress, ":")[1]), Type: "TCP"}

	if v.arClient != nil {
		sudphPort := v.arClient.Addresses(ctx)
		if sudphPort != "" {
			ports["sudph"] = PortDetail{Port: sudphPort, Type: "UDP"}
		}
	}
	if v.stunClient != nil {
		if v.stunClient.PublicIP != nil {
			ports["public_visor"] = PortDetail{Port: fmt.Sprint(v.stunClient.PublicIP.Port()), Type: "TCP"}
		}
	}
	if v.dmsgC != nil {
		dmsgSessions := v.dmsgC.AllSessions()
		for i, session := range dmsgSessions {
			ports[fmt.Sprintf("dmsg_session_%d", i)] = PortDetail{Port: strings.Split(session.LocalTCPAddr().String(), ":")[1], Type: "TCP"}
		}

		dmsgStreams := v.dmsgC.AllStreams()
		for i, stream := range dmsgStreams {
			ports[fmt.Sprintf("dmsg_stream_%d", i)] = PortDetail{Port: strings.Split(stream.LocalAddr().String(), ":")[1], Type: "DMSG"}
		}
	}
	if v.procM != nil {
		apps, _ := v.Apps() //nolint
		for _, app := range apps {
			port, err := v.procM.GetAppPort(app.Name)
			if err == nil {
				ports[app.Name] = PortDetail{Port: fmt.Sprint(port), Type: "SKYNET"}

				switch app.Name {
				case "skysocks_client":
					ports["skysocks_client_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(skyenv.SkysocksClientAddr, ":")[1]), Type: "TCP"}
				case "skychat":
					ports["skychat_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(skyenv.SkychatAddr, ":")[1]), Type: "TCP"}
				}
			}
		}
	}
	return ports, nil
}

// TransportTypes implements API.
func (v *Visor) TransportTypes() ([]string, error) {
	var types []string
	for _, netType := range v.tpM.Networks() {
		types = append(types, string(netType))
	}
	return types, nil
}

// Transports implements API.
func (v *Visor) Transports(types []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error) {
	var result []*TransportSummary

	typeIncluded := func(tType network.Type) bool {
		if types != nil {
			for _, ft := range types {
				if string(tType) == ft {
					return true
				}
			}
			return false
		}
		return true
	}
	pkIncluded := func(localPK, remotePK cipher.PubKey) bool {
		if pks != nil {
			for _, fpk := range pks {
				if localPK == fpk || remotePK == fpk {
					return true
				}
			}
			return false
		}
		return true
	}
	v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if typeIncluded(tp.Type()) && pkIncluded(v.tpM.Local(), tp.Remote()) {
			result = append(result, newTransportSummary(v.tpM, tp, logs, v.router.SetupIsTrusted(tp.Remote())))
		}
		return true
	})

	return result, nil
}

// Transport implements API.
func (v *Visor) Transport(tid uuid.UUID) (*TransportSummary, error) {
	tp := v.tpM.Transport(tid)
	if tp == nil {
		return nil, ErrNotFound
	}

	return newTransportSummary(v.tpM, tp, true, v.router.SetupIsTrusted(tp.Remote())), nil
}

// AddTransport implements API.
func (v *Visor) AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration) (*TransportSummary, error) {
	ctx := context.Background()

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second*20)
		defer cancel()
	}

	v.log.Debugf("Saving transport to %v via %v", remote, tpType)

	tp, err := v.tpM.SaveTransport(ctx, remote, network.Type(tpType), transport.LabelUser)
	if err != nil {
		return nil, err
	}

	v.log.Debugf("Saved transport to %v via %v, label %s", remote, tpType, tp.Entry.Label)

	return newTransportSummary(v.tpM, tp, false, v.router.SetupIsTrusted(tp.Remote())), nil
}

// RemoveTransport implements API.
func (v *Visor) RemoveTransport(tid uuid.UUID) error {
	v.tpM.DeleteTransport(tid)
	return nil
}

// DiscoverTransportsByPK implements API.
func (v *Visor) DiscoverTransportsByPK(pk cipher.PubKey) ([]*transport.Entry, error) {
	tpD := v.tpDiscClient()

	entries, err := tpD.GetTransportsByEdge(context.Background(), pk)
	if err != nil {
		return nil, err
	}

	return entries, nil
}

// DiscoverTransportByID implements API.
func (v *Visor) DiscoverTransportByID(id uuid.UUID) (*transport.Entry, error) {
	tpD := v.tpDiscClient()

	entry, err := tpD.GetTransportByID(context.Background(), id)
	if err != nil {
		return nil, err
	}

	return entry, nil
}

// RoutingRules implements API.
func (v *Visor) RoutingRules() ([]routing.Rule, error) {
	return v.router.Rules(), nil
}

// PingConfig use as configuration for ping command
type PingConfig struct {
	PK          cipher.PubKey
	Tries       int
	PcktSize    int
	PubVisCount int
	AutoTp      bool
}

// DialPing implements API.
func (v *Visor) DialPing(conf PingConfig) error {
	if conf.PK == v.conf.PK {
		return fmt.Errorf("Visor cannot ping itself")
	}
	v.pingPcktSize = conf.PcktSize
	// waiting for at least one transport to initialize
	<-v.tpM.Ready()

	addr := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: v.conf.PK,
		Port:   routing.Port(skyenv.SkyPingPort),
	}

	var err error
	var conn net.Conn

	ctx := context.TODO()
	var r = netutil.NewRetrier(v.log, 2*time.Second, netutil.DefaultMaxBackoff, 1, 2)
	err = r.Do(ctx, func() error {
		conn, err = appnet.Ping(conf.PK, addr, conf.AutoTp)
		return err
	})
	if err != nil {
		return err
	}

	skywireConn, isSkywireConn := conn.(*appnet.SkywireConn)
	if !isSkywireConn {
		return fmt.Errorf("Can't get such info from this conn")
	}
	v.pingConnMx.Lock()
	v.pingConns[conf.PK] = ping.Conn{
		Conn:    skywireConn,
		Latency: make(chan time.Duration),
	}
	v.pingConnMx.Unlock()
	return nil
}

// Ping implements API.
func (v *Visor) Ping(conf PingConfig) ([]time.Duration, error) {
	v.pingConnMx.Lock()
	defer v.pingConnMx.Unlock()
	latencies := []time.Duration{}
	data := make([]byte, conf.PcktSize*1024)
	for i := 1; i <= conf.Tries; i++ {
		skywireConn := v.pingConns[conf.PK].Conn
		msg := ping.Msg{
			Timestamp: time.Now(),
			PingPk:    conf.PK,
			Data:      data,
		}
		p, err := json.Marshal(msg)
		if err != nil {
			return latencies, err
		}
		pingSizeMsg := ping.SizeMsg{
			Size: len(p),
		}
		size, err := json.Marshal(pingSizeMsg)
		if err != nil {
			return latencies, err
		}
		_, err = skywireConn.Write(size)
		if err != nil {
			return latencies, err
		}

		buf := make([]byte, 32*1024)
		_, err = skywireConn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return latencies, err
			}
		}
		_, err = skywireConn.Write(p)
		if err != nil {
			return latencies, err
		}
		latencies = append(latencies, <-v.pingConns[conf.PK].Latency)
	}
	return latencies, nil
}

// StopPing implements API.
func (v *Visor) StopPing(pk cipher.PubKey) error {
	v.pingConnMx.Lock()
	defer v.pingConnMx.Unlock()

	skywireConn := v.pingConns[pk].Conn
	err := skywireConn.Close()
	if err != nil {
		return err
	}
	delete(v.pingConns, pk)
	return nil
}

// TestResult type of test result
type TestResult struct {
	PK     string
	Max    string
	Min    string
	Mean   string
	Status string
}

// TestVisor trying to test visor
func (v *Visor) TestVisor(conf PingConfig) ([]TestResult, error) {
	result := []TestResult{}
	if v.dmsgC == nil {
		return result, errors.New("dmsgC is not available")
	}

	publicVisors, err := v.dmsgC.AllEntries(context.TODO())
	if err != nil {
		return result, err
	}

	if conf.PubVisCount < len(publicVisors) {
		publicVisors = publicVisors[:conf.PubVisCount+1]
	}

	for _, publicVisor := range publicVisors {
		if publicVisor == v.conf.PK.Hex() {
			continue
		}

		if err := conf.PK.UnmarshalText([]byte(publicVisor)); err != nil {
			continue
		}
		err := v.DialPing(conf)
		if err != nil {
			result = append(result, TestResult{PK: conf.PK.String(), Max: fmt.Sprint(0), Min: fmt.Sprint(0), Mean: fmt.Sprint(0), Status: "Failed"})
			continue
		}
		latencies, err := v.Ping(conf)
		if err != nil {
			go v.StopPing(conf.PK) //nolint
			result = append(result, TestResult{PK: conf.PK.String(), Max: fmt.Sprint(0), Min: fmt.Sprint(0), Mean: fmt.Sprint(0), Status: "Failed"})
			continue
		}
		var max, min, mean, sumLatency time.Duration
		min = time.Duration(10000000000)
		for _, latency := range latencies {
			if latency > max {
				max = latency
			}
			if latency < min {
				min = latency
			}
			sumLatency += latency
		}
		mean = sumLatency / time.Duration(len(latencies))
		result = append(result, TestResult{PK: conf.PK.String(), Max: fmt.Sprint(max), Min: fmt.Sprint(min), Mean: fmt.Sprint(mean), Status: "Success"})
		v.StopPing(conf.PK) //nolint
	}
	return result, nil
}

// RoutingRule implements API.
func (v *Visor) RoutingRule(key routing.RouteID) (routing.Rule, error) {
	return v.router.Rule(key)
}

// SaveRoutingRule implements API.
func (v *Visor) SaveRoutingRule(rule routing.Rule) error {
	return v.router.SaveRule(rule)
}

// RemoveRoutingRule implements API.
func (v *Visor) RemoveRoutingRule(key routing.RouteID) error {
	v.router.DelRules([]routing.RouteID{key})
	return nil
}

// RouteGroups implements API.
func (v *Visor) RouteGroups() ([]RouteGroupInfo, error) {
	var routegroups []RouteGroupInfo

	rules := v.router.Rules()
	for _, rule := range rules {
		if rule.Type() != routing.RuleReverse {
			continue
		}

		fwdRID := rule.NextRouteID()
		rule, err := v.router.Rule(fwdRID)
		if err != nil {
			return nil, err
		}

		routegroups = append(routegroups, RouteGroupInfo{
			ConsumeRule: rule,
			FwdRule:     rule,
		})
	}

	return routegroups, nil
}

// Restart implements API.
func (v *Visor) Restart() error {
	if v.restartCtx == nil {
		return ErrMalformedRestartContext
	}

	return v.restartCtx.Restart()
}

// Reload implements API.
func (v *Visor) Reload() error {
	if v.restartCtx == nil {
		return ErrMalformedRestartContext
	}
	return reload(v)
}

// Shutdown implements API.
func (v *Visor) Shutdown() error {
	if v.restartCtx == nil {
		return ErrMalformedRestartContext
	}
	defer os.Exit(0)
	return v.Close()
}

// RuntimeLogs returns visor runtime logs
func (v *Visor) RuntimeLogs() (string, error) {
	var builder strings.Builder
	builder.WriteString("[")
	logs, _ := v.logstore.GetLogs()
	builder.WriteString(strings.Join(logs, ","))
	builder.WriteString("]")
	return builder.String(), nil
}

// SetMinHops sets min_hops routing config of visor
func (v *Visor) SetMinHops(in uint16) error {
	return v.conf.UpdateMinHops(in)
}

// SetPersistentTransports sets min_hops routing config of visor
func (v *Visor) SetPersistentTransports(pTps []transport.PersistentTransports) error {
	v.tpM.SetPTpsCache(pTps)
	return v.conf.UpdatePersistentTransports(pTps)
}

// GetPersistentTransports gets min_hops routing config of visor
func (v *Visor) GetPersistentTransports() ([]transport.PersistentTransports, error) {
	return v.conf.GetPersistentTransports()
}

// SetLogRotationInterval sets log_rotation_interval config of visor
func (v *Visor) SetLogRotationInterval(d visorconfig.Duration) error {
	return v.conf.UpdateLogRotationInterval(d)
}

// GetLogRotationInterval gets log_rotation_interval config of visor
func (v *Visor) GetLogRotationInterval() (visorconfig.Duration, error) {
	return v.conf.GetLogRotationInterval()
}

// SetPublicAutoconnect sets public_autoconnect config of visor
func (v *Visor) SetPublicAutoconnect(pAc bool) error {
	return v.conf.UpdatePublicAutoconnect(pAc)
}

// GetVPNClientAddress get PK address of server set on vpn-client
func (v *Visor) GetVPNClientAddress() string {
	for _, v := range v.conf.Launcher.Apps {
		if v.Name == visorconfig.VPNClientName {
			for index := range v.Args {
				if v.Args[index] == "-srv" {
					return v.Args[index+1]
				}
			}
		}
	}
	return ""
}

// GetSkysocksClientAddress get PK address of server set on skysocks-client
func (v *Visor) GetSkysocksClientAddress() string {
	for _, v := range v.conf.Launcher.Apps {
		if v.Name == visorconfig.SkysocksClientAddr {
			for index := range v.Args {
				if v.Args[index] == "-srv" {
					return v.Args[index+1]
				}
			}
		}
	}
	return ""
}

// IsDMSGClientReady return availability of dsmg client
func (v *Visor) IsDMSGClientReady() (bool, error) {
	if v.isDTMReady() {
		dmsgTracker, _ := v.dtm.Get(v.conf.PK) //nolint
		if dmsgTracker.ServerPK.Hex()[:5] != "00000" {
			return true, nil
		}
	}
	return false, errors.New("dmsg client is not ready")
}

// RegisterHTTPPort implements API.
func (v *Visor) RegisterHTTPPort(localPort int) error {
	v.allowedMX.Lock()
	defer v.allowedMX.Unlock()
	ok := isPortAvailable(v.log, localPort)
	if ok {
		return fmt.Errorf("No connection on local port :%v", localPort)
	}
	if v.allowedPorts[localPort] {
		return fmt.Errorf("Port :%v already registered", localPort)
	}
	v.allowedPorts[localPort] = true
	return nil
}

// DeregisterHTTPPort implements API.
func (v *Visor) DeregisterHTTPPort(localPort int) error {
	v.allowedMX.Lock()
	defer v.allowedMX.Unlock()
	if !v.allowedPorts[localPort] {
		return fmt.Errorf("Port :%v not registered", localPort)
	}
	delete(v.allowedPorts, localPort)
	return nil
}

// ListHTTPPorts implements API.
func (v *Visor) ListHTTPPorts() ([]int, error) {
	v.allowedMX.Lock()
	defer v.allowedMX.Unlock()
	keys := make([]int, 0, len(v.allowedPorts))
	for k := range v.allowedPorts {
		keys = append(keys, k)
	}
	return keys, nil
}

// Connect implements API.
func (v *Visor) Connect(remotePK cipher.PubKey, remotePort, localPort int) (uuid.UUID, error) {
	ok := isPortAvailable(v.log, localPort)
	if !ok {
		return uuid.UUID{}, fmt.Errorf(":%v local port already in use", localPort)
	}
	connApp := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: remotePK,
		Port:   routing.Port(skyenv.SkyForwardingServerPort),
	}
	conn, err := appnet.Dial(connApp)
	if err != nil {
		return uuid.UUID{}, err
	}
	remoteConn, err := appnet.WrapConn(conn)
	if err != nil {
		return uuid.UUID{}, err
	}

	cMsg := clientMsg{
		Port: remotePort,
	}

	clientMsg, err := json.Marshal(cMsg)
	if err != nil {
		return uuid.UUID{}, err
	}
	_, err = remoteConn.Write([]byte(clientMsg))
	if err != nil {
		return uuid.UUID{}, err
	}
	v.log.Debugf("Msg sent %s", clientMsg)

	buf := make([]byte, 32*1024)
	n, err := remoteConn.Read(buf)
	if err != nil {
		return uuid.UUID{}, err
	}
	var sReply serverReply
	err = json.Unmarshal(buf[:n], &sReply)
	if err != nil {
		return uuid.UUID{}, err
	}
	v.log.Debugf("Received: %v", sReply)

	if sReply.Error != nil {
		sErr := sReply.Error
		v.log.WithError(fmt.Errorf(*sErr)).Error("Server closed with error")
		return uuid.UUID{}, fmt.Errorf(*sErr)
	}
	forwardConn := appnet.NewForwardConn(v.log, remoteConn, remotePort, localPort)
	forwardConn.Serve()
	return forwardConn.ID, nil
}

// Disconnect implements API.
func (v *Visor) Disconnect(id uuid.UUID) error {
	forwardConn := appnet.GetForwardConn(id)
	return forwardConn.Close()
}

// List implements API.
func (v *Visor) List() (map[uuid.UUID]*appnet.ForwardConn, error) {
	return appnet.GetAllForwardConns(), nil
}

func isPortAvailable(log *logging.Logger, port int) bool {
	timeout := time.Second
	conn, err := net.DialTimeout("tcp", fmt.Sprintf(":%v", port), timeout)
	if err != nil {
		return true
	}
	if conn != nil {
		defer closeConn(log, conn)
		return false
	}
	return true
}

func isPortRegistered(port int, v *Visor) bool {
	ports, err := v.ListHTTPPorts()
	if err != nil {
		return false
	}
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}
