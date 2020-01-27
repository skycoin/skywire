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
	"github.com/rjeczalik/notify"

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
	// ErrNoConfigPath is returned on attempt to read/write config when node contains no config path.
	ErrNoConfigPath = errors.New("no config path")
)

// Version is the node version.
const Version = "0.0.1"

const supportedProtocolVersion = "0.0.1"

var reservedPorts = map[routing.Port]string{0: "router", 1: "skychat", 3: "skysocks"}

// AppState defines state parameters for a registered App.
type AppState struct {
	Name      string       `json:"name"`
	AutoStart bool         `json:"autostart"`
	Port      routing.Port `json:"port"`
	Status    AppStatus    `json:"status"`
}

// Node provides messaging runtime for Apps by setting up all
// necessary connections and performing messaging gateway functions.
type Node struct {
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

	pidMu sync.Mutex

	rpcListener net.Listener
	rpcDialers  []*RPCClientDialer

	procManager  appserver.ProcManager
	appRPCServer *appserver.Server

	Onspd  bool
	spdCmd *exec.Cmd
}

// NewNode constructs new Node.
func NewNode(cfg *Config, logger *logging.MasterLogger, restartCtx *restart.Context, cfgPath *string) (*Node, error) {
	ctx := context.Background()

	node := &Node{
		conf:     cfg,
		confPath: cfgPath,
	}

	node.Logger = logger
	node.logger = node.Logger.PackageLogger("skywire")

	restartCheckDelay, err := time.ParseDuration(cfg.RestartCheckDelay)
	if err == nil {
		restartCtx.SetCheckDelay(restartCheckDelay)
	}

	restartCtx.RegisterLogger(node.logger)

	node.restartCtx = restartCtx

	pk := cfg.Node.StaticPubKey
	sk := cfg.Node.StaticSecKey

	fmt.Println("min sessions:", cfg.Dmsg.SessionsCount)
	node.n = snet.New(snet.Config{
		PubKey:          pk,
		SecKey:          sk,
		TpNetworks:      []string{dmsg.Type, snet.STcpType}, // TODO: Have some way to configure this.
		DmsgDiscAddr:    cfg.Dmsg.Discovery,
		DmsgMinSessions: cfg.Dmsg.SessionsCount,
		STCPLocalAddr:   cfg.STCP.LocalAddr,
		STCPTable:       cfg.STCP.PubKeyTable,
	})
	if err := node.n.Init(ctx); err != nil {
		return nil, fmt.Errorf("failed to init network: %v", err)
	}

	if cfg.DmsgPty != nil {
		pty, err := cfg.DmsgPtyHost(node.n.Dmsg())
		if err != nil {
			return nil, fmt.Errorf("failed to setup pty: %v", err)
		}
		node.pty = pty
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
		DefaultNodes:    cfg.TrustedNodes,
		DiscoveryClient: trDiscovery,
		LogStore:        logStore,
	}
	node.tm, err = transport.NewManager(node.n, tmConfig)
	if err != nil {
		return nil, fmt.Errorf("transport manager: %s", err)
	}

	node.rt, err = cfg.RoutingTable()
	if err != nil {
		return nil, fmt.Errorf("routing table: %s", err)
	}

	rConfig := &router.Config{
		Logger:           node.Logger.PackageLogger("router"),
		PubKey:           pk,
		SecKey:           sk,
		TransportManager: node.tm,
		RoutingTable:     node.rt,
		RouteFinder:      rfclient.NewHTTP(cfg.Routing.RouteFinder, time.Duration(cfg.Routing.RouteFinderTimeout)),
		SetupNodes:       cfg.Routing.SetupNodes,
	}

	r, err := router.New(node.n, rConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to setup router: %v", err)
	}
	node.router = r

	node.appsConf, err = cfg.AppsConfig()
	if err != nil {
		return nil, fmt.Errorf("invalid AppsConfig: %s", err)
	}

	node.appsPath, err = cfg.AppsDir()
	if err != nil {
		return nil, fmt.Errorf("invalid AppsPath: %s", err)
	}

	node.localPath, err = cfg.LocalDir()
	if err != nil {
		return nil, fmt.Errorf("invalid LocalPath: %s", err)
	}

	if lvl, err := logging.LevelFromString(cfg.LogLevel); err == nil {
		node.Logger.SetLevel(lvl)
	}

	if cfg.Interfaces.RPCAddress != "" {
		l, err := net.Listen("tcp", cfg.Interfaces.RPCAddress)
		if err != nil {
			return nil, fmt.Errorf("failed to setup RPC listener: %s", err)
		}
		node.rpcListener = l
	}

	node.rpcDialers = make([]*RPCClientDialer, len(cfg.Hypervisors))

	for i, entry := range cfg.Hypervisors {
		_, rpcPort, err := httputil.SplitRPCAddr(entry.Addr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse rpc port from rpc address: %s", err)
		}

		node.rpcDialers[i] = NewRPCClientDialer(node.n, entry.PubKey, rpcPort)
	}

	node.appRPCServer = appserver.New(logging.MustGetLogger("app_rpc_server"), node.conf.AppServerSockFile)

	go func() {
		if err := node.appRPCServer.ListenAndServe(); err != nil {
			node.logger.WithError(err).Error("error serving RPC")
		}
	}()

	node.procManager = appserver.NewProcManager(logging.MustGetLogger("proc_manager"), node.appRPCServer)

	return node, err
}

