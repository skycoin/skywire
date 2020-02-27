// Package visor implements skywire visor.
package visor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/rpc"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appserver"
	"github.com/SkycoinProject/skywire-mainnet/pkg/dmsgpty"
	"github.com/SkycoinProject/skywire-mainnet/pkg/httputil"
	"github.com/SkycoinProject/skywire-mainnet/pkg/restart"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routefinder/rfclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/router"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/pathutil"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/updater"
)

// AppStatus defines running status of an App.
type AppStatus int

const (
	// AppStatusStopped represents status of a stopped App.
	AppStatusStopped AppStatus = iota

	// AppStatusRunning represents status of a running App.
	AppStatusRunning
)

var (
	// ErrUnknownApp represents lookup error for App related calls.
	ErrUnknownApp = errors.New("unknown app")
	// ErrNoConfigPath is returned on attempt to read/write config when visor contains no config path.
	ErrNoConfigPath = errors.New("no config path")
)

const supportedProtocolVersion = "0.1.0"

var reservedPorts = map[routing.Port]string{0: "router", 1: "skychat", 3: "skysocks"}

// AppState defines state parameters for a registered App.
type AppState struct {
	Name      string       `json:"name"`
	AutoStart bool         `json:"autostart"`
	Port      routing.Port `json:"port"`
	Status    AppStatus    `json:"status"`
}

// Visor provides messaging runtime for Apps by setting up all
// necessary connections and performing messaging gateway functions.
type Visor struct {
	conf   *Config
	router router.Router
	n      *snet.Network
	tm     *transport.Manager
	rt     routing.Table
	pty    *dmsgpty.Host // TODO(evanlinjin): Complete.

	Logger *logging.MasterLogger
	logger *logging.Logger

	confPath  *string
	appsPath  string
	localPath string
	appsConf  map[string]AppConfig

	startedAt  time.Time
	restartCtx *restart.Context
	updater    *updater.Updater

	pidMu sync.Mutex

	rpcListener net.Listener
	rpcDialers  []*RPCClientDialer

	procManager  appserver.ProcManager
	appRPCServer *appserver.Server
}

