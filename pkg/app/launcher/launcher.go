// Package launcher launcher.go
package launcher

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/util/pathutil"
)

const (
	appsPIDFileName = "apps-pid.txt"
)

// Launcher associated errors.
var (
	ErrAppNotFound   = errors.New("app not found")
	ErrAppNotRunning = errors.New("app not running")
)

// AppLauncher is responsible for launching and keeping track of app states.
type AppLauncher struct {
	conf  AppLauncherConfig
	log   logrus.FieldLogger
	r     router.Router
	procM appserver.ProcManager
	apps  map[string]appserver.AppConfig
	mx    sync.Mutex
}

// AppLauncherConfig configures the launcher.
type AppLauncherConfig struct {
	VisorPK       cipher.PubKey
	Apps          []appserver.AppConfig
	ServerAddr    string
	BinPath       string
	LocalPath     string
	DisplayNodeIP bool
	MuxRoutes     int // Number of parallel mux routes per connection (0 = disabled)
}

// ResetConfig resets the launcher config.
func (l *AppLauncher) ResetConfig(conf AppLauncherConfig) {
	l.mx.Lock()
	defer l.mx.Unlock()

	apps := make(map[string]appserver.AppConfig, len(conf.Apps))
	for _, ac := range conf.Apps {
		apps[ac.Name] = ac
	}
	l.apps = apps

	// we shouldn't change directories of apps, it causes
	// all kinds of troubles and also doesn't make sense.
	// So, just changing individual fields
	l.conf.VisorPK = conf.VisorPK
	l.conf.Apps = conf.Apps
	l.conf.ServerAddr = conf.ServerAddr
}

// NewLauncher creates a new launcher.
func NewLauncher(log logrus.FieldLogger, conf AppLauncherConfig, dmsgC *dmsg.Client, r router.Router, procM appserver.ProcManager) (*AppLauncher, error) {
	launcher := &AppLauncher{
		conf:  conf,
		log:   log,
		r:     r,
		procM: procM,
		mx:    sync.Mutex{},
	}

	// Ensure the existence of directories.
	if err := ensureDir(&launcher.conf.BinPath); err != nil {
		return nil, err
	}
	if err := ensureDir(&launcher.conf.LocalPath); err != nil {
		return nil, err
	}

	// Prepare networks.
	skyN := appnet.NewSkywireNetworker(log.WithField("_", appnet.TypeSkynet), r)
	if sn, ok := skyN.(*appnet.SkywireNetworker); ok && conf.MuxRoutes > 1 {
		sn.MuxRoutes = conf.MuxRoutes
	}
	if err := appnet.AddNetworker(appnet.TypeSkynet, skyN); err != nil {
		return nil, err
	}
	dmsgN := appnet.NewDMSGNetworker(dmsgC)
	if err := appnet.AddNetworker(appnet.TypeDmsg, dmsgN); err != nil {
		return nil, err
	}

	// Kill old processes.
	if err := launcher.killHangingProcesses(); err != nil {
		return nil, err
	}

	// Prepare apps (autostart if necessary).
	apps := make(map[string]appserver.AppConfig, len(conf.Apps))
	for _, ac := range conf.Apps {
		apps[ac.Name] = ac
	}
	launcher.apps = apps

	return launcher, nil
}

// EnvMaker makes a list of environment variables with their values set
// It is used to let other code to decide how environment variables should be built
type EnvMaker func() ([]string, error)

// EnvMap is a mapping from application name to environment maker function
type EnvMap map[string]EnvMaker

// AutoStart auto-starts marked apps.
func (l *AppLauncher) AutoStart(envMap EnvMap) error {
	if envMap == nil {
		envMap = make(EnvMap)
	}
	log := l.log.WithField("func", "AutoStart")

	l.mx.Lock()
	defer l.mx.Unlock()

	for name, ac := range l.apps {
		if !ac.AutoStart {
			continue
		}
		var envs []string
		if makeEnvs, ok := envMap[name]; ok {
			var err error
			envs, err = makeEnvs()
			if err != nil {
				return fmt.Errorf("error running %s: %w", name, err)
			}
		}
		if err := l.startApp(name, ac.Args, envs); err != nil {
			log.WithError(err).
				WithField("app_name", name).
				WithField("args", ac.Args).
				WithField("envs", envs).
				Warn("Failed to start app.")
		}
	}

	return nil
}

