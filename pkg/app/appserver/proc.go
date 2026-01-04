// Package appserver pkg/app/appserver/proc.go
package appserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ipc "github.com/james-barrow/golang-ipc"
	"github.com/orandin/lumberjackrus"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appdisc"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

var (
	errProcAlreadyRunning = errors.New("process already running")
	errProcNotStarted     = errors.New("process is not started")

	// stdoutMutex serializes stdout/stderr replacement for in-process apps
	stdoutMutex sync.Mutex
)

// Proc is an instance of a skywire app. It encapsulates the running process itself and the RPC server for app/visor
// communication.
type Proc struct {
	ipcServer   *ipc.Server
	ipcServerWg sync.WaitGroup
	disc        appdisc.Updater // app discovery client
	conf        appcommon.ProcConfig
	log         *logging.Logger

	logDB     appcommon.LogStore
	masterLog *logging.MasterLogger // master logger for in-process apps

	cmd       *exec.Cmd
	isRunning int32
	waitMx    sync.Mutex
	waitErr   error

	appCtx       context.Context
	appCancelCtx context.CancelFunc

	rpcGWMu  sync.Mutex
	rpcGW    *RPCIngressGateway
	conn     net.Conn
	connCh   chan struct{}
	connOnce sync.Once

	m       ProcManager
	appName string

	startTimeMx sync.RWMutex
	startTime   time.Time

	statusMx sync.RWMutex
	status   string

	connDuration   int64
	connDurationMu sync.RWMutex

	errMx sync.RWMutex
	err   string

	portMx sync.RWMutex
	port   routing.Port

	cmdStderr io.ReadCloser

	readyCh   chan struct{}
	readyOnce sync.Once
}

// NewProc constructs `Proc`.
func NewProc(mLog *logging.MasterLogger, conf appcommon.ProcConfig, disc appdisc.Updater, m ProcManager,
	appName, logStorePath string) *Proc {
	if mLog == nil {
		mLog = logging.NewMasterLogger()
	}
	moduleName := fmt.Sprintf("proc:%s:%s", conf.AppName, conf.ProcKey)

	var cmd *exec.Cmd
	var stderr io.ReadCloser

	if conf.RunFunc == nil {
		envs := conf.Envs()
		cmd = exec.Command(conf.BinaryLoc, conf.ProcArgs...) //nolint:gosec
		cmd.Env = append(os.Environ(), envs...)
		cmd.Dir = conf.ProcWorkDir
	}

	var appLogDB appcommon.LogStore
	var appLog *logging.MasterLogger
	procLogger := mLog
	if conf.LogDBLoc != "" {
		appLog, appLogDB = appcommon.NewProcLogger(conf, mLog)
		procLogger = appLog
		if logStorePath != "" {
			storeLog(appLog, logStorePath)
		}

		if cmd != nil {
			cmd.Stdout = appLog.WithField("_module", moduleName).WithField("func", "(STDOUT)").WriterLevel(logrus.DebugLevel)

			errorLog := appLog.WithField("_module", moduleName).WithField("func", "(STDERR)")
			stderr, _ = cmd.StderrPipe() //nolint:errcheck
			printStdErr(stderr, errorLog)
		}
	}

	p := &Proc{
		disc:      disc,
		conf:      conf,
		log:       procLogger.PackageLogger(moduleName),
		logDB:     appLogDB,
		masterLog: appLog,
		cmd:       cmd,
		connCh:    make(chan struct{}, 1),
		m:         m,
		appName:   appName,
		readyCh:   make(chan struct{}, 1),
		cmdStderr: stderr,
	}

	if runtime.GOOS == "windows" {
		p.ipcServerWg.Add(1)
	}
	return p
}

// Logs obtains the log store.
func (p *Proc) Logs() appcommon.LogStore {
	return p.logDB
}

// Cmd returns the internal cmd name.
func (p *Proc) Cmd() *exec.Cmd {
	return p.cmd
}

// StartTime returns app start time.
func (p *Proc) StartTime() (time.Time, bool) {
	if !p.IsRunning() {
		return time.Time{}, false
	}

	p.startTimeMx.RLock()
	defer p.startTimeMx.RUnlock()

	return p.startTime, true
}

