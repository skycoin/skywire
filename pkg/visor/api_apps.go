// api_apps.go contains app management API methods.
package visor

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

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

// StartApp implements API.
func (v *Visor) StartApp(appName string) error {
	return v.StartAppWithMode(appName, "")
}

// StartAppWithMode implements API with launcher mode override.
// launcherMode can be "internal", "external", or "" for default behavior.
func (v *Visor) StartAppWithMode(appName, launcherMode string) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	var envs []string
	var err error
	if appName == skyenv.VPNClientName {
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
		return v.appL.StartAppWithMode(appName, nil, envs, launcherMode)
	}
	return ErrProcNotAvailable
}

// AddApp implement API.
func (v *Visor) AddApp(appName, binaryName string) error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	return v.conf.AddAppConfig(v.appL, appName, binaryName)
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
		_, err := v.appL.StopApp(appName) //nolint:errcheck,gosec
		return err
	}
	return ErrProcNotAvailable
}

// KillApp implements API.
func (v *Visor) KillApp(appName string) error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if v.procM != nil {
		return v.appL.KillApp(appName)
	}
	return ErrProcNotAvailable
}

// StartVPNClient implements API.
func (v *Visor) StartVPNClient(pk cipher.PubKey) error {
	return v.StartVPNClientWithMode(pk, "")
}

// StartVPNClientWithMode implements API with launcher mode override.
func (v *Visor) StartVPNClientWithMode(pk cipher.PubKey, launcherMode string) error {
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
		if app.Name == skyenv.VPNClientName {
			// we set the args in memory and pass it in `v.appL.StartAppWithMode`
			// unlike the api method `StartApp` where `nil` is passed in `v.appL.StartApp` as args
			// but the args are set in the config
			v.conf.Launcher.Apps[index].Args = []string{"app", "vpn-client", "--srv", pk.Hex()}
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
				return v.appL.StartAppWithMode(skyenv.VPNClientName, v.conf.Launcher.Apps[index].Args, envs, launcherMode)
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
		_, err := v.appL.StopApp(appName) //nolint:errcheck,gosec
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
		if app.Name == skyenv.SkysocksClientName {
			if v.GetSkysocksClientAddress() == "" && serverKey == "" {
				return errors.New("skysocks server pub key is missing")
			}

			if serverKey != "" {
				var pk cipher.PubKey
				if err := pk.Set(serverKey); err != nil {
					return err
				}
				if err := v.SetAppPK(skyenv.SkysocksClientName, pk); err != nil {
					return err
				}
				// we set the args in memory and pass it in `v.appL.StartApp`
				// unlike the api method `StartApp` where `nil` is passed in `v.appL.StartApp` as args
				// but the args are set in the config
				v.conf.Launcher.Apps[index].Args = []string{"app", "skysocks-client", "--srv", pk.Hex(), "--addr", skyenv.SkysocksClientAddr}
			} else {
				var pk cipher.PubKey
				if err := pk.Set(v.GetSkysocksClientAddress()); err != nil {
					return err
				}
				v.conf.Launcher.Apps[index].Args = []string{"app", "skysocks-client", "--srv", pk.Hex(), "--addr", skyenv.SkysocksClientAddr}
			}

			// check process manager availability
			if v.procM != nil {
				return v.appL.StartApp(skyenv.SkysocksClientName, v.conf.Launcher.Apps[index].Args, envs)
			}
			return ErrProcNotAvailable
		}
	}
	return errors.New("no skysocks-client app configuration found")
}

// StopSkysocksClients implements API.
func (v *Visor) StopSkysocksClients() error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if v.procM != nil {
		for _, app := range v.conf.Launcher.Apps {
			for _, args := range app.Args {
				if args == skyenv.SkysocksClientName {
					if _, err := v.appL.StopApp(app.Name); err != nil { //nolint:errcheck,gosec
						v.log.WithError(err).Warnf("Failed to stop app %s", app.Name)
					}
				}
			}
		}
		return nil
	}
	return ErrProcNotAvailable
}

