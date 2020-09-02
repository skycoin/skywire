package visor

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/skycoin/dmsg/buildinfo"
	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/util/updater"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
)

// API represents visor API.
type API interface {
	Summary() (*Summary, error)
	ExtraSummary() (*ExtraSummary, error)

	Health() (*HealthInfo, error)
	Uptime() (float64, error)

	Apps() ([]*launcher.AppState, error)
	StartApp(appName string) error
	StopApp(appName string) error
	SetAutoStart(appName string, autostart bool) error
	SetAppPassword(appName, password string) error
	SetAppPK(appName string, pk cipher.PubKey) error
	LogsSince(timestamp time.Time, appName string) ([]string, error)

	TransportTypes() ([]string, error)
	Transports(types []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error)
	Transport(tid uuid.UUID) (*TransportSummary, error)
	AddTransport(remote cipher.PubKey, tpType string, public bool, timeout time.Duration) (*TransportSummary, error)
	RemoveTransport(tid uuid.UUID) error

	DiscoverTransportsByPK(pk cipher.PubKey) ([]*transport.EntryWithStatus, error)
	DiscoverTransportByID(id uuid.UUID) (*transport.EntryWithStatus, error)

	RoutingRules() ([]routing.Rule, error) // TODO(nkryuchkov): improve API
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
}

// Summary provides a summary of a Skywire Visor.
type Summary struct {
	PubKey          cipher.PubKey        `json:"local_pk"`
	BuildInfo       *buildinfo.Info      `json:"build_info"`
	AppProtoVersion string               `json:"app_protocol_version"`
	Apps            []*launcher.AppState `json:"apps"`
	Transports      []*TransportSummary  `json:"transports"`
	RoutesCount     int                  `json:"routes_count"`
}

// Summary implements API.
func (v *Visor) Summary() (*Summary, error) {
	var summaries []*TransportSummary
	if v == nil {
		panic("v is nil")
	}
	if v.tpM == nil {
		panic("tpM is nil")
	}
	v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
		summaries = append(summaries,
			newTransportSummary(v.tpM, tp, true, v.router.SetupIsTrusted(tp.Remote())))
		return true
	})

	summary := &Summary{
		PubKey:          v.conf.PK,
		BuildInfo:       buildinfo.Get(),
		AppProtoVersion: supportedProtocolVersion,
		Apps:            v.appL.AppStates(),
		Transports:      summaries,
		RoutesCount:     v.router.RoutesCount(),
	}

	return summary, nil
}

// ExtraSummary provides an extra summary of a Skywire Visor.
type ExtraSummary struct {
	Summary *Summary                        `json:"summary"`
	Dmsg    []dmsgtracker.DmsgClientSummary `json:"dmsg"`
	Health  *HealthInfo                     `json:"health"`
	Uptime  float64                         `json:"uptime"`
	Routes  []routingRuleResp               `json:"routes"`
}