// Start spawns auto-started Apps, starts router and RPC interfaces .
func (node *Node) Start() error {
	skywireNetworker := appnet.NewSkywireNetworker(logging.MustGetLogger("skynet"), node.router)
	if err := appnet.AddNetworker(appnet.TypeSkynet, skywireNetworker); err != nil {
		return fmt.Errorf("failed to add skywire networker: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	node.startedAt = time.Now()

	// Start pty.
	if node.pty != nil {
		go node.pty.ServeRemoteRequests(ctx)
		go node.pty.ServeCLIRequests(ctx)
	}

	pathutil.EnsureDir(node.dir())
	node.closePreviousApps()

	for _, ac := range node.appsConf {
		if !ac.AutoStart {
			continue
		}

		go func(a AppConfig) {
			if err := node.SpawnApp(&a, nil); err != nil {
				node.logger.Warnf("App %s stopped working: %v", a.App, err)
			}
		}(ac)
	}

	rpcSvr := rpc.NewServer()
	if err := rpcSvr.RegisterName(RPCPrefix, &RPC{node: node}); err != nil {
		return fmt.Errorf("rpc server created failed: %s", err)
	}

	if node.rpcListener != nil {
		node.logger.Info("Starting RPC interface on ", node.rpcListener.Addr())

		go rpcSvr.Accept(node.rpcListener)
	}

	for _, dialer := range node.rpcDialers {
		go func(dialer *RPCClientDialer) {
			if err := dialer.Run(rpcSvr, time.Second); err != nil {
				node.logger.Errorf("Hypervisor Dmsg Dial exited with error: %v", err)
			}
		}(dialer)
	}

	node.logger.Info("Starting packet router")

	if err := node.router.Serve(ctx); err != nil {
		return fmt.Errorf("failed to start Node: %s", err)
	}

	return nil
}

// RunDaemon starts a skywire-peering-daemon as an external process
func (node *Node) RunDaemon() error {
	node.Onspd = true
	bin, err := exec.LookPath("daemon")
	if err != nil {
		return fmt.Errorf("Cannot find `skywire-peering-daemon` binary in $PATH: %s", err)
	}

	dir, err := ioutil.TempDir("", "named_pipes")
	if err != nil {
		return fmt.Errorf("Couldn't create named_pipes dir: %s", err)
	}

	namedPipe := filepath.Join(dir, "stdout")
	lAddr := node.conf.STCP.LocalAddr
	pubKey := node.conf.Node.StaticPubKey.Hex()
	err = syscall.Mkfifo(namedPipe, 0600)
	if err != nil {
		return err
	}

	node.spdCmd = exec.Command(bin, pubKey, lAddr, namedPipe)
	if err := execute(node.spdCmd); err != nil {
		return fmt.Errorf("Failed to start daemon as an external process: %s", err)
	}

	node.logger.Info("Opening named pipe for reading packets from skywire-peering-daemon")
	stdOut, err := os.OpenFile(namedPipe, os.O_RDONLY, 0600)
	if err != nil {
		return err
	}

	if node.conf.STCP.PubKeyTable == nil {
		node.conf.STCP.PubKeyTable = make(map[cipher.PubKey]string)
	}
	pubKeyTable := node.conf.STCP.PubKeyTable
	c := make(chan notify.EventInfo, 5)

	err = watchNamedPipe(namedPipe, c)
	if err != nil {
		return err
	}

	readSPDPacket(stdOut, c, pubKeyTable)

	return nil
}

// StopDaemon kills the the skywire-peering-daemon started as an external process
// and all child processes.
func (node *Node) StopDaemon() {
	node.Onspd = false
	node.logger.Info("Shutting down skywire-peering-daemon")
	if err := node.spdCmd.Process.Kill(); err != nil {
		node.logger.Errorf("Failed to kill skywire-peering-daemon process: %s", err)
	}

	node.logger.Info("Skywire-peering-daemon closed successfully")
}

func (node *Node) dir() string {
	return pathutil.NodeDir(node.conf.Node.StaticPubKey)
}

func (node *Node) pidFile() *os.File {
	f, err := os.OpenFile(filepath.Join(node.dir(), "apps-pid.txt"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		panic(err)
	}

	return f
}

func (node *Node) closePreviousApps() {
	node.logger.Info("killing previously ran apps if any...")

	pids := node.pidFile()
	defer func() {
		if err := pids.Close(); err != nil {
			node.logger.Warnf("error closing PID file: %s", err)
		}
	}()

	scanner := bufio.NewScanner(pids)
	for scanner.Scan() {
		appInfo := strings.Split(scanner.Text(), " ")
		if len(appInfo) != 2 {
			node.logger.Fatalf("error parsing %s. Err: %s", pids.Name(), errors.New("line should be: [app name] [pid]"))
		}

		pid, err := strconv.Atoi(appInfo[1])
		if err != nil {
			node.logger.Fatalf("error parsing %s. Err: %s", pids.Name(), err)
		}

		node.stopUnhandledApp(appInfo[0], pid)
	}

	// empty file
	pathutil.AtomicWriteFile(pids.Name(), []byte{})
}

func (node *Node) stopUnhandledApp(name string, pid int) {
	p, err := os.FindProcess(pid)
	if err != nil {
		if runtime.GOOS != "windows" {
			node.logger.Infof("Previous app %s ran by this node with pid: %d not found", name, pid)
		}
		return
	}

	err = p.Signal(syscall.SIGKILL)
	if err != nil {
		return
	}

	node.logger.Infof("Found and killed hanged app %s with pid %d previously ran by this node", name, pid)
}

// Close safely stops spawned Apps and messaging Node.
func (node *Node) Close() (err error) {
	if node == nil {
		return nil
	}

	if node.rpcListener != nil {
		if err = node.rpcListener.Close(); err != nil {
			node.logger.WithError(err).Error("failed to stop RPC interface")
		} else {
			node.logger.Info("RPC interface stopped successfully")
		}
	}

	for i, dialer := range node.rpcDialers {
		if err = dialer.Close(); err != nil {
			node.logger.WithError(err).Errorf("(%d) failed to stop RPC dialer", i)
		} else {
			node.logger.Infof("(%d) RPC dialer closed successfully", i)
		}
	}

	node.procManager.StopAll()

	if err = node.router.Close(); err != nil {
		node.logger.WithError(err).Error("failed to stop router")
	} else {
		node.logger.Info("router stopped successfully")
	}

	if err := node.appRPCServer.Close(); err != nil {
		node.logger.WithError(err).Error("error closing RPC server")
	}

	if err := UnlinkSocketFiles(node.conf.AppServerSockFile); err != nil {
		node.logger.WithError(err).Errorf("Failed to unlink socket file %s", node.conf.AppServerSockFile)
	} else {
		node.logger.Infof("Socket file %s removed successfully", node.conf.AppServerSockFile)
	}

	if node.Onspd {
		node.StopDaemon()
	}

	return err
}

// Exec executes a shell command. It returns combined stdout and stderr output and an error.
func (node *Node) Exec(command string) ([]byte, error) {
	args := strings.Split(command, " ")
	cmd := exec.Command(args[0], args[1:]...) // nolint: gosec
	return cmd.CombinedOutput()
}

// Apps returns list of AppStates for all registered apps.
func (node *Node) Apps() []*AppState {
	// TODO: move app states to the app module
	res := make([]*AppState, 0)

	for _, app := range node.appsConf {
		state := &AppState{app.App, app.AutoStart, app.Port, AppStatusStopped}

		if node.procManager.Exists(app.App) {
			state.Status = AppStatusRunning
		}

		res = append(res, state)
	}

	return res
}

// StartApp starts registered App.
func (node *Node) StartApp(appName string) error {
	for _, app := range node.appsConf {
		if app.App == appName {
			startCh := make(chan struct{})

			go func(app AppConfig) {
				if err := node.SpawnApp(&app, startCh); err != nil {
					node.logger.Warnf("App %s stopped working: %v", appName, err)
				}
			}(app)

			<-startCh
			return nil
		}
	}

	return ErrUnknownApp
}

// SpawnApp configures and starts new App.
func (node *Node) SpawnApp(config *AppConfig, startCh chan<- struct{}) (err error) {
	node.logger.Infof("Starting %s.v%s", config.App, config.Version)

	if app, ok := reservedPorts[config.Port]; ok && app != config.App {
		return fmt.Errorf("can't bind to reserved port %d", config.Port)
	}

	appCfg := appcommon.Config{
		Name:         config.App,
		Version:      config.Version,
		SockFilePath: node.conf.AppServerSockFile,
		VisorPK:      node.conf.Node.StaticPubKey.Hex(),
		BinaryDir:    node.appsPath,
		WorkDir:      filepath.Join(node.localPath, config.App, fmt.Sprintf("v%s", config.Version)),
	}

	if _, err := ensureDir(appCfg.WorkDir); err != nil {
		return err
	}

	// TODO: make PackageLogger return *RuleEntry. FieldLogger doesn't expose Writer.
	logger := node.logger.WithField("_module", fmt.Sprintf("%s.v%s", config.App, config.Version)).Writer()
	errLogger := node.logger.WithField("_module", fmt.Sprintf("%s.v%s[ERROR]", config.App, config.Version)).Writer()

	defer func() {
		if logErr := logger.Close(); err == nil && logErr != nil {
			err = logErr
		}

		if logErr := errLogger.Close(); err == nil && logErr != nil {
			err = logErr
		}
	}()

	appLogger := logging.MustGetLogger(fmt.Sprintf("app_%s", config.App))
	appArgs := append([]string{filepath.Join(node.dir(), config.App)}, config.Args...)

	pid, err := node.procManager.Start(appLogger, appCfg, appArgs, logger, errLogger)
	if err != nil {
		return fmt.Errorf("error running app %s: %v", config.App, err)
	}

	if startCh != nil {
		startCh <- struct{}{}
	}

	node.pidMu.Lock()
	node.logger.Infof("storing app %s pid %d", config.App, pid)
	node.persistPID(config.App, pid)
	node.pidMu.Unlock()

	return node.procManager.Wait(config.App)
}

func (node *Node) persistPID(name string, pid appcommon.ProcID) {
	pidF := node.pidFile()
	pidFName := pidF.Name()
	if err := pidF.Close(); err != nil {
		node.logger.WithError(err).Warn("Failed to close PID file")
	}

	pathutil.AtomicAppendToFile(pidFName, []byte(fmt.Sprintf("%s %d\n", name, pid)))
}

// StopApp stops running App.
func (node *Node) StopApp(appName string) error {
	node.logger.Infof("Stopping app %s and closing ports", appName)

	if !node.procManager.Exists(appName) {
		return ErrUnknownApp
	}

	if err := node.procManager.Stop(appName); err != nil {
		node.logger.Warn("Failed to stop app: ", err)
		return err
	}

	return nil
}

// RestartApp restarts running App.
func (node *Node) RestartApp(name string) error {
	node.logger.Infof("Restarting app %v", name)

	if err := node.StopApp(name); err != nil {
		return fmt.Errorf("stop app %v: %w", name, err)
	}

	if err := node.StartApp(name); err != nil {
		return fmt.Errorf("start app %v: %w", name, err)
	}

	return nil
}

func (node *Node) setAutoStart(appName string, autoStart bool) error {
	appConf, ok := node.appsConf[appName]
	if !ok {
		return ErrUnknownApp
	}

	appConf.AutoStart = autoStart
	node.appsConf[appName] = appConf

	return node.updateConfigAppAutoStart(appName, autoStart)
}

func (node *Node) updateConfigAppAutoStart(appName string, autoStart bool) error {
	if node.confPath == nil {
		return nil
	}

	config, err := node.readConfig()
	if err != nil {
		return err
	}

	node.logger.Infof("Saving auto start = %v for app %v to config", autoStart, appName)

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

	return node.writeConfig(config)
}

func (node *Node) setSocksPassword(password string) error {
	node.logger.Infof("Changing skysocks password to %q", password)

	const (
		socksName       = "skysocks"
		passcodeArgName = "-passcode"
	)

	updateFunc := func(config *Config) {
		node.updateArg(config, socksName, passcodeArgName, password)
	}

	if err := node.updateConfig(updateFunc); err != nil {
		return err
	}

	node.logger.Infof("Updated %v password, restarting it", socksName)

	return node.RestartApp(socksName)
}

func (node *Node) setSocksClientPK(pk cipher.PubKey) error {
	node.logger.Infof("Changing skysocks-client PK to %q", pk)

	const (
		socksClientName = "skysocks-client"
		pkArgName       = "-srv"
	)

	updateFunc := func(config *Config) {
		node.updateArg(config, socksClientName, pkArgName, pk.String())
	}

	if err := node.updateConfig(updateFunc); err != nil {
		return err
	}

	node.logger.Infof("Updated %v PK, restarting it", socksClientName)

	return node.RestartApp(socksClientName)
}

func (node *Node) updateArg(config *Config, appName, argName, value string) {
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

func (node *Node) updateConfig(f func(*Config)) error {
	if node.confPath == nil {
		return nil
	}

	config, err := node.readConfig()
	if err != nil {
		return err
	}

	f(config)

	return node.writeConfig(config)
}

func (node *Node) readConfig() (*Config, error) {
	if node.confPath == nil {
		return nil, ErrNoConfigPath
	}

	configPath := *node.confPath

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

func (node *Node) writeConfig(config *Config) error {
	if node.confPath == nil {
		return ErrNoConfigPath
	}

	configPath := *node.confPath

	node.logger.Infof("Updating visor config to %+v", config)

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
			if strings.Contains(err.Error(), "no such file or directory") {
				continue
			} else {
				return err
			}
		}
	}

	return nil
}