// AppState returns a single app state of given name.
func (l *AppLauncher) AppState(name string) (*appserver.AppState, bool) {
	l.mx.Lock()
	defer l.mx.Unlock()

	ac, ok := l.apps[name]
	if !ok {
		return nil, false
	}
	state := &appserver.AppState{AppConfig: ac, Status: appserver.AppStatusStopped}
	// Check for stored error first (persists after proc cleanup)
	var savedError string
	if err, ok := l.procM.ErrorByName(ac.Name); ok && err != "" {
		savedError = err
		state.DetailedStatus = err
		state.Status = appserver.AppStatusErrored
	}
	if proc, ok := l.procM.ProcByName(ac.Name); ok {
		procStatus := proc.DetailedStatus()
		connSummary := proc.ConnectionsSummary()
		if connSummary != nil {
			state.Status = appserver.AppStatusRunning
			state.DetailedStatus = procStatus
		} else if proc.IsRunning() && procStatus == appserver.AppDetailedStatusRunning {
			// App is running but doesn't use connections (e.g. skynet server)
			state.Status = appserver.AppStatusRunning
			state.DetailedStatus = procStatus
		} else if proc.IsRunning() && isRawProcessApp(ac) {
			// Raw external apps (skycoin-daemon, skycoin-web — cobra
			// subcommands of the skywire binary that don't speak the
			// appserver IPC) never call SetDetailedStatus("Running")
			// and never get a ConnectionsSummary, so the two checks
			// above can't promote them out of "Starting". Trust the
			// IsRunning bit (set by the proc launcher when the child
			// successfully exec'd and Wait() hasn't returned) for
			// these — same semantics as systemd's Type=simple.
			state.Status = appserver.AppStatusRunning
			state.DetailedStatus = appserver.AppDetailedStatusRunning
		} else if savedError != "" {
			// Proc exists but not running — preserve the saved error
			state.DetailedStatus = savedError
			state.Status = appserver.AppStatusErrored
		} else {
			state.DetailedStatus = procStatus
		}
		switch procStatus {
		case appserver.AppDetailedStatusVPNConnecting, appserver.AppDetailedStatusStarting, appserver.AppDetailedStatusReconnecting:
			// Don't downgrade raw-process apps that we just promoted
			// to Running above — IsRunning() is the source of truth
			// for them since they never call SetDetailedStatus.
			if !(proc.IsRunning() && isRawProcessApp(ac)) {
				state.Status = appserver.AppStatusStarting
				state.DetailedStatus = procStatus
			}
		}
	}
	return state, true
}

// AppStates returns list of AppStates for all registered apps.
func (l *AppLauncher) AppStates() []*appserver.AppState {
	// Collect app names under lock to avoid race on l.apps map
	l.mx.Lock()
	names := make([]string, 0, len(l.apps))
	for _, app := range l.apps {
		names = append(names, app.Name)
	}
	l.mx.Unlock()

	var states []*appserver.AppState
	for _, name := range names {
		state, _ := l.AppState(name)
		states = append(states, state)
	}
	return states
}

// StartApp starts cmd with given args and env.
// If 'args' is nil, default args will be used.
func (l *AppLauncher) StartApp(cmd string, args, envs []string) error {
	return l.StartAppWithMode(cmd, args, envs, "")
}

// StartAppWithMode starts cmd with given args, env, and launcher mode override.
// launcherMode can be "internal", "external", or "" for default behavior.
func (l *AppLauncher) StartAppWithMode(cmd string, args, envs []string, launcherMode string) error {
	l.mx.Lock()
	defer l.mx.Unlock()

	return l.startAppWithMode(cmd, args, envs, launcherMode)
}

// RegisterApp registers a proc for an external app to use.
func (l *AppLauncher) RegisterApp(procConf appcommon.ProcConfig) (appcommon.ProcKey, error) {
	l.mx.Lock()
	defer l.mx.Unlock()

	return l.procM.Register(procConf)
}

// DeregisterApp de registers the proc used by an external.
func (l *AppLauncher) DeregisterApp(procKey appcommon.ProcKey) error {
	l.mx.Lock()
	defer l.mx.Unlock()

	return l.procM.Deregister(procKey)
}

func (l *AppLauncher) startApp(cmd string, args, envs []string) error {
	return l.startAppWithMode(cmd, args, envs, "")
}

