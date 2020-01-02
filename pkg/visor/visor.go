// Package visor implements skywire visor.
package visor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	"github.com/SkycoinProject/dmsg/noise"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	cp "github.com/SkycoinProject/skycoin/src/cipher"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app"
	"github.com/SkycoinProject/skywire-mainnet/pkg/dmsgpty"
	routeFinder "github.com/SkycoinProject/skywire-mainnet/pkg/route-finder/client"
	"github.com/SkycoinProject/skywire-mainnet/pkg/router"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/pathutil"
	apd "github.com/SkycoinProject/skywire-peering-daemon/src/daemon"
	"github.com/rjeczalik/notify"
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

// ErrAppNotRunning occurs when an app is attempted to be stopped when it was not running.
var ErrAppNotRunning = errors.New("app is not running")

// Version is the node version.
const Version = "0.0.1"

const supportedProtocolVersion = "0.0.1"

var reservedPorts = map[routing.Port]string{0: "router", 1: "skychat", 3: "socksproxy"}

// AppState defines state parameters for a registered App.
type AppState struct {
	Name      string       `json:"name"`
	AutoStart bool         `json:"autostart"`
	Port      routing.Port `json:"port"`
	Status    AppStatus    `json:"status"`
}

type appExecuter interface {
	Start(cmd *exec.Cmd) (int, error)
	Stop(pid int) error
	Wait(cmd *exec.Cmd) error
}

type appBind struct {
	conn net.Conn
	pid  int
}

// PacketRouter performs routing of the skywire packets.
type PacketRouter interface {
	io.Closer
	Serve(ctx context.Context) error
	ServeApp(conn net.Conn, port routing.Port, appConf *app.Config) error
	SetupIsTrusted(sPK cipher.PubKey) bool
}

// Node provides messaging runtime for Apps by setting up all
// necessary connections and performing messaging gateway functions.
type Node struct {
	conf   *Config
	router PacketRouter
	n      *snet.Network
	tm     *transport.Manager
	rt     routing.Table
	exec   appExecuter
	pty    *dmsgpty.Host // TODO(evanlinjin): Complete.

	Logger *logging.MasterLogger
	logger *logging.Logger

	appsPath  string
	localPath string
	appsConf  map[string]AppConfig

	startedMu   sync.RWMutex
	startedApps map[string]*appBind
	startedAt   time.Time

	pidMu sync.Mutex
	spdMu sync.Mutex

	rpcListener net.Listener
	rpcDialers  []*noise.RPCClientDialer
}

