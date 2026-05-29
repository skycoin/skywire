// api_apps.go contains app management API methods.
package visor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/policy"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// ErrRouterNotAvailable is returned by RPC handlers that need a
// router (e.g. SetAppRoutingPolicy) before the visor has finished
// router init.
var ErrRouterNotAvailable = errors.New("router not available")

// ErrPolicyHookUnavailable is returned when no router.DialHook is
// installed — typically because no policy was configured at startup
// and the runtime swap is the first policy this visor has seen.
// (Not currently reachable: init_router.go always installs a hook
// for the runtime-swap path. Reserved for the case where DialHook
// is explicitly disabled.)
var ErrPolicyHookUnavailable = errors.New("routing-policy hook unavailable")

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

// StartApp implements API. Resolves the launcher mode from the
// AppConfig.LauncherMode field if set, falling back to the visor's
// default. StartAppWithMode("") matches; StartAppWithMode(<explicit>)
// still wins per-call.
func (v *Visor) StartApp(appName string) error {
	mode := ""
	if v.conf != nil {
		for _, app := range v.conf.Launcher.Apps {
			if app.Name == appName {
				mode = app.LauncherMode
				break
			}
		}
	}
	return v.StartAppWithMode(appName, mode)
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

// SetAppRoutingPolicy installs (or clears) a per-app routing
// policy at runtime. Backend is dispatched by file extension
// — `.wasm` uses the WASM loader, anything else (including the
// inline-Starlark form) uses the Starlark loader. The swap is
// live: the running app picks up the new policy on its next
// dial, no restart required.
//
// path == "" or "none" clears the per-app entry, falling the
// app back to the visor-wide policy (or no policy if none is
// configured).
//
// Returns ErrRouterNotAvailable when called before router
// initialization completes.
func (v *Visor) SetAppRoutingPolicy(appName, path string) error {
	if v.router == nil {
		return ErrRouterNotAvailable
	}
	dh := v.router.DialHook()
	hook, ok := dh.(*policy.Hook)
	if !ok || hook == nil {
		return ErrPolicyHookUnavailable
	}

	logger := func(format string, args ...interface{}) {
		v.log.Infof(format, args...)
	}
	provider := newVisorPolicyProvider(v)

	pe, err := loadPolicyEngine(path, provider, logger)
	if err != nil {
		return fmt.Errorf("load policy %q: %w", path, err)
	}

	var newEngine policy.Engine
	if pe != nil {
		if werr := pe.watch(logger); werr != nil {
			v.log.WithError(werr).WithField("app", appName).
				Warn("Per-app routing policy hot-reload watcher failed to start; policy still active but won't auto-reload.")
		}
		newEngine = pe.engine
	}
	prev := hook.RegisterApp(appName, newEngine)
	if prev != nil {
		// Close the displaced engine to release its wazero
		// runtime / fsnotify watcher. Idempotent for both
		// backends; safe to call after the new engine is
		// already serving dials.
		if cerr := prev.Close(); cerr != nil {
			v.log.WithError(cerr).WithField("app", appName).
				Warn("Closing displaced routing-policy engine returned an error.")
		}
	}
	if newEngine != nil {
		v.pushCloseStack(pe.tag+".app:"+appName+".runtime", newEngine.Close)
		v.log.WithField("app", appName).WithField("source", newEngine.Source()).
			Info("Per-app routing policy installed at runtime.")
	} else {
		v.log.WithField("app", appName).
			Info("Per-app routing policy cleared at runtime.")
	}
	return nil
}

// AddApp implement API.
func (v *Visor) AddApp(appName, binaryName string) error {
	// check process manager and app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	return v.conf.AddAppConfig(v.appL, appName, binaryName)
}

// DeleteApp implements API. Stops the running proc (if any), then
// removes the app entry from the launcher config and flushes the
// updated config to disk. Returns an error if the app doesn't
// exist or the proc fails to stop cleanly.
func (v *Visor) DeleteApp(appName string) error {
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	// Best-effort stop. StopApp on a not-running app returns
	// ErrAppProcNotRunning, which we tolerate so delete still
	// proceeds for stopped entries.
	if err := v.StopApp(appName); err != nil &&
		!errors.Is(err, ErrAppProcNotRunning) {
		v.log.WithField("app", appName).WithError(err).
			Warn("DeleteApp: stop returned an error, continuing with config removal")
	}
	return v.conf.DeleteAppConfig(v.appL, appName)
}

// SetAppArgs replaces the entire Args slice on the named app.
// Used by the universal app-settings panel where the operator
// edits args as a shell-string; the caller (HTTP handler / CLI)
// is responsible for tokenizing.
func (v *Visor) SetAppArgs(appName string, args []string) error {
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	return v.conf.UpdateAppArgsFull(v.appL, appName, args)
}

// SetAppEnvFull replaces the entire Env slice on the named app.
// Counterpart to SetAppArgs for environment vars; the per-key
// SetAppEnv RPC is still available for targeted mutation.
func (v *Visor) SetAppEnvFull(appName string, env []string) error {
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	return v.conf.UpdateAppEnvFull(v.appL, appName, env)
}

// SetAppLauncherMode persists the launcher-mode preference for an
// app entry. Empty clears the override; "internal" / "external"
// pin the mode. Honored by StartApp on the next start.
func (v *Visor) SetAppLauncherMode(appName, mode string) error {
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	return v.conf.UpdateAppLauncherMode(v.appL, appName, mode)
}

// appHelpCache memoizes the --help output so the universal panel's
// "Show flags" disclosure is cheap to open repeatedly. Keyed by the
// resolved exec path + leading positional args (the subcommand path
// for embedded apps); cleared whenever the running skywire binary's
// mtime changes (operator rebuilt it).
var (
	appHelpCacheMu sync.Mutex
	appHelpCache   = make(map[string]appHelpCacheEntry)
)

type appHelpCacheEntry struct {
	mtime int64
	help  string
}

// AppHelp returns the `--help` output for the named app. Three
// resolution paths to cover the launcher's three app shapes:
//
//  1. In-process registered app (launcher.RegisterApp("skysocks", …))
//     with args like ["app", "skysocks", …] → exec the running
//     skywire binary with the leading non-flag args + "--help"
//     (i.e. "skywire app skysocks --help").
//
//  2. Embedded cobra subcommand of skywire (skycoin daemon,
//     skycoin web) with args like ["skycoin", "daemon", …] → same
//     pattern: "skywire skycoin daemon --help".
//
//  3. External standalone binary at <BinPath>/<Binary> → exec the
//     file directly with --help.
//
// Cached per resolved (exec, args-prefix); invalidated by the
// running skywire binary's mtime so a rebuild surfaces fresh help.
func (v *Visor) AppHelp(appName string) (string, error) {
	if v.appL == nil {
		return "", ErrAppLauncherNotAvailable
	}
	var ac *appserver.AppConfig
	for i := range v.conf.Launcher.Apps {
		if v.conf.Launcher.Apps[i].Name == appName {
			ac = &v.conf.Launcher.Apps[i]
			break
		}
	}
	if ac == nil {
		return "", fmt.Errorf("app %q not found", appName)
	}

	execPath, helpArgs, err := resolveAppHelpExec(*ac, v.conf.Launcher.BinPath)
	if err != nil {
		return "", err
	}

	mtime, err := mtimeNanos(execPath)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", execPath, err)
	}
	cacheKey := execPath + "\x00" + strings.Join(helpArgs, "\x00")

	appHelpCacheMu.Lock()
	if entry, ok := appHelpCache[cacheKey]; ok && entry.mtime == mtime {
		appHelpCacheMu.Unlock()
		return entry.help, nil
	}
	appHelpCacheMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// execPath comes from os.Executable() (running skywire binary)
	// or BinPath/Binary from the visor's own AppConfig — both
	// operator-controlled, not user input. helpArgs is positional
	// app-args plus the literal "--help" string.
	cmd := exec.CommandContext(ctx, execPath, helpArgs...) //nolint:gosec
	out, runErr := cmd.CombinedOutput()
	// Many CLI binaries exit non-zero on --help; treat the captured
	// output as authoritative regardless and only surface a hard
	// error when nothing was emitted.
	if len(out) == 0 && runErr != nil {
		return "", fmt.Errorf("exec %s %s: %w", execPath, strings.Join(helpArgs, " "), runErr)
	}
	help := string(out)
	appHelpCacheMu.Lock()
	appHelpCache[cacheKey] = appHelpCacheEntry{mtime: mtime, help: help}
	appHelpCacheMu.Unlock()
	return help, nil
}