// InjectConn introduces the connection to the Proc after it is started.
// Only the first call will return true.
// It also prepares the RPC gateway.
func (p *Proc) InjectConn(conn net.Conn) bool {
	ok := false

	p.connOnce.Do(func() {
		ok = true
		p.conn = conn
		p.rpcGWMu.Lock()
		p.rpcGW = NewRPCGateway(p.log, p)
		p.rpcGWMu.Unlock()

		// Send ready signal.
		p.connCh <- struct{}{}
		close(p.connCh)
	})

	return ok
}

// AwaitConn waits for the connection.
func (p *Proc) AwaitConn() bool {
	<-p.connCh
	rpcS := rpc.NewServer()
	if err := rpcS.RegisterName(p.conf.ProcKey.String(), p.rpcGW); err != nil {
		panic(err)
	}

	go rpcS.ServeConn(p.conn)

	p.log.Debug("Associated and serving proc conn.")
	return true
}

// Start starts the application.
func (p *Proc) Start() error {
	if !atomic.CompareAndSwapInt32(&p.isRunning, 0, 1) {
		return errProcAlreadyRunning
	}

	p.waitMx.Lock()

	p.startTimeMx.Lock()
	p.startTime = time.Now().UTC()
	p.startTimeMx.Unlock()

	if p.conf.RunFunc != nil {
		return p.startInProcess()
	}

	return p.startExternal()
}

