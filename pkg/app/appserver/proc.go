// Package appserver pkg/app/appserver/proc.go c2-vis-appsvc
package appserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/orandin/lumberjackrus"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appdisc"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appnet"
	rpc "github.com/skycoin/skywire/pkg/gobrpc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

var (
	errProcAlreadyRunning = errors.New("process already running")
	errProcNotStarted     = errors.New("process is not started")
)

// Proc is an instance of a skywire app. It encapsulates the running process itself and the RPC server for app/visor
// communication.
type Proc struct {
	// ipcServer is the Windows IPC server (*ipc.Server on native builds). Typed
	// `any` so golang-ipc stays out of the TinyGo graph; see proc_external_*.go.
	ipcServer   any
	ipcServerWg sync.WaitGroup
	disc        appdisc.Updater // app discovery client
	conf        appcommon.ProcConfig
	log         *logging.Logger

	logDB     appcommon.LogStore
	masterLog *logging.MasterLogger // master logger for in-process apps

	// cmd is the external app's OS process (*exec.Cmd on native builds). Typed
	// `any` so os/exec stays out of the TinyGo graph; nil for in-process apps.
	cmd       any
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

	// cmd is non-nil only for external-mode apps on native builds; the os/exec
	// machinery lives in proc_external_native.go (build-tagged out of TinyGo).
	cmd := newExternalCmd(conf, mLog, moduleName)
	var stderr io.ReadCloser

	if conf.EffectiveRunMode() != appcommon.RunModeExternal && (conf.ProcUser != "" || conf.ProcGroup != "") {
		// In-process apps can't drop privileges per-goroutine — the
		// runtime moves them between OS threads, and setuid is
		// process-wide anyway. Surface the misconfiguration so the
		// operator knows the User/Group they set is being ignored.
		mLog.PackageLogger(moduleName).
			Warnf("AppConfig.User/Group ignored for in-process app %q; switch to external launcher mode if you need the privilege drop",
				conf.AppName)
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

		stderr = attachCmdLogs(cmd, appLog, moduleName)
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

	switch p.conf.EffectiveRunMode() {
	case appcommon.RunModeInternal:
		return p.startInProcess()
	default:
		return p.startExternal()
	}
}

func (p *Proc) startInProcess() error {
	p.appCtx, p.appCancelCtx = context.WithCancel(context.Background()) //nolint:gosec

	runFunc := p.conf.RunFunc

	appConn, serverConn := net.Pipe()
	appcommon.RegisterInProcessConn(p.conf.ProcKey, appConn)

	// Acquire lock to serialize env var access for internal apps
	// This prevents race conditions where multiple apps overwrite each other's PROC_CONFIG
	configReadDone := appcommon.AcquireInternalAppStart(p.conf.ProcKey)

	envs := p.conf.Envs()
	for _, env := range envs {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			_ = os.Setenv(parts[0], parts[1]) //nolint:errcheck
		}
	}

	go func() {
		// Internal apps use their isolated logger directly via p.log
		// Do NOT replace global os.Stdout/os.Stderr as it causes race conditions
		// when multiple internal apps run concurrently

		defer func() {
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

			// An in-process app exiting on its own (RunFunc returned or
			// panicked) is the equivalent of an external app's process
			// exiting. Cancel appCtx so the lifecycle goroutine runs its
			// teardown (conn.Close + cm/lm.CloseAll), which closes the app's
			// appnet listeners and frees their porter ports. Without this an
			// app that reserves a port and then errors (e.g. "port already
			// bound") leaks that reservation, so it can never be restarted on
			// the same port. The external path already gets this via cmd.Wait.
			if p.appCancelCtx != nil {
				p.appCancelCtx()
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

	// Wait for app to read config (with timeout to prevent deadlock)
	select {
	case <-configReadDone:
		// App has read its config, safe to release lock
	case <-time.After(30 * time.Second):
		p.log.Warn("Timeout waiting for app to read config, releasing lock anyway")
	}
	appcommon.ReleaseInternalAppStart(p.conf.ProcKey)

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
			select {
			case <-p.readyCh:
				p.disc.Start()
			case <-p.appCtx.Done():
				// Torn down before the app reported Running — don't start
				// discovery for a now-dead app; just exit instead of leaking
				// this goroutine blocked on readyCh forever.
			}
		}()
		defer p.disc.Stop()

		if err := p.startWindowsIPCServer(); err != nil {
			p.appCancelCtx()
			p.waitMx.Unlock()
			p.ipcServerWg.Done()
			return
		}

		<-p.appCtx.Done()

		if err := p.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
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

	// External app: deliver the stop signal (SIGINT / IPC shutdown). No-op for
	// in-process apps and on TinyGo (see proc_external_*.go).
	if err := p.signalStop(); err != nil {
		return err
	}

	p.disc.Stop()

	p.waitMx.Lock()
	defer func() {
		p.closeIPCServer()
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