// resolveAppHelpExec picks the exec path and args used to fetch
// help for the given AppConfig. See AppHelp for the resolution
// strategy. Returns (execPath, [args..., "--help"]).
func resolveAppHelpExec(ac appserver.AppConfig, binPath string) (string, []string, error) {
	// Take the leading run of non-flag positional args. For the
	// in-process / cobra cases this is the subcommand path
	// (e.g. ["app", "skysocks"] or ["skycoin", "daemon"]).
	var positional []string
	for _, a := range ac.Args {
		if strings.HasPrefix(a, "-") {
			break
		}
		positional = append(positional, a)
	}

	if len(positional) > 0 {
		// Embedded subcommand or in-process app — invoke the running
		// skywire binary with the positional path + --help.
		exe, err := os.Executable()
		if err != nil {
			return "", nil, fmt.Errorf("os.Executable: %w", err)
		}
		return exe, append(positional, "--help"), nil
	}

	// No positional args. Two cases:
	//   - In-process app registered via launcher.RegisterApp (the
	//     launcher resolves this by Name or Binary; same registry
	//     lookup tells us whether 'skywire app <name>' is the right
	//     help target). Internal apps whose AppConfig.Args is pure
	//     flags (vpn-client: ["--dns", "1.1.1.1"]) land here.
	//   - External standalone binary at <BinPath>/<Binary>.
	registryName := ac.Name
	if ac.Binary != "" {
		registryName = ac.Binary
	}
	if _, found := launcher.GetApp(registryName); found {
		exe, err := os.Executable()
		if err != nil {
			return "", nil, fmt.Errorf("os.Executable: %w", err)
		}
		return exe, []string{"app", registryName, "--help"}, nil
	}

	if ac.Binary == "" {
		return "", nil, fmt.Errorf("app %q has no positional args and no binary; can't resolve help target", ac.Name)
	}
	external := filepath.Join(binPath, ac.Binary)
	return external, []string{"--help"}, nil
}