func (p *Proc) startInProcess() error {
	p.appCtx, p.appCancelCtx = context.WithCancel(context.Background())

	runFunc, ok := p.conf.RunFunc.(appcommon.AppFunc)
	if !ok {
		p.waitMx.Unlock()
		return fmt.Errorf("invalid RunFunc signature for app %s", p.conf.AppName)
	}

	appConn, serverConn := net.Pipe()
	appcommon.RegisterInProcessConn(p.conf.ProcKey, appConn)

	envs := p.conf.Envs()
	for _, env := range envs {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			_ = os.Setenv(parts[0], parts[1]) //nolint:errcheck
		}
	}

	go func() {
		// Redirect stdout/stderr for this app using pipes
		var origStdout, origStderr *os.File
		var stdoutR, stdoutW, stderrR, stderrW *os.File

		if p.masterLog != nil {
			moduleName := fmt.Sprintf("proc:%s:%s", p.conf.AppName, p.conf.ProcKey)
			stdoutLogger := p.masterLog.WithField("_module", moduleName).WithField("func", "(STDOUT)").WriterLevel(logrus.DebugLevel)
			stderrLogger := p.masterLog.WithField("_module", moduleName).WithField("func", "(STDERR)").WriterLevel(logrus.ErrorLevel)

			// Create pipes for stdout/stderr
			var pipeErr error
			stdoutR, stdoutW, pipeErr = os.Pipe()
			if pipeErr != nil {
				p.log.WithError(pipeErr).Warn("Failed to create stdout pipe")
				return
			}
			stderrR, stderrW, pipeErr = os.Pipe()
			if pipeErr != nil {
				p.log.WithError(pipeErr).Warn("Failed to create stderr pipe")
				_ = stdoutR.Close() //nolint:errcheck
				_ = stdoutW.Close() //nolint:errcheck
				return
			}

			// Start goroutines to copy from pipes to loggers
			go func() { _, _ = io.Copy(stdoutLogger, stdoutR) }() //nolint:errcheck
			go func() { _, _ = io.Copy(stderrLogger, stderrR) }() //nolint:errcheck

			// Replace stdout/stderr
			stdoutMutex.Lock()
			origStdout = os.Stdout
			origStderr = os.Stderr
			os.Stdout = stdoutW
			os.Stderr = stderrW
			stdoutMutex.Unlock()
		}

		defer func() {
			// Restore original stdout/stderr and close pipes
			if origStdout != nil {
				stdoutMutex.Lock()
				os.Stdout = origStdout
				os.Stderr = origStderr
				stdoutMutex.Unlock()

				_ = stdoutW.Close() //nolint:errcheck
				_ = stderrW.Close() //nolint:errcheck
				_ = stdoutR.Close() //nolint:errcheck
				_ = stderrR.Close() //nolint:errcheck
			}

			if r := recover(); r != nil {
				p.errMx.Lock()
				p.err = fmt.Sprintf("app panic: %v", r)
				p.errMx.Unlock()
				p.log.Errorf("App %s panicked: %v", p.conf.AppName, r)
			}

			for _, env := range envs {
				parts := strings.SplitN(env, "=", 2)
				if len(parts) == 2 {
					_ = os.Unsetenv(parts[0]) //nolint:errcheck
				}
			}
		}()

		p.log.Debug("Calling app RunFunc")
		err := runFunc(p.appCtx, p.conf.ProcArgs)
		if err != nil {
			p.errMx.Lock()
			p.err = err.Error()
			p.errMx.Unlock()
			p.log.WithError(err).Error("App RunFunc returned error")
		} else {
			p.log.Debug("App RunFunc returned normally")
		}
	}()

	go func() {
		defer func() {
			appcommon.UnregisterInProcessConn(p.conf.ProcKey)
			_ = p.m.SetError(p.appName, p.err) //nolint:errcheck
			_ = p.m.Stop(p.appName)            //nolint:errcheck
		}()

		pm, ok := p.m.(*procManager)
		if !ok {
			_ = serverConn.Close() //nolint:errcheck
			_ = appConn.Close()    //nolint:errcheck
			p.appCancelCtx()
			p.waitMx.Unlock()
			p.log.Error("Failed to cast ProcManager to procManager.")
			return
		}

		hello, err := appevent.DoRespHandshake(pm.eb, serverConn)
		if err != nil {
			_ = serverConn.Close() //nolint:errcheck
			_ = appConn.Close()    //nolint:errcheck
			p.appCancelCtx()
			p.waitMx.Unlock()
			p.log.WithError(err).Error("Failed to do handshake with in-process app.")
			return
		}

		if hello.ProcKey != p.conf.ProcKey {
			_ = serverConn.Close() //nolint:errcheck
			_ = appConn.Close()    //nolint:errcheck
			p.appCancelCtx()
			p.waitMx.Unlock()
			p.log.Error("In-process app hello ProcKey mismatch.")
			return
		}

		if !p.InjectConn(serverConn) {
			_ = serverConn.Close() //nolint:errcheck
			_ = appConn.Close()    //nolint:errcheck
			p.appCancelCtx()
			p.waitMx.Unlock()
			return
		}

		select {
		case _, ok := <-p.connCh:
			if !ok {
				p.appCancelCtx()
				p.waitMx.Unlock()
				return
			}
		case <-time.After(ProcStartTimeout):
			p.waitErr = fmt.Errorf("app failed to connect within %s", ProcStartTimeout)
			p.waitMx.Unlock()
			p.connOnce.Do(func() { close(p.connCh) })
			return
		}

		if ok := p.AwaitConn(); !ok {
			p.appCancelCtx()
			p.waitMx.Unlock()
			return
		}

		go func() {
			<-p.readyCh
			p.disc.Start()
		}()
		defer p.disc.Stop()

		if runtime.GOOS == "windows" {
			ipcServer, err := ipc.StartServer(p.appName, nil)
			if err != nil {
				p.appCancelCtx()
				p.waitMx.Unlock()
				p.ipcServerWg.Done()
				return
			}
			p.ipcServer = ipcServer
			p.ipcServerWg.Done()
		}

		<-p.appCtx.Done()

		if err := p.conn.Close(); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			p.log.WithError(err).Warn("Closing proc conn returned unexpected error.")
		}
		p.rpcGW.cm.CloseAll()
		p.rpcGW.lm.CloseAll()

		p.waitMx.Unlock()
	}()

	return nil
}

func (p *Proc) startExternal() error {
	if err := p.cmd.Start(); err != nil {
		p.waitMx.Unlock()
		return err
	}

	go func() {
		waitErrCh := make(chan error)
		go func() {
			waitErrCh <- p.cmd.Wait()
			close(waitErrCh)
		}()

		defer func() {
			_ = p.m.SetError(p.appName, p.err) //nolint:errcheck
			_ = p.m.Stop(p.appName)            //nolint:errcheck
		}()

		select {
		case _, ok := <-p.connCh:
			if !ok {
				_ = p.cmd.Process.Kill() //nolint:errcheck
				p.waitMx.Unlock()

				return
			}
		case waitErr := <-waitErrCh:
			p.waitErr = waitErr
			p.waitMx.Unlock()

			p.connOnce.Do(func() { close(p.connCh) })

			return
		}

		if ok := p.AwaitConn(); !ok {
			_ = p.cmd.Process.Kill() //nolint:errcheck
			p.waitMx.Unlock()
			return
		}

		go func() {
			<-p.readyCh
			p.disc.Start()
		}()
		defer p.disc.Stop()

		if runtime.GOOS == "windows" {
			ipcServer, err := ipc.StartServer(p.appName, nil)
			if err != nil {
				_ = p.cmd.Process.Kill() //nolint:errcheck
				p.waitMx.Unlock()
				p.ipcServerWg.Done()
				return
			}
			p.ipcServer = ipcServer
			p.ipcServerWg.Done()
		}

		p.waitErr = <-waitErrCh

		if err := p.conn.Close(); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			p.log.WithError(err).Warn("Closing proc conn returned unexpected error.")
		}
		p.rpcGW.cm.CloseAll()
		p.rpcGW.lm.CloseAll()

		p.waitMx.Unlock()
	}()

	return nil
}

