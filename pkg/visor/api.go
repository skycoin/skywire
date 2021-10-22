package visor

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/google/uuid"
	"github.com/skycoin/dmsg/buildinfo"
	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/util/netutil"
	"github.com/skycoin/skywire/pkg/util/updater"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
)

// API represents visor API.
type API interface {
	Overview() (*Overview, error)
	Summary() (*Summary, error)

	Health() (*HealthInfo, error)
	Uptime() (float64, error)

	Apps() ([]*launcher.AppState, error)
	StartApp(appName string) error
	StopApp(appName string) error
	SetAppDetailedStatus(appName, state string) error
	SetAppError(appName, stateErr string) error
	RestartApp(appName string) error
	SetAutoStart(appName string, autostart bool) error
	SetAppPassword(appName, password string) error
	SetAppPK(appName string, pk cipher.PubKey) error
	SetAppSecure(appName string, isSecure bool) error
	SetAppKillswitch(appName string, killswitch bool) error
	LogsSince(timestamp time.Time, appName string) ([]string, error)
	GetAppStats(appName string) (appserver.AppStats, error)
	GetAppConnectionsSummary(appName string) ([]appserver.ConnectionSummary, error)

	TransportTypes() ([]string, error)
	Transports(types []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error)
	Transport(tid uuid.UUID) (*TransportSummary, error)
	AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration) (*TransportSummary, error)
	RemoveTransport(tid uuid.UUID) error
	SetPublicAutoconnect(pAc bool) error

	DiscoverTransportsByPK(pk cipher.PubKey) ([]*transport.Entry, error)
	DiscoverTransportByID(id uuid.UUID) (*transport.Entry, error)

	RoutingRules() ([]routing.Rule, error)
	RoutingRule(key routing.RouteID) (routing.Rule, error)
	SaveRoutingRule(rule routing.Rule) error
	RemoveRoutingRule(key routing.RouteID) error

	RouteGroups() ([]RouteGroupInfo, error)

	Restart() error
	Exec(command string) ([]byte, error)
	Update(config updater.UpdateConfig) (bool, error)
	UpdateWithStatus(config updater.UpdateConfig) <-chan StatusMessage
	UpdateAvailable(channel updater.Channel) (*updater.Version, error)
	UpdateStatus() (string, error)
	RuntimeLogs() (string, error)

	SetMinHops(uint16) error

	GetPersistentTransports() ([]transport.PersistentTransports, error)
	SetPersistentTransports([]transport.PersistentTransports) error
}

// HealthCheckable resource returns its health status as an integer
// that corresponds to HTTP status code returned from the resource
// 200 codes correspond to a healthy resource
type HealthCheckable interface {
	Health(ctx context.Context) (int, error)
}