func mtimeNanos(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return st.ModTime().UnixNano(), nil
}

// SetAppEnv implements API. Sets / replaces / deletes a KEY=value
// entry on the named app's environment. Empty value deletes.
func (v *Visor) SetAppEnv(appName, key, value string) error {
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	return v.conf.UpdateAppEnv(v.appL, appName, key, value)
}

// SetAppEnvBatch is the multi-key counterpart to SetAppEnv. Single
// config flush + launcher reset for the whole batch.
func (v *Visor) SetAppEnvBatch(appName string, env map[string]string) error {
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	return v.conf.UpdateAppEnvBatch(v.appL, appName, env)
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

// SetAppWhitelist implements API. Updates the comma-separated PK
// whitelist that gates incoming connections on skysocks / vpn-server.
// Empty value means "open to all authenticated peers" — same
// semantics as omitting the flag.
//
// Replaces the older SetAppPassword: the apps no longer recognize a
// --passcode flag, so updating that arg was a silent no-op (or
// worse, would prevent the app from starting if the flag definition
// was tightened).
func (v *Visor) SetAppWhitelist(appName, whitelist string) error {
	// check app launcher availability
	if v.appL == nil {
		return ErrAppLauncherNotAvailable
	}
	allowed := map[string]struct{}{
		skyenv.SkysocksName:  {},
		skyenv.VPNServerName: {},
	}
	if _, ok := allowed[appName]; !ok {
		return fmt.Errorf("app %s does not support a connection whitelist", appName)
	}

	v.log.Infof("Updating %s whitelist (%d chars)", appName, len(whitelist))

	const whitelistArgName = "--whitelist"
	if err := v.conf.UpdateAppArg(v.appL, appName, whitelistArgName, whitelist); err != nil {
		return err
	}

	v.log.Infof("Updated %v whitelist", appName)
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