// NewVisor constructs new Visor.
func NewVisor(cfg *Config, logger *logging.MasterLogger, restartCtx *restart.Context, cfgPath *string) (*Visor, error) {
	ctx := context.Background()

	visor := &Visor{
		conf:     cfg,
		confPath: cfgPath,
	}

	visor.Logger = logger
	visor.logger = visor.Logger.PackageLogger("skywire")

	restartCheckDelay, err := time.ParseDuration(cfg.RestartCheckDelay)
	if err == nil {
		restartCtx.SetCheckDelay(restartCheckDelay)
	}

	restartCtx.RegisterLogger(visor.logger)

	visor.restartCtx = restartCtx

	pk := cfg.Visor.StaticPubKey
	sk := cfg.Visor.StaticSecKey

	fmt.Println("min sessions:", cfg.Dmsg.SessionsCount)
	visor.n = snet.New(snet.Config{
		PubKey:          pk,
		SecKey:          sk,
		TpNetworks:      []string{dmsg.Type, snet.STcpType}, // TODO: Have some way to configure this.
		DmsgDiscAddr:    cfg.Dmsg.Discovery,
		DmsgMinSessions: cfg.Dmsg.SessionsCount,
		STCPLocalAddr:   cfg.STCP.LocalAddr,
		STCPTable:       cfg.STCP.PubKeyTable,
	})
	if err := visor.n.Init(ctx); err != nil {
		return nil, fmt.Errorf("failed to init network: %v", err)
	}

	if cfg.DmsgPty != nil {
		pty, err := cfg.DmsgPtyHost(visor.n.Dmsg())
		if err != nil {
			return nil, fmt.Errorf("failed to setup pty: %v", err)
		}
		visor.pty = pty
	}

	logger.Info("'dmsgpty' is not configured, skipping...")

	trDiscovery, err := cfg.TransportDiscovery()
	if err != nil {
		return nil, fmt.Errorf("invalid transport discovery config: %s", err)
	}
	logStore, err := cfg.TransportLogStore()
	if err != nil {
		return nil, fmt.Errorf("invalid TransportLogStore: %s", err)
	}
	tmConfig := &transport.ManagerConfig{
		PubKey:          pk,
		SecKey:          sk,
		DefaultVisors:   cfg.TrustedVisors,
		DiscoveryClient: trDiscovery,
		LogStore:        logStore,
	}
	visor.tm, err = transport.NewManager(visor.n, tmConfig)
	if err != nil {
		return nil, fmt.Errorf("transport manager: %s", err)
	}

	visor.rt, err = cfg.RoutingTable()
	if err != nil {
		return nil, fmt.Errorf("routing table: %s", err)
	}

	rConfig := &router.Config{
		Logger:           visor.Logger.PackageLogger("router"),
		PubKey:           pk,
		SecKey:           sk,
		TransportManager: visor.tm,
		RoutingTable:     visor.rt,
		RouteFinder:      rfclient.NewHTTP(cfg.Routing.RouteFinder, time.Duration(cfg.Routing.RouteFinderTimeout)),
		SetupNodes:       cfg.Routing.SetupNodes,
	}

	r, err := router.New(visor.n, rConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to setup router: %v", err)
	}
	visor.router = r

	visor.appsConf, err = cfg.AppsConfig()
	if err != nil {
		return nil, fmt.Errorf("invalid AppsConfig: %s", err)
	}

	visor.appsPath, err = cfg.AppsDir()
	if err != nil {
		return nil, fmt.Errorf("invalid AppsPath: %s", err)
	}

	visor.localPath, err = cfg.LocalDir()
	if err != nil {
		return nil, fmt.Errorf("invalid LocalPath: %s", err)
	}

	if lvl, err := logging.LevelFromString(cfg.LogLevel); err == nil {
		visor.Logger.SetLevel(lvl)
	}

	if cfg.Interfaces.RPCAddress != "" {
		l, err := net.Listen("tcp", cfg.Interfaces.RPCAddress)
		if err != nil {
			return nil, fmt.Errorf("failed to setup RPC listener: %s", err)
		}
		visor.rpcListener = l
	}

	visor.rpcDialers = make([]*RPCClientDialer, len(cfg.Hypervisors))

	for i, entry := range cfg.Hypervisors {
		_, rpcPort, err := httputil.SplitRPCAddr(entry.Addr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse rpc port from rpc address: %s", err)
		}

		visor.rpcDialers[i] = NewRPCClientDialer(visor.n, entry.PubKey, rpcPort)
	}

	visor.appRPCServer = appserver.New(logging.MustGetLogger("app_rpc_server"), visor.conf.AppServerSockFile)

	go func() {
		if err := visor.appRPCServer.ListenAndServe(); err != nil {
			visor.logger.WithError(err).Error("error serving RPC")
		}
	}()

	visor.procManager = appserver.NewProcManager(logging.MustGetLogger("proc_manager"), visor.appRPCServer)

	visor.updater = updater.New(visor.logger, visor.restartCtx, visor.appsPath)

	return visor, err
}