func (l *AppLauncher) startAppWithMode(cmd string, args, envs []string, launcherMode string) error {
	log := l.log.WithField("func", "StartApp").WithField("cmd", cmd)

	// Obtain associated app config.
	ac, ok := l.apps[cmd]
	if !ok {
		return ErrAppNotFound
	}

	if args != nil {
		ac.Args = args
	}

	// Override launcher mode if specified
	if launcherMode != "" {
		acCopy := ac
		if launcherMode == "internal" {
			// Force internal: clear binary
			acCopy.Binary = ""
		} else if launcherMode == "external" {
			// Force external: ensure binary is set
			if ac.Binary == "" {
				// Use default binary name (app name)
				acCopy.Binary = cmd
			}
		}
		ac = acCopy
	}

	// Make proc config.
	procConf, err := makeProcConfig(l.conf, ac, envs)
	if err != nil {
		return err
	}
	// Start proc and persist pid.
	pid, err := l.procM.Start(procConf)
	if err != nil {
		return err
	}
	if err := l.persistPID(cmd, pid); err != nil {
		log.WithError(err).Warn("Failed to persist pid.")
	}

	return nil
}

// StopApp stops running app.
func (l *AppLauncher) StopApp(name string) (*appserver.Proc, error) {
	log := l.log.WithField("func", "StopApp").WithField("app_name", name)

	proc, ok := l.procM.ProcByName(name)
	if !ok {
		return nil, ErrAppNotRunning
	}

	if err := l.procM.Stop(name); err != nil {
		log.WithError(err).Warn("Failed to stop app.")
		return proc, err
	}

	return proc, nil
}

// KillApp stop an app, by its name | We use it on stop apps during its running steps by cli.
func (l *AppLauncher) KillApp(name string) error {
	log := l.log.WithField("func", "KillApp").WithField("app_name", name)

	if err := l.killApp(name); err != nil {
		log.WithError(err).Warn("Failed to kill app.")
		return err
	}

	return nil
}

// RestartApp restarts a running app.
func (l *AppLauncher) RestartApp(name, binary string) error {
	l.log.WithField("func", "RestartApp").WithField("app_name", name).
		Info("Restarting app...")

	proc, err := l.StopApp(name)
	if err != nil {
		return fmt.Errorf("failed to stop %s: %w", name, err)
	}

	cmd := proc.Cmd()
	if err := l.StartApp(binary, nil, cmd.Env); err != nil {
		return fmt.Errorf("failed to start %s: %w", name, err)
	}

	return nil
}

func makeProcConfig(lc AppLauncherConfig, ac appserver.AppConfig, envs []string) (appcommon.ProcConfig, error) {

	// Honor AppConfig WorkDir override; fall back to per-app default.
	workDir := ac.WorkDir
	if workDir == "" {
		workDir = filepath.Join(lc.LocalPath, ac.Name)
	}

	// Determine the effective HOME for this proc: the target user's
	// home dir when the AppConfig pins User, else inherit the visor's.
	// Used to (a) tilde-expand args / env values like
	// `--wallet-dir ~/.skycoin/wallets` (exec doesn't run a shell, so
	// ~/ stays literal otherwise — the binary then looks in a directory
	// called "~" and fails to find anything) and (b) inject HOME into
	// the proc's env so binaries that read $HOME directly (most do)
	// see the operator-user's home, not _skywire's.
	procHome := ""
	if ac.User != "" {
		if u, err := user.Lookup(ac.User); err == nil {
			procHome = u.HomeDir
		}
	}
	if procHome == "" {
		procHome = os.Getenv("HOME")
	}

	expandedArgs := expandHomeAll(ac.Args, procHome)

	// Merge per-app env on top of the launcher's base env. Per-app
	// values land later so they win on duplicate keys.
	mergedEnvs := append([]string(nil), envs...)
	mergedEnvs = append(mergedEnvs, ac.Env...)
	mergedEnvs = expandHomeAllEnv(mergedEnvs, procHome)
	// Auto-inject HOME for credential-dropped procs unless the operator
	// already set one explicitly. Without this, exec'd binaries running
	// as a different UID inherit the visor's HOME and write to the
	// wrong directory (e.g. _skywire's home instead of the operator's).
	if procHome != "" && !envHasKey(mergedEnvs, "HOME") {
		mergedEnvs = append(mergedEnvs, "HOME="+procHome)
	}

	procConf := appcommon.ProcConfig{
		AppName:     ac.Name,
		AppSrvAddr:  lc.ServerAddr,
		ProcKey:     appcommon.RandProcKey(),
		ProcArgs:    expandedArgs,
		ProcEnvs:    mergedEnvs,
		ProcWorkDir: workDir,
		VisorPK:     lc.VisorPK,
		RoutingPort: ac.Port,
		BinaryLoc:   filepath.Join(lc.BinPath, ac.Binary),
		LogDBLoc:    filepath.Join(lc.LocalPath, ac.Name+"_log.db"),
		ProcUser:    ac.User,
		ProcGroup:   ac.Group,
	}

	// Try to find internal app function:
	// 1. If Binary is empty, look up by Name (e.g., "skynet")
	// 2. If Binary is set, look up by Binary name (e.g., "skynet-3435" with Binary="skynet")
	// This allows multiple instances (skynet-3435, skynet-8080) to use the same internal function.
	if ac.Binary == "" {
		if runFunc, found := GetApp(ac.Name); found {
			procConf.RunFunc = runFunc
		}
	} else {
		// Binary is set - try to find internal app by binary name first
		if runFunc, found := GetApp(ac.Binary); found {
			procConf.RunFunc = runFunc
			// Clear BinaryLoc since we're using internal function
			procConf.BinaryLoc = ""
		}
	}

	err := ensureDir(&procConf.ProcWorkDir)
	return procConf, err
}