// NewNode constructs new Node.
func NewNode(config *Config, masterLogger *logging.MasterLogger) (*Node, error) {
	ctx := context.Background()

	node := &Node{
		conf:        config,
		exec:        newOSExecuter(),
		startedApps: make(map[string]*appBind),
	}

	node.Logger = masterLogger
	node.logger = node.Logger.PackageLogger("skywire")

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
		RouteFinder:      routeFinder.NewHTTP(config.Routing.RouteFinder, time.Duration(config.Routing.RouteFinderTimeout)),
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

// RunDaemon starts the auto-peering-daemon as an external process
func (node *Node) RunDaemon() {
	bin, err := exec.LookPath("daemon")
	if err != nil {
		node.logger.Fatalf("Cannot find `skywire-peering-daemon` binary in $PATH: %s", err)
	}

	dir, err := ioutil.TempDir("", "named_pipes")
	if err != nil {
		node.logger.Errorf("Couldn't create named_pipes dir: %s", err)
	}

	namedPipe := filepath.Join(dir, "stdout")
	pubKey := node.conf.Node.StaticPubKey.Hex()
	syscall.Mkfifo(namedPipe, 0600)
	if err := execute(bin, pubKey, namedPipe); err != nil {
		node.logger.Fatal("Failed to start daemon as an external process: ", err)
	}

	node.logger.Info("Opening named pipe for reading packets from skywire-peering-daemon")
	stdOut, err := os.OpenFile(namedPipe, os.O_RDONLY, 0600)
	if err != nil {
		node.logger.Fatal(err)
	}

	c := make(chan notify.EventInfo, 5)
	notify.Watch(namedPipe, c, notify.Write)

	go func() {
		// Read packets from named pipe
		for {
			var (
				packet apd.Packet
				buff   bytes.Buffer
			)
			select {
			case <-c:
				_, err = io.Copy(&buff, stdOut)
				if err != nil {
					node.logger.Fatal(err)
				}

				packet, err = apd.Deserialize(buff.Bytes())
				if err != nil {
					node.logger.Error(err)
				}

				node.spdMu.Lock()

				rPK := cp.MustPubKeyFromHex(packet.PublicKey)
				node.conf.STCP.PubKeyTable[cipher.PubKey(rPK)] = packet.IP
				node.logger.Infof("Packets received from skywire-peering-daemon:\n\t{%s: %s}", packet.PublicKey, packet.IP)

				conn, err := createTransport(node.n, snet.STcpType, packet)
				if err != nil {
					node.logger.Fatalf("Couldn't establish transport to remote visor: %s", err)
				}

				node.logger.Infof("Transport established to remote visor: \n\t{%s: %s}", conn.RemotePK(), conn.RemoteAddr())

				node.spdMu.Unlock()
			}
		}
	}()
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
	node.startedMu.Lock()
	for a, bind := range node.startedApps {
		if err = node.stopApp(a, bind); err != nil {
			node.logger.WithError(err).Errorf("(%s) failed to stop app", a)
		} else {
			node.logger.Infof("(%s) app stopped successfully", a)
		}
	}
	node.startedMu.Unlock()
	if err = node.router.Close(); err != nil {
		node.logger.WithError(err).Error("failed to stop router")
	} else {
		node.logger.Info("router stopped successfully")
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
	res := make([]*AppState, 0)
	for _, app := range node.appsConf {
		state := &AppState{app.App, app.AutoStart, app.Port, AppStatusStopped}
		node.startedMu.RLock()
		if node.startedApps[app.App] != nil {
			state.Status = AppStatusRunning
		}
		node.startedMu.RUnlock()

		res = append(res, state)
	}

	return res
}

// StartApp starts registered App.
func (node *Node) StartApp(appName string) error {
	appConf, ok := node.appsConf[appName]
	if !ok {
		return ErrUnknownApp
	}

	done := make(chan struct{}, 1)
	var err error

	go func(appConf AppConfig) {
		if err = node.SpawnApp(&appConf, done); err != nil {
			done <- struct{}{}
			node.logger.Warnf("Failed to start app %s: %s", appName, err)
		}
	}(appConf)

	<-done
	close(done)

	return err
}

// SpawnApp configures and starts new App.
func (node *Node) SpawnApp(config *AppConfig, startCh chan<- struct{}) (err error) {
	node.logger.Infof("Starting %s.v%s", config.App, config.Version)
	node.logger.Warnf("here: config.Args: %+v, with len %d", config.Args, len(config.Args))
	conn, cmd, err := app.Command(
		&app.Config{ProtocolVersion: supportedProtocolVersion, AppName: config.App, AppVersion: config.Version},
		node.appsPath,
		append([]string{filepath.Join(node.dir(), config.App)}, config.Args...),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize App server: %s", err)
	}

	bind := &appBind{conn, -1}
	if app, ok := reservedPorts[config.Port]; ok && app != config.App {
		return fmt.Errorf("can't bind to reserved port %d", config.Port)
	}

	node.startedMu.Lock()
	if node.startedApps[config.App] != nil {
		node.startedMu.Unlock()
		return fmt.Errorf("app %s is already started", config.App)
	}

	node.startedApps[config.App] = bind
	node.startedMu.Unlock()

	// TODO: make PackageLogger return *Entry. FieldLogger doesn't expose Writer.
	logger := node.logger.WithField("_module", fmt.Sprintf("%s.v%s", config.App, config.Version)).Writer()
	defer func() {
		if logErr := logger.Close(); err == nil && logErr != nil {
			err = logErr
		}
	}()

	cmd.Stdout = logger
	cmd.Stderr = logger
	cmd.Dir = filepath.Join(node.localPath, config.App, fmt.Sprintf("v%s", config.Version))
	if _, err := ensureDir(cmd.Dir); err != nil {
		return err
	}

	appCh := make(chan error)
	go func() {
		pid, err := node.exec.Start(cmd)
		if err != nil {
			appCh <- err
			return
		}

		node.startedMu.Lock()
		bind.pid = pid
		node.startedMu.Unlock()

		node.pidMu.Lock()
		node.logger.Infof("storing app %s pid %d", config.App, pid)
		node.persistPID(config.App, pid)
		node.pidMu.Unlock()
		appCh <- node.exec.Wait(cmd)
	}()

	srvCh := make(chan error)
	go func() {
		srvCh <- node.router.ServeApp(conn, config.Port, &app.Config{AppName: config.App, AppVersion: config.Version})
	}()

	if startCh != nil {
		startCh <- struct{}{}
	}

	var appErr error
	select {
	case err := <-appCh:
		if err != nil {
			if _, ok := err.(*exec.ExitError); !ok {
				appErr = fmt.Errorf("failed to run app executable: %s", err)
			}
		}
	case err := <-srvCh:
		if err != nil {
			appErr = fmt.Errorf("failed to start communication server: %s", err)
		}
	}

	node.startedMu.Lock()
	delete(node.startedApps, config.App)
	node.startedMu.Unlock()

	return appErr
}

func (node *Node) persistPID(name string, pid int) {
	pidF := node.pidFile()
	pidFName := pidF.Name()
	if err := pidF.Close(); err != nil {
		log.WithError(err).Warn("Failed to close PID file")
	}

	pathutil.AtomicAppendToFile(pidFName, []byte(fmt.Sprintf("%s %d\n", name, pid)))
}

// StopApp stops running App.
func (node *Node) StopApp(appName string) error {
	node.startedMu.Lock()
	bind := node.startedApps[appName]
	node.startedMu.Unlock()

	if bind == nil {
		return ErrAppNotRunning
	}

	return node.stopApp(appName, bind)
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

func (node *Node) stopApp(app string, bind *appBind) (err error) {
	node.logger.Infof("Stopping app %s and closing ports", app)

	if excErr := node.exec.Stop(bind.pid); excErr != nil {
		node.logger.Warn("Failed to stop app: ", excErr)
		err = excErr
	}

	if srvErr := bind.conn.Close(); srvErr != nil && err == nil {
		node.logger.Warnf("Failed to close App conn: %s", srvErr)
		err = srvErr
	}

	return err
}

type osExecuter struct {
	processes []*os.Process
	mu        sync.Mutex
}

func newOSExecuter() *osExecuter {
	return &osExecuter{processes: make([]*os.Process, 0)}
}

func (exc *osExecuter) Start(cmd *exec.Cmd) (int, error) {
	if err := cmd.Start(); err != nil {
		return -1, err
	}
	exc.mu.Lock()
	exc.processes = append(exc.processes, cmd.Process)
	exc.mu.Unlock()

	return cmd.Process.Pid, nil
}

func (exc *osExecuter) Stop(pid int) (err error) {
	exc.mu.Lock()
	defer exc.mu.Unlock()

	for _, process := range exc.processes {
		if process.Pid != pid {
			continue
		}

		if sigErr := process.Signal(syscall.SIGKILL); sigErr != nil && err == nil {
			err = sigErr
		}
	}

	return err
}

func (exc *osExecuter) Wait(cmd *exec.Cmd) error {
	return cmd.Wait()
}