// RestartApp implements API.
func (v *Visor) RestartApp(appName string) error {
	// check app launcher availability
	if v.appL == nil {
		v.log.Warn("app launcher not ready yet")
		return ErrAppLauncherNotAvailable
	}
	if _, ok := v.procM.ProcByName(appName); ok { //nolint:errcheck,gosec
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
		passcodeArgName = "--passcode"
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

	if skyenv.VPNServerName != appName {
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
		pkArgName = "--srv"
	)
	if err := v.conf.UpdateAppArg(v.appL, appName, pkArgName, pk.String()); err != nil {
		return err
	}

	v.log.Infof("Updated %v PK", appName)

	return nil
}

// SetAppKillswitch implements API.
func (v *Visor) SetAppKillswitch(appName string, killswitch bool) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}

	if appName != skyenv.VPNClientName {
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
	if appName != skyenv.VPNServerName {
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

// SetAppAddress implements API.
func (v *Visor) SetAppAddress(appName string, address string) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}

	if appName != skyenv.SkychatName {
		return fmt.Errorf("app %s is not allowed to set addr", appName)
	}

	if len(address) < 5 || (address[:1] != ":" && address[:2] != "*:") {
		return fmt.Errorf("invalid addr value: %s", address)
	}

	forLocalhostOnly := address[:1] == ":"
	prefix := 2
	if forLocalhostOnly {
		prefix = 1
	}

	portNumber, err := strconv.Atoi(address[prefix:])
	if err != nil || portNumber < 1025 || portNumber > 65536 {
		return fmt.Errorf("invalid port number: %s", strconv.Itoa(portNumber))
	}

	v.log.Infof("Setting %s addr to %v", appName, address)

	const (
		addrArg = "--addr"
	)
	if err := v.conf.UpdateAppArg(v.appL, appName, addrArg, address); err != nil {
		return err
	}

	v.log.Infof("Updated %v addr state", appName)

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
		pkArgName = "--dns"
	)

	if err := v.conf.UpdateAppArg(v.appL, appName, pkArgName, dnsAddr); err != nil {
		return err
	}

	v.log.Infof("Updated %v DNS Address", appName)

	return nil
}

// DoCustomSetting implents API.
func (v *Visor) DoCustomSetting(appName string, customSetting map[string]any) error {
	fmt.Println(customSetting)
	v.log.Infof("Changing %s Settings to %v", appName, customSetting)
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	if err := v.conf.DeleteAppArg(v.appL, appName); err != nil {
		v.log.Warn("An error occurs deleting old arguments.")
		return err
	}

	if value, ok := customSetting["appPort"]; ok && value != 0 {
		if err := v.conf.UpdateAppPort(v.appL, appName, customSetting["appPort"].(uint16)); err != nil {
			return err
		}
		delete(customSetting, "appPort")
	}

	if err := v.conf.UpdateAppArgBatch(v.appL, appName, customSetting); err != nil {
		return err
	}

	v.log.Info("Updated Settings.")

	return nil
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

// FetchUptimeTrackerData implements API
func (v *Visor) FetchUptimeTrackerData(pk string) ([]byte, error) {
	var body []byte
	var pubkey cipher.PubKey

	if pk != "" {
		err := pubkey.Set(pk)
		if err != nil {
			return body, fmt.Errorf("invalid or missing public key")
		}
	}
	if v.uptimeTracker == nil {
		return body, fmt.Errorf("uptime tracker module not available")
	}
	return v.uptimeTracker.FetchUptimes(context.TODO(), pk)
}

// GetVPNClientAddress get PK address of server set on vpn-client
func (v *Visor) GetVPNClientAddress() string {
	for _, v := range v.conf.Launcher.Apps {
		if v.Name == skyenv.VPNClientName {
			for index := range v.Args {
				if v.Args[index] == "--srv" && index+1 < len(v.Args) {
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
		if v.Name == skyenv.SkysocksClientAddr {
			for index := range v.Args {
				if v.Args[index] == "--srv" && index+1 < len(v.Args) {
					return v.Args[index+1]
				}
			}
		}
	}
	return ""
}