// Start spawns auto-started Apps, starts router and RPC interfaces .
func (visor *Visor) Start() error {
	skywireNetworker := appnet.NewSkywireNetworker(logging.MustGetLogger("skynet"), visor.router)
	if err := appnet.AddNetworker(appnet.TypeSkynet, skywireNetworker); err != nil {
		return fmt.Errorf("failed to add skywire networker: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	visor.startedAt = time.Now()

	// Start pty.
	if visor.pty != nil {
		go visor.pty.ServeRemoteRequests(ctx)
		go visor.pty.ServeCLIRequests(ctx)
	}

	pathutil.EnsureDir(visor.dir())
	visor.closePreviousApps()

	for _, ac := range visor.appsConf {
		if !ac.AutoStart {
			continue
		}

		go func(a AppConfig) {
			if err := visor.SpawnApp(&a, nil); err != nil {
				visor.logger.Warnf("App %s stopped working: %v", a.App, err)
			}
		}(ac)
	}

	rpcSvr := rpc.NewServer()
	if err := rpcSvr.RegisterName(RPCPrefix, &RPC{visor: visor}); err != nil {
		return fmt.Errorf("rpc server created failed: %s", err)
	}

	if visor.rpcListener != nil {
		visor.logger.Info("Starting RPC interface on ", visor.rpcListener.Addr())

		go rpcSvr.Accept(visor.rpcListener)
	}

	for _, dialer := range visor.rpcDialers {
		go func(dialer *RPCClientDialer) {
			if err := dialer.Run(rpcSvr, time.Second); err != nil {
				visor.logger.Errorf("Hypervisor Dmsg Dial exited with error: %v", err)
			}
		}(dialer)
	}

	visor.logger.Info("Starting packet router")

	if err := visor.router.Serve(ctx); err != nil {
		return fmt.Errorf("failed to start Visor: %s", err)
	}

	return nil
}

func (visor *Visor) dir() string {
	return pathutil.VisorDir(visor.conf.Visor.StaticPubKey)
}

func (visor *Visor) pidFile() *os.File {
	f, err := os.OpenFile(filepath.Join(visor.dir(), "apps-pid.txt"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		panic(err)
	}

	return f
}

func (visor *Visor) closePreviousApps() {
	visor.logger.Info("killing previously ran apps if any...")

	pids := visor.pidFile()
	defer func() {
		if err := pids.Close(); err != nil {
			visor.logger.Warnf("error closing PID file: %s", err)
		}
	}()

	scanner := bufio.NewScanner(pids)
	for scanner.Scan() {
		appInfo := strings.Split(scanner.Text(), " ")
		if len(appInfo) != 2 {
			visor.logger.Fatalf("error parsing %s. Err: %s", pids.Name(), errors.New("line should be: [app name] [pid]"))
		}

		pid, err := strconv.Atoi(appInfo[1])
		if err != nil {
			visor.logger.Fatalf("error parsing %s. Err: %s", pids.Name(), err)
		}

		visor.stopUnhandledApp(appInfo[0], pid)
	}

	// empty file
	pathutil.AtomicWriteFile(pids.Name(), []byte{})
}

func (visor *Visor) stopUnhandledApp(name string, pid int) {
	p, err := os.FindProcess(pid)
	if err != nil {
		if runtime.GOOS != "windows" {
			visor.logger.Infof("Previous app %s ran by this visor with pid: %d not found", name, pid)
		}
		return
	}

	err = p.Signal(syscall.SIGKILL)
	if err != nil {
		return
	}

	visor.logger.Infof("Found and killed hanged app %s with pid %d previously ran by this visor", name, pid)
}

// Close safely stops spawned Apps and Visor.
func (visor *Visor) Close() (err error) {
	if visor == nil {
		return nil
	}

	if visor.rpcListener != nil {
		if err = visor.rpcListener.Close(); err != nil {
			visor.logger.WithError(err).Error("failed to stop RPC interface")
		} else {
			visor.logger.Info("RPC interface stopped successfully")
		}
	}

	for i, dialer := range visor.rpcDialers {
		if err = dialer.Close(); err != nil {
			visor.logger.WithError(err).Errorf("(%d) failed to stop RPC dialer", i)
		} else {
			visor.logger.Infof("(%d) RPC dialer closed successfully", i)
		}
	}

	visor.procManager.StopAll()

	if err = visor.router.Close(); err != nil {
		visor.logger.WithError(err).Error("failed to stop router")
	} else {
		visor.logger.Info("router stopped successfully")
	}

	if err := visor.appRPCServer.Close(); err != nil {
		visor.logger.WithError(err).Error("error closing RPC server")
	}

	if err := UnlinkSocketFiles(visor.conf.AppServerSockFile); err != nil {
		visor.logger.WithError(err).Errorf("Failed to unlink socket file %s", visor.conf.AppServerSockFile)
	} else {
		visor.logger.Infof("Socket file %s removed successfully", visor.conf.AppServerSockFile)
	}

	return err
}

// Apps returns list of AppStates for all registered apps.
func (visor *Visor) Apps() []*AppState {
	// TODO: move app states to the app module
	res := make([]*AppState, 0)

	for _, app := range visor.appsConf {
		state := &AppState{app.App, app.AutoStart, app.Port, AppStatusStopped}

		if visor.procManager.Exists(app.App) {
			state.Status = AppStatusRunning
		}

		res = append(res, state)
	}

	return res
}

// StartApp starts registered App.
func (visor *Visor) StartApp(appName string) error {
	for _, app := range visor.appsConf {
		if app.App == appName {
			startCh := make(chan struct{})

			go func(app AppConfig) {
				if err := visor.SpawnApp(&app, startCh); err != nil {
					visor.logger.Warnf("App %s stopped working: %v", appName, err)
				}
			}(app)

			<-startCh
			return nil
		}
	}

	return ErrUnknownApp
}

// SpawnApp configures and starts new App.
func (visor *Visor) SpawnApp(config *AppConfig, startCh chan<- struct{}) (err error) {
	visor.logger.Infof("Starting %s", config.App)

	if app, ok := reservedPorts[config.Port]; ok && app != config.App {
		return fmt.Errorf("can't bind to reserved port %d", config.Port)
	}

	appCfg := appcommon.Config{
		Name:         config.App,
		SockFilePath: visor.conf.AppServerSockFile,
		VisorPK:      visor.conf.Visor.StaticPubKey.Hex(),
		BinaryDir:    visor.appsPath,
		WorkDir:      filepath.Join(visor.localPath, config.App),
	}

	if _, err := ensureDir(appCfg.WorkDir); err != nil {
		return err
	}

	// TODO: make PackageLogger return *RuleEntry. FieldLogger doesn't expose Writer.
	logger := visor.logger.WithField("_module", config.App).Writer()
	errLogger := visor.logger.WithField("_module", config.App+"[ERROR]").Writer()

	defer func() {
		if logErr := logger.Close(); err == nil && logErr != nil {
			err = logErr
		}

		if logErr := errLogger.Close(); err == nil && logErr != nil {
			err = logErr
		}
	}()

	appLogger := logging.MustGetLogger(fmt.Sprintf("app_%s", config.App))
	appArgs := append([]string{filepath.Join(visor.dir(), config.App)}, config.Args...)

	pid, err := visor.procManager.Start(appLogger, appCfg, appArgs, logger, errLogger)
	if err != nil {
		return fmt.Errorf("error running app %s: %v", config.App, err)
	}

	if startCh != nil {
		startCh <- struct{}{}
	}

	visor.pidMu.Lock()
	visor.logger.Infof("storing app %s pid %d", config.App, pid)
	visor.persistPID(config.App, pid)
	visor.pidMu.Unlock()

	return visor.procManager.Wait(config.App)
}

func (visor *Visor) persistPID(name string, pid appcommon.ProcID) {
	pidF := visor.pidFile()
	pidFName := pidF.Name()
	if err := pidF.Close(); err != nil {
		visor.logger.WithError(err).Warn("Failed to close PID file")
	}

	pathutil.AtomicAppendToFile(pidFName, []byte(fmt.Sprintf("%s %d\n", name, pid)))
}

// StopApp stops running App.
func (visor *Visor) StopApp(appName string) error {
	if !visor.procManager.Exists(appName) {
		return ErrUnknownApp
	}

	visor.logger.Infof("Stopping app %s and closing ports", appName)

	if err := visor.procManager.Stop(appName); err != nil {
		visor.logger.Warn("Failed to stop app: ", err)
		return err
	}

	return nil
}

// RestartApp restarts running App.
func (visor *Visor) RestartApp(name string) error {
	visor.logger.Infof("Restarting app %v", name)

	if err := visor.StopApp(name); err != nil {
		return fmt.Errorf("stop app %v: %w", name, err)
	}

	if err := visor.StartApp(name); err != nil {
		return fmt.Errorf("start app %v: %w", name, err)
	}

	return nil
}

// Exec executes a shell command. It returns combined stdout and stderr output and an error.
func (visor *Visor) Exec(command string) ([]byte, error) {
	args := strings.Split(command, " ")
	cmd := exec.Command(args[0], args[1:]...) // nolint: gosec
	return cmd.CombinedOutput()
}

// Update checks if visor update is available.
// If it is, the method downloads a new visor versions, starts it and kills the current process.
func (visor *Visor) Update() error {
	if err := visor.updater.Update(); err != nil {
		visor.logger.Errorf("Failed to update visor: %v", err)
		return err
	}

	return nil
}

func (visor *Visor) setAutoStart(appName string, autoStart bool) error {
	appConf, ok := visor.appsConf[appName]
	if !ok {
		return ErrUnknownApp
	}

	appConf.AutoStart = autoStart
	visor.appsConf[appName] = appConf

	return visor.updateConfigAppAutoStart(appName, autoStart)
}

func (visor *Visor) updateConfigAppAutoStart(appName string, autoStart bool) error {
	if visor.confPath == nil {
		return nil
	}

	config, err := visor.readConfig()
	if err != nil {
		return err
	}

	visor.logger.Infof("Saving auto start = %v for app %v to config", autoStart, appName)

	changed := false

	for i := range config.Apps {
		if config.Apps[i].App == appName {
			config.Apps[i].AutoStart = autoStart
			changed = true
			break
		}
	}

	if !changed {
		return nil
	}

	return visor.writeConfig(config)
}

func (visor *Visor) setSocksPassword(password string) error {
	visor.logger.Infof("Changing skysocks password to %q", password)

	const (
		socksName       = "skysocks"
		passcodeArgName = "-passcode"
	)

	updateFunc := func(config *Config) {
		visor.updateArg(config, socksName, passcodeArgName, password)
	}

	if err := visor.updateConfig(updateFunc); err != nil {
		return err
	}

	if visor.procManager.Exists(socksName) {
		visor.logger.Infof("Updated %v password, restarting it", socksName)
		return visor.RestartApp(socksName)
	}

	visor.logger.Infof("Updated %v password", socksName)

	return nil
}

func (visor *Visor) setSocksClientPK(pk cipher.PubKey) error {
	visor.logger.Infof("Changing skysocks-client PK to %q", pk)

	const (
		socksClientName = "skysocks-client"
		pkArgName       = "-srv"
	)

	updateFunc := func(config *Config) {
		visor.updateArg(config, socksClientName, pkArgName, pk.String())
	}

	if err := visor.updateConfig(updateFunc); err != nil {
		return err
	}

	if visor.procManager.Exists(socksClientName) {
		visor.logger.Infof("Updated %v PK, restarting it", socksClientName)
		return visor.RestartApp(socksClientName)
	}

	visor.logger.Infof("Updated %v PK", socksClientName)

	return nil
}

func (visor *Visor) updateArg(config *Config, appName, argName, value string) {
	changed := false

	for i := range config.Apps {
		if config.Apps[i].App == appName {
			for j := range config.Apps[i].Args {
				if config.Apps[i].Args[j] == argName && j+1 < len(config.Apps[i].Args) {
					config.Apps[i].Args[j+1] = value
					changed = true
					break
				}
			}

			if !changed {
				config.Apps[i].Args = append(config.Apps[i].Args, argName, value)
			}

			return
		}
	}
}

func (visor *Visor) updateConfig(f func(*Config)) error {
	if visor.confPath == nil {
		return nil
	}

	config, err := visor.readConfig()
	if err != nil {
		return err
	}

	f(config)

	return visor.writeConfig(config)
}

func (visor *Visor) readConfig() (*Config, error) {
	if visor.confPath == nil {
		return nil, ErrNoConfigPath
	}

	configPath := *visor.confPath

	bytes, err := ioutil.ReadFile(filepath.Clean(configPath))
	if err != nil {
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(bytes, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func (visor *Visor) writeConfig(config *Config) error {
	if visor.confPath == nil {
		return ErrNoConfigPath
	}

	configPath := *visor.confPath

	visor.logger.Infof("Updating visor config to %+v", config)

	bytes, err := json.MarshalIndent(config, "", "\t")
	if err != nil {
		return err
	}

	const filePerm = 0644
	return ioutil.WriteFile(configPath, bytes, filePerm)
}

// UnlinkSocketFiles removes unix socketFiles from file system
func UnlinkSocketFiles(socketFiles ...string) error {
	for _, f := range socketFiles {
		if err := syscall.Unlink(f); err != nil {
			if !strings.Contains(err.Error(), "no such file or directory") {
				return err
			}
		}
	}

	return nil
}
