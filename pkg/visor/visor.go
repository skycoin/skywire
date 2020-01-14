// Package visor implements skywire visor.
package visor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
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
	"github.com/SkycoinProject/dmsg/noise"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appserver"
	"github.com/SkycoinProject/skywire-mainnet/pkg/dmsgpty"
	"github.com/SkycoinProject/skywire-mainnet/pkg/restart"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routefinder/rfclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/router"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/pathutil"
)

var log = logging.MustGetLogger("node")

// AppStatus defines running status of an App.
type AppStatus int

const (
	// AppStatusStopped represents status of a stopped App.
	AppStatusStopped AppStatus = iota

	// AppStatusRunning represents status of a running App.
	AppStatusRunning
)

// ErrUnknownApp represents lookup error for App related calls.
var ErrUnknownApp = errors.New("unknown app")

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

	appsPath  string
	localPath string
	appsConf  map[string]AppConfig

	startedAt  time.Time
	restartCtx *restart.Context

	pidMu sync.Mutex

	rpcListener net.Listener
	rpcDialers  []*noise.RPCClientDialer

	procManager appserver.ProcManager
}

// NewNode constructs new Node.
func NewNode(config *Config, masterLogger *logging.MasterLogger, restartCtx *restart.Context) (*Node, error) {
	ctx := context.Background()

	node := &Node{
		conf:        config,
		procManager: appserver.NewProcManager(logging.MustGetLogger("proc_manager")),
	}

	node.Logger = masterLogger
	node.logger = node.Logger.PackageLogger("skywire")

	restartCheckDelay, err := time.ParseDuration(config.RestartCheckDelay)
	if err == nil {
		restartCtx.SetCheckDelay(restartCheckDelay)
	}

	restartCtx.RegisterLogger(node.logger)

	node.restartCtx = restartCtx

	pk := config.Node.StaticPubKey
	sk := config.Node.StaticSecKey

	fmt.Println("min servers:", config.Messaging.ServerCount)
	node.n = snet.New(snet.Config{
		PubKey:        pk,
		SecKey:        sk,
		TpNetworks:    []string{dmsg.Type, snet.STcpType}, // TODO: Have some way to configure this.
		DmsgDiscAddr:  config.Messaging.Discovery,
		DmsgMinSrvs:   config.Messaging.ServerCount,
		STCPLocalAddr: config.STCP.LocalAddr,
		STCPTable:     config.STCP.PubKeyTable,
	})
	if err := node.n.Init(ctx); err != nil {
		return nil, fmt.Errorf("failed to init network: %v", err)
	}

	if config.DmsgPty != nil {
		pty, err := config.DmsgPtyHost(node.n.Dmsg())
		if err != nil {
			return nil, fmt.Errorf("failed to setup pty: %v", err)
		}
		node.pty = pty
	}
	masterLogger.Info("'dmsgpty' is not configured, skipping...")

	trDiscovery, err := config.TransportDiscovery()
	if err != nil {
		return nil, fmt.Errorf("invalid MessagingConfig: %s", err)
	}
	logStore, err := config.TransportLogStore()
	if err != nil {
		return nil, fmt.Errorf("invalid TransportLogStore: %s", err)
	}
	tmConfig := &transport.ManagerConfig{
		PubKey:          pk,
		SecKey:          sk,
		DefaultNodes:    config.TrustedNodes,
		DiscoveryClient: trDiscovery,
		LogStore:        logStore,
	}
	node.tm, err = transport.NewManager(node.n, tmConfig)
	if err != nil {
		return nil, fmt.Errorf("transport manager: %s", err)
	}

	node.rt, err = config.RoutingTable()
	if err != nil {
		return nil, fmt.Errorf("routing table: %s", err)
	}
	rConfig := &router.Config{
		Logger:           node.Logger.PackageLogger("router"),
		PubKey:           pk,
		SecKey:           sk,
		TransportManager: node.tm,
		RoutingTable:     node.rt,
		RouteFinder:      rfclient.NewHTTP(config.Routing.RouteFinder, time.Duration(config.Routing.RouteFinderTimeout)),
		SetupNodes:       config.Routing.SetupNodes,
	}
	r, err := router.New(node.n, rConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to setup router: %v", err)
	}
	node.router = r

	node.appsConf, err = config.AppsConfig()
	if err != nil {
		return nil, fmt.Errorf("invalid AppsConfig: %s", err)
	}

	node.appsPath, err = config.AppsDir()
	if err != nil {
		return nil, fmt.Errorf("invalid AppsPath: %s", err)
	}

	node.localPath, err = config.LocalDir()
	if err != nil {
		return nil, fmt.Errorf("invalid LocalPath: %s", err)
	}

	if lvl, err := logging.LevelFromString(config.LogLevel); err == nil {
		node.Logger.SetLevel(lvl)
	}

	if config.Interfaces.RPCAddress != "" {
		l, err := net.Listen("tcp", config.Interfaces.RPCAddress)
		if err != nil {
			return nil, fmt.Errorf("failed to setup RPC listener: %s", err)
		}
		node.rpcListener = l
	}
	node.rpcDialers = make([]*noise.RPCClientDialer, len(config.Hypervisors))
	for i, entry := range config.Hypervisors {
		node.rpcDialers[i] = noise.NewRPCClientDialer(entry.Addr, noise.HandshakeXK, noise.Config{
			LocalPK:   pk,
			LocalSK:   sk,
			RemotePK:  entry.PubKey,
			Initiator: true,
		})
	}

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
				node.logger.Warnf("Failed to start %s: %s\n", a.App, err)
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
		go func(dialer *noise.RPCClientDialer) {
			if err := dialer.Run(rpcSvr, time.Second); err != nil {
				node.logger.Errorf("Dialer exited with error: %v", err)
			}
		}(dialer)
	}

	node.logger.Info("Starting packet router")
	if err := node.router.Serve(ctx); err != nil {
		return fmt.Errorf("failed to start Node: %s", err)
	}

	return nil
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

	if err := UnlinkSocketFiles(node.conf.AppServerSockFile); err != nil {
		node.logger.WithError(err).Errorf("Failed to unlink socket file %s", node.conf.AppServerSockFile)
	} else {
		node.logger.Infof("Socket file %s removed successfully", node.conf.AppServerSockFile)
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
					node.logger.Warnf("Failed to start app %s: %s", appName, err)
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
	node.logger.Warnf("here: config.Args: %+v, with len %d", config.Args, len(config.Args))

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
	defer func() {
		if logErr := logger.Close(); err == nil && logErr != nil {
			err = logErr
		}
	}()

	pid, err := node.procManager.Run(logging.MustGetLogger(fmt.Sprintf("app_%s", config.App)),
		appCfg, append([]string{filepath.Join(node.dir(), config.App)}, config.Args...), logger, logger)
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

	if err := node.procManager.Wait(config.App); err != nil {
		return err
	}

	return nil
}

func (node *Node) persistPID(name string, pid appcommon.ProcID) {
	pidF := node.pidFile()
	pidFName := pidF.Name()
	if err := pidF.Close(); err != nil {
		log.WithError(err).Warn("Failed to close PID file")
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

// SetAutoStart sets an app to auto start or not.
func (node *Node) SetAutoStart(appName string, autoStart bool) error {
	appConf, ok := node.appsConf[appName]
	if !ok {
		return ErrUnknownApp
	}

	appConf.AutoStart = autoStart
	node.appsConf[appName] = appConf
	return nil
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