// Stop stops the application.
func (p *Proc) Stop() error {
	if atomic.LoadInt32(&p.isRunning) == 0 {
		return errProcNotStarted
	}

	if p.appCancelCtx != nil {
		p.appCancelCtx()
	}

	if p.cmd != nil && p.cmd.Process != nil {
		if runtime.GOOS != "windows" {
			err := p.cmd.Process.Signal(os.Interrupt)
			if err != nil {
				return err
			}
		} else {
			p.ipcServerWg.Wait()
			if p.ipcServer != nil {
				if err := p.ipcServer.Write(skyenv.IPCShutdownMessageType, []byte("")); err != nil {
					return err
				}
			}
		}
	}

	p.disc.Stop()

	p.waitMx.Lock()
	defer func() {
		if p.ipcServer != nil {
			p.ipcServer.Close()
		}
		if p.cmdStderr != nil {
			_ = p.cmdStderr.Close() //nolint:errcheck
		}
		p.waitMx.Unlock()
		p.connOnce.Do(func() { close(p.connCh) })
	}()

	return nil
}

// Wait waits for the application cmd to exit.
func (p *Proc) Wait() error {
	if atomic.LoadInt32(&p.isRunning) != 1 {
		return errProcNotStarted
	}

	// the lock will be acquired as soon as the cmd finishes its work
	p.waitMx.Lock()
	defer p.waitMx.Unlock()

	return p.waitErr
}

// IsRunning checks whether application cmd is running.
func (p *Proc) IsRunning() bool {
	return atomic.LoadInt32(&p.isRunning) == 1
}

// SetDetailedStatus sets proc's detailed status.
func (p *Proc) SetDetailedStatus(status string) {
	p.statusMx.Lock()
	defer p.statusMx.Unlock()
	if status == AppDetailedStatusRunning {
		p.readyOnce.Do(func() { close(p.readyCh) })
	}

	if status == AppDetailedStatusRunning || status == AppDetailedStatusStopped {
		p.log.Infof("App %v is %v", p.appName, status)
	}

	p.status = status
}

// SetConnectionDuration sets the proc's connection duration
func (p *Proc) SetConnectionDuration(dur int64) {
	p.connDurationMu.Lock()
	defer p.connDurationMu.Unlock()
	p.connDuration = dur
}

// ConnectionDuration gets proc's connection duration
func (p *Proc) ConnectionDuration() int64 {
	p.connDurationMu.RLock()
	defer p.connDurationMu.RUnlock()
	return p.connDuration
}

// DetailedStatus gets proc's detailed status.
func (p *Proc) DetailedStatus() string {
	p.statusMx.RLock()
	defer p.statusMx.RUnlock()

	return p.status
}

// SetError sets proc's detailed status error.
func (p *Proc) SetError(appErr string) {
	p.errMx.Lock()
	defer p.errMx.Unlock()

	p.err = appErr
}

// SetAppPort sets the proc's connection port
func (p *Proc) SetAppPort(port routing.Port) {
	p.portMx.Lock()
	defer p.portMx.Unlock()
	p.port = port
}

// GetAppPort gets the proc's connection port
func (p *Proc) GetAppPort() routing.Port {
	p.portMx.Lock()
	defer p.portMx.Unlock()

	return p.port
}

// Error gets proc's error.
func (p *Proc) Error() string {
	p.errMx.RLock()
	defer p.errMx.RUnlock()

	return p.err
}