// ExtraSummary implements API.
func (v *Visor) ExtraSummary() (*ExtraSummary, error) {
	summary, err := v.Summary()
	if err != nil {
		return nil, fmt.Errorf("summary")
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

// HealthInfo carries information about visor's external services health represented as http status codes
type HealthInfo struct {
	TransportDiscovery int `json:"transport_discovery"`
	RouteFinder        int `json:"route_finder"`
	SetupNode          int `json:"setup_node"`
	UptimeTracker      int `json:"uptime_tracker"`
	AddressResolver    int `json:"address_resolver"`
}

// Health implements API.
func (v *Visor) Health() (*HealthInfo, error) {
	ctx := context.Background()

	healthInfo := &HealthInfo{
		TransportDiscovery: http.StatusNotFound,
		RouteFinder:        http.StatusNotFound,
		SetupNode:          http.StatusNotFound,
		UptimeTracker:      http.StatusNotFound,
		AddressResolver:    http.StatusNotFound,
	}

	if tdClient := v.tpDiscClient(); tdClient != nil {
		tdStatus, err := tdClient.Health(ctx)
		if err != nil {
			v.log.WithError(err).Warnf("Failed to check transport discovery health")

			healthInfo.TransportDiscovery = http.StatusInternalServerError
		}

		healthInfo.TransportDiscovery = tdStatus
	}

	if rfClient := v.routeFinderClient(); rfClient != nil {
		rfStatus, err := rfClient.Health(ctx)
		if err != nil {
			v.log.WithError(err).Warnf("Failed to check route finder health")

			healthInfo.RouteFinder = http.StatusInternalServerError
		}

		healthInfo.RouteFinder = rfStatus
	}

	// TODO(evanlinjin): This should actually poll the setup nodes services.
	if len(v.conf.Routing.SetupNodes) == 0 {
		healthInfo.SetupNode = http.StatusNotFound
	} else {
		healthInfo.SetupNode = http.StatusOK
	}

	if utClient := v.uptimeTrackerClient(); utClient != nil {
		utStatus, err := utClient.Health(ctx)
		if err != nil {
			v.log.WithError(err).Warnf("Failed to check uptime tracker health")

			healthInfo.UptimeTracker = http.StatusInternalServerError
		}

		healthInfo.UptimeTracker = utStatus
	}

	if arClient := v.addressResolverClient(); arClient != nil {
		arStatus, err := arClient.Health(ctx)
		if err != nil {
			v.log.WithError(err).Warnf("Failed to check address resolver health")

			healthInfo.AddressResolver = http.StatusInternalServerError
		}

		healthInfo.AddressResolver = arStatus
	}

	return healthInfo, nil
}

// Uptime implements API.
func (v *Visor) Uptime() (float64, error) {
	return time.Since(v.startedAt).Seconds(), nil
}

// Apps implements API.
func (v *Visor) Apps() ([]*launcher.AppState, error) {
	return v.appL.AppStates(), nil
}

// StartApp implements API.
func (v *Visor) StartApp(appName string) error {
	var envs []string
	var err error
	if appName == skyenv.VPNClientName {
		envs, err = makeVPNEnvs(v.conf, v.net)
		if err != nil {
			return err
		}
	}

	return v.appL.StartApp(appName, nil, envs)
}

// StopApp implements API.
func (v *Visor) StopApp(appName string) error {
	_, err := v.appL.StopApp(appName)
	return err
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

	if _, ok := v.procM.ProcByName(appName); ok {
		v.log.Infof("Updated %v password, restarting it", appName)
		return v.appL.RestartApp(appName)
	}

	v.log.Infof("Updated %v password", appName)

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

	if _, ok := v.procM.ProcByName(appName); ok {
		v.log.Infof("Updated %v PK, restarting it", appName)
		return v.appL.RestartApp(appName)
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

// TransportTypes implements API.
func (v *Visor) TransportTypes() ([]string, error) {
	return v.tpM.Networks(), nil
}

// Transports implements API.
func (v *Visor) Transports(types []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error) {
	var result []*TransportSummary

	typeIncluded := func(tType string) bool {
		if types != nil {
			for _, ft := range types {
				if tType == ft {
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
func (v *Visor) AddTransport(remote cipher.PubKey, tpType string, public bool, timeout time.Duration) (*TransportSummary, error) {
	ctx := context.Background()

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Second*20)
		defer cancel()
	}

	v.log.Debugf("Saving transport to %v via %v", remote, tpType)

	tp, err := v.tpM.SaveTransport(ctx, remote, tpType)
	if err != nil {
		return nil, err
	}

	v.log.Debugf("Saved transport to %v via %v", remote, tpType)

	return newTransportSummary(v.tpM, tp, false, v.router.SetupIsTrusted(tp.Remote())), nil
}

// RemoveTransport implements API.
func (v *Visor) RemoveTransport(tid uuid.UUID) error {
	v.tpM.DeleteTransport(tid)
	return nil
}

// DiscoverTransportsByPK implements API.
func (v *Visor) DiscoverTransportsByPK(pk cipher.PubKey) ([]*transport.EntryWithStatus, error) {
	tpD := v.tpDiscClient()

	entries, err := tpD.GetTransportsByEdge(context.Background(), pk)
	if err != nil {
		return nil, err
	}

	return entries, nil
}

// DiscoverTransportByID implements API.
func (v *Visor) DiscoverTransportByID(id uuid.UUID) (*transport.EntryWithStatus, error) {
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