// expandHome replaces a leading "~/" or bare "~" in a path-shaped
// string with the given home directory. Other forms like "~user/..."
// are intentionally left alone — they're rare in app args and would
// require a username lookup the launcher shouldn't do per-arg.
// Returns s unchanged if home is empty (nothing to expand to).
func expandHome(s, home string) string {
	if home == "" {
		return s
	}
	if s == "~" {
		return home
	}
	if strings.HasPrefix(s, "~/") {
		return home + s[1:]
	}
	return s
}

// expandHomeAll runs expandHome on every arg in a slice; returns a
// fresh slice so the input AppConfig isn't mutated in place.
func expandHomeAll(args []string, home string) []string {
	if home == "" || len(args) == 0 {
		return args
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = expandHome(a, home)
	}
	return out
}

// expandHomeAllEnv runs expandHome on the value side of each
// KEY=VALUE entry. Common case: SKYCOINWEBWALLET=~/.skycoin/wallets
// in the on-disk config should reach the proc as
// HOME-resolved /home/.../.skycoin/wallets. Empty home → no-op.
func expandHomeAllEnv(env []string, home string) []string {
	if home == "" || len(env) == 0 {
		return env
	}
	out := make([]string, len(env))
	for i, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out[i] = kv
			continue
		}
		key, value := kv[:eq], kv[eq+1:]
		out[i] = key + "=" + expandHome(value, home)
	}
	return out
}

// envHasKey checks whether KEY=… is present in the env slice.
// Used by makeProcConfig to avoid clobbering an explicit HOME
// override the operator set in the AppConfig.
func envHasKey(env []string, key string) bool {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}

// isRawProcessApp reports whether the named app is a "raw" external
// process — one that doesn't speak the visor's appserver IPC, never
// connects an RPC gateway, and therefore never calls
// SetDetailedStatus("Running") or reports a ConnectionsSummary.
//
// Concretely this covers cobra subcommands of the skywire binary
// like 'skywire skycoin daemon' and 'skywire skycoin web' — they run
// as ordinary child processes with their own log/output streams; the
// visor knows they're alive only because Wait() hasn't returned.
//
// In-process apps (registered via launcher.RegisterApp by name or
// binary) AND in-tree external apps invoked via 'skywire app <name>'
// both DO speak the IPC and are excluded.
func isRawProcessApp(ac appserver.AppConfig) bool {
	// In-process registry hit by name → not raw.
	if _, found := GetApp(ac.Name); found {
		return false
	}
	// Multi-instance: registry hit by binary → not raw
	// (skysocks-client-2 → "skysocks-client" func).
	if ac.Binary != "" {
		if _, found := GetApp(ac.Binary); found {
			return false
		}
	}
	// 'skywire app <name>' wrapper — also speaks IPC.
	if len(ac.Args) > 0 && ac.Args[0] == "app" {
		return false
	}
	// Anything else (cobra subcommand, third-party binary) is raw.
	return true
}