// ConnectionSummary sums up the connection stats.
type ConnectionSummary struct {
	IsAlive            bool          `json:"is_alive"`
	Latency            time.Duration `json:"latency"`
	UploadSpeed        uint32        `json:"upload_speed"`
	DownloadSpeed      uint32        `json:"download_speed"`
	BandwidthSent      uint64        `json:"bandwidth_sent"`
	BandwidthReceived  uint64        `json:"bandwidth_received"`
	Error              string        `json:"error"`
	ConnectionDuration int64         `json:"connection_duration,omitempty"`
}

// ConnectionsSummary returns all of the proc's connections stats.
func (p *Proc) ConnectionsSummary() []ConnectionSummary {
	p.rpcGWMu.Lock()
	rpcGW := p.rpcGW
	p.rpcGWMu.Unlock()

	if rpcGW == nil {
		return nil
	}

	var summaries []ConnectionSummary
	rpcGW.cm.DoRange(func(_ uint16, v interface{}) bool {
		if v == nil {
			summaries = append(summaries, ConnectionSummary{})
			return true
		}

		conn, ok := v.(net.Conn)
		if !ok {
			summaries = append(summaries, ConnectionSummary{})
		}

		wrappedConn := conn.(*appnet.WrappedConn)

		skywireConn, isSkywireConn := wrappedConn.Conn.(*appnet.SkywireConn)
		if !isSkywireConn {
			summaries = append(summaries, ConnectionSummary{
				Error: "Can't get such info from this conn",
			})
			return true
		}
		summaries = append(summaries, ConnectionSummary{
			IsAlive: skywireConn.IsAlive(),
			// Latency in summary is expected to be in ms and not ns so we change the base to ms
			Latency:            time.Duration(skywireConn.Latency().Milliseconds()),
			UploadSpeed:        skywireConn.UploadSpeed(),
			DownloadSpeed:      skywireConn.DownloadSpeed(),
			BandwidthSent:      skywireConn.BandwidthSent(),
			BandwidthReceived:  skywireConn.BandwidthReceived(),
			ConnectionDuration: p.ConnectionDuration(),
		})

		return true
	})

	return summaries
}

func storeLog(log *logging.MasterLogger, localPath string) {
	hook, _ := lumberjackrus.NewHook( //nolint:errcheck
		&lumberjackrus.LogFile{
			Filename:   localPath + "/log/skywire.log",
			MaxSize:    1,
			MaxBackups: 1,
			MaxAge:     1,
			Compress:   false,
			LocalTime:  false,
		},
		logrus.TraceLevel,
		&logging.TextFormatter{
			DisableColors:   true,
			FullTimestamp:   true,
			ForceFormatting: true,
		},
		&lumberjackrus.LogFileOpts{
			logrus.InfoLevel: &lumberjackrus.LogFile{
				Filename:   localPath + "/log/skywire.log",
				MaxSize:    1,
				MaxBackups: 1,
				MaxAge:     1,
				Compress:   false,
				LocalTime:  false,
			},
			logrus.WarnLevel: &lumberjackrus.LogFile{
				Filename:   localPath + "/log/skywire.log",
				MaxSize:    1,
				MaxBackups: 1,
				MaxAge:     1,
				Compress:   false,
				LocalTime:  false,
			},
			logrus.TraceLevel: &lumberjackrus.LogFile{
				Filename:   localPath + "/log/skywire.log",
				MaxSize:    1,
				MaxBackups: 1,
				MaxAge:     1,
				Compress:   false,
				LocalTime:  false,
			},
			logrus.ErrorLevel: &lumberjackrus.LogFile{
				Filename:   localPath + "/log/skywire.log",
				MaxSize:    1,
				MaxBackups: 1,
				MaxAge:     1,
				Compress:   false,
				LocalTime:  false,
			},
			logrus.DebugLevel: &lumberjackrus.LogFile{
				Filename:   localPath + "/log/skywire.log",
				MaxSize:    1,
				MaxBackups: 1,
				MaxAge:     1,
				Compress:   false,
				LocalTime:  false,
			},
			logrus.FatalLevel: &lumberjackrus.LogFile{
				Filename:   localPath + "/log/skywire.log",
				MaxSize:    1,
				MaxBackups: 1,
				MaxAge:     1,
				Compress:   false,
				LocalTime:  false,
			},
		},
	)
	log.Hooks.Add(hook)
}