// Overview provides a range of basic information about a Visor.
type Overview struct {
	PubKey          cipher.PubKey        `json:"local_pk"`
	BuildInfo       *buildinfo.Info      `json:"build_info"`
	AppProtoVersion string               `json:"app_protocol_version"`
	Apps            []*launcher.AppState `json:"apps"`
	Transports      []*TransportSummary  `json:"transports"`
	RoutesCount     int                  `json:"routes_count"`
	LocalIP         string               `json:"local_ip"`
	PublicIP        string               `json:"public_ip"`
	IsSymmetricNAT  bool                 `json:"is_symmetic_nat"`
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
	if v.stunClient != nil {
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

	overview := &Overview{
		PubKey:          v.conf.PK,
		BuildInfo:       buildinfo.Get(),
		AppProtoVersion: supportedProtocolVersion,
		Apps:            v.appL.AppStates(),
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

	summary := &Summary{
		Overview:             overview,
		Health:               health,
		Uptime:               uptime,
		Routes:               extraRoutes,
		MinHops:              v.conf.Routing.MinHops,
		PersistentTransports: pts,
		SkybianBuildVersion:  skybianBuildVersion,
		BuildTag:             BuildTag,
		PublicAutoconnect:    v.conf.Transport.PublicAutoconnect,
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

// Apps implements API.
func (v *Visor) Apps() ([]*launcher.AppState, error) {
	return v.appL.AppStates(), nil
}

// SkybianBuildVersion implements API.
func (v *Visor) SkybianBuildVersion() string {
	return os.Getenv("SKYBIAN_BUILD_VERSION")
}

// StartApp implements API.
func (v *Visor) StartApp(appName string) error {
	var envs []string
	var err error
	if appName == skyenv.VPNClientName {
		// todo: can we use some kind of app start hook that will be used for both autostart
		// and start? Reason: this is also called in init for autostart
		maker := vpnEnvMaker(v.conf, v.dmsgC, v.tpM.STCPRRemoteAddrs())
		envs, err = maker()
		if err != nil {
			return err
		}
	}

	return v.appL.StartApp(appName, nil, envs)
}

// StopApp implements API.
func (v *Visor) StopApp(appName string) error {
	_, err := v.appL.StopApp(appName) //nolint:errcheck
	return err
}

// SetAppDetailedStatus implements API.
func (v *Visor) SetAppDetailedStatus(appName, status string) error {
	proc, ok := v.procM.ProcByName(appName)
	if !ok {
		return ErrAppProcNotRunning
	}

	v.log.Infof("Setting app detailed status %v for app %v", status, appName)
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
	if _, ok := v.procM.ProcByName(appName); ok { //nolint:errcheck
		v.log.Infof("Updated %v password, restarting it", appName)
		return v.appL.RestartApp(appName)
	}

	return nil
}

// SetAutoStart implements API.
func (v *Visor) SetAutoStart(appName string, autoStart bool) error {
	if _, ok := v.appL.AppState(appName); !ok {
		return ErrAppProcNotRunning
	}

	v.log.Infof("Saving auto start = %v for app %v to config", autoStart, appName)
	return v.conf.UpdateAppAutostart(v.appL, appName, autoStart)
}

// SetAppPassword implements API.
func (v *Visor) SetAppPassword(appName, password string) error {
	allowedToChangePassword := func(appName string) bool {
		allowedApps := map[string]struct{}{
			skyenv.SkysocksName:  {},
			skyenv.VPNClientName: {},
			skyenv.VPNServerName: {},
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

// SetAppKillswitch implements API.
func (v *Visor) SetAppKillswitch(appName string, killswitch bool) error {
	if appName != skyenv.VPNClientName {
		return fmt.Errorf("app %s is not allowed to set killswitch", appName)
	}

	v.log.Infof("Setting %s killswitch to %q", appName, killswitch)

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
	if appName != skyenv.VPNServerName {
		return fmt.Errorf("app %s is not allowed to change 'secure' parameter", appName)
	}

	v.log.Infof("Setting %s secure to %q", appName, isSecure)

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
			skyenv.SkysocksClientName: {},
			skyenv.VPNClientName:      {},
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

// GetAppConnectionsSummary implements API.
func (v *Visor) GetAppConnectionsSummary(appName string) ([]appserver.ConnectionSummary, error) {
	cSummary, err := v.procM.ConnectionsSummary(appName)
	if err != nil {
		return nil, err
	}

	return cSummary, nil
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

// Exec implements API.
// Exec executes a shell command. It returns combined stdout and stderr output and an error.
func (v *Visor) Exec(command string) ([]byte, error) {
	args := strings.Split(command, " ")
	cmd := exec.Command(args[0], args[1:]...) // nolint: gosec
	return cmd.CombinedOutput()
}

// Update implements API.
// Update updates visor.
// It checks if visor update is available.
// If it is, the method downloads a new visor versions, starts it and kills the current process.
func (v *Visor) Update(updateConfig updater.UpdateConfig) (bool, error) {
	updated, err := v.updater.Update(updateConfig)
	if err != nil {
		v.log.Errorf("Failed to update visor: %v", err)
		return false, err
	}

	return updated, nil
}

// UpdateWithStatus implements API.
// UpdateWithStatus combines results of Update and UpdateStatus.
func (v *Visor) UpdateWithStatus(config updater.UpdateConfig) <-chan StatusMessage {
	ch := make(chan StatusMessage, 512)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				status, err := v.UpdateStatus()
				if err != nil {
					v.log.WithError(err).Errorf("Failed to check update status")
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

		updated, err := v.Update(config)
		if err != nil {
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

// UpdateAvailable implements API.
// UpdateAvailable checks if visor update is available.
func (v *Visor) UpdateAvailable(channel updater.Channel) (*updater.Version, error) {
	version, err := v.updater.UpdateAvailable(channel)
	if err != nil {
		v.log.Errorf("Failed to check if visor update is available: %v", err)
		return nil, err
	}

	return version, nil
}

// UpdateStatus returns status of the current updating operation.
func (v *Visor) UpdateStatus() (string, error) {
	return v.updater.Status(), nil
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

// GetPersistentTransports sets min_hops routing config of visor
func (v *Visor) GetPersistentTransports() ([]transport.PersistentTransports, error) {
	return v.conf.GetPersistentTransports()
}

// SetPublicAutoconnect sets public_autoconnect config of visor
func (v *Visor) SetPublicAutoconnect(pAc bool) error {
	return v.conf.UpdatePublicAutoconnect(pAc)
}