func ensureDir(path *string) error {
	var err error
	if *path, err = filepath.Abs(*path); err != nil {
		return fmt.Errorf("failed to expand path: %s", err)
	}
	if _, err := os.Stat(*path); !os.IsNotExist(err) {
		return nil
	}
	if err := os.MkdirAll(*path, 0750); err != nil {
		return fmt.Errorf("failed to create dir: %s", err)
	}
	return nil
}

/*
	<<< PID management >>>
*/

func (l *AppLauncher) pidFile() (*os.File, error) {
	return os.OpenFile(filepath.Join(l.conf.LocalPath, appsPIDFileName), os.O_RDWR|os.O_CREATE, 0606) //nolint:gosec
}

func (l *AppLauncher) persistPID(appName string, pid appcommon.ProcID) error {
	log := l.log.
		WithField("func", "persistPID").
		WithField("app_name", appName).
		WithField("pid", pid)

	pidF, err := l.pidFile()
	if err != nil {
		return err
	}
	pidFName := pidF.Name()
	log = log.WithField("pid_file", pidFName)

	if err := pidF.Close(); err != nil {
		log.WithError(err).Warn("Failed to close PID file.")
	}

	data := fmt.Sprintf("%s %d\n", appName, pid)
	if err := pathutil.AtomicAppendToFile(pidFName, []byte(data)); err != nil {
		log.WithError(err).Warn("Failed to save PID to file.")
	}

	return nil
}

func (l *AppLauncher) killHangingProcesses() error {
	log := l.log.WithField("func", "killHangingProcesses")

	pidF, err := l.pidFile()
	if err != nil {
		return err
	}
	filename := pidF.Name()
	log = log.WithField("pid_file", filename)

	scan := bufio.NewScanner(pidF)
	for scan.Scan() {
		appInfo := strings.Split(scan.Text(), " ")
		if len(appInfo) != 2 {
			err := errors.New("line should be: [app name] [pid]")
			log.WithError(err).Fatal("Failed parsing pid file.")
		}

		pid, err := strconv.Atoi(appInfo[1])
		if err != nil {
			log.WithError(err).Fatal("Failed parsing pid file.")
		}

		l.killHangingProc(appInfo[0], pid)
	}
	if err = pidF.Close(); err != nil {
		log.WithError(err).Error("Failed to close file")
	}

	// empty file
	if err = pathutil.AtomicWriteFile(filename, []byte{}); err != nil {
		log.WithError(err).Error("Failed to empty pid file.")
	}

	return nil
}

func (l *AppLauncher) killHangingProc(appName string, pid int) {
	log := l.log.WithField("app_name", appName).WithField("pid", pid)

	p, err := os.FindProcess(pid)
	if err != nil {
		if runtime.GOOS != "windows" {
			log.Info("Process not found.")
		}
		return
	}

	err = p.Signal(syscall.SIGKILL)
	if err != nil {
		return
	}
	log.Info("Killed hanging child process that ran previously with this visor.")
}

func (l *AppLauncher) killApp(appName string) error {
	log := l.log.WithField("func", "killHangingProcesses")

	pidF, err := l.pidFile()
	if err != nil {
		return err
	}
	filename := pidF.Name()
	log = log.WithField("pid_file", filename)

	scan := bufio.NewScanner(pidF)
	for scan.Scan() {
		appInfo := strings.Split(scan.Text(), " ")
		if len(appInfo) != 2 {
			err := errors.New("line should be: [app name] [pid]")
			log.WithError(err).Fatal("Failed parsing pid file.")
		}

		pid, err := strconv.Atoi(appInfo[1])
		if err != nil {
			log.WithError(err).Fatal("Failed parsing pid file.")
		}
		if appInfo[0] == appName {
			l.killArbitraryProc(appInfo[0], pid)
		}
	}

	return nil
}

func (l *AppLauncher) killArbitraryProc(appName string, pid int) {
	log := l.log.WithField("app_name", appName).WithField("pid", pid)

	p, err := os.FindProcess(pid)
	if err != nil {
		if runtime.GOOS != "windows" {
			log.Info("Process not found.")
		}
		return
	}

	err = p.Signal(syscall.SIGKILL)
	if err != nil {
		return
	}
	log.Info("Killed process.")
}
