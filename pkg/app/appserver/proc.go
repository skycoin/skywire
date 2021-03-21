package appserver

import (
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appdisc"
	"github.com/skycoin/skywire/pkg/app/appnet"
)

var (
	errProcAlreadyRunning = errors.New("process already running")
	errProcNotStarted     = errors.New("process is not started")
)

// Proc is an instance of a skywire app. It encapsulates the running process itself and the RPC server for app/visor
// communication.
// TODO(evanlinjin): In the future, we will implement the ability to run multiple instances (procs) of a single app.
type Proc struct {
	disc appdisc.Updater // app discovery client
	conf appcommon.ProcConfig
	log  *logging.Logger

	logDB appcommon.LogStore

	cmd       *exec.Cmd
	isRunning int32
	waitMx    sync.Mutex
	waitErr   error

	rpcGWMu  sync.Mutex
	rpcGW    *RPCIngressGateway // gateway shared over 'conn' - introduced AFTER proc is started
	conn     net.Conn           // connection to proc - introduced AFTER proc is started
	connCh   chan struct{}      // push here when conn is received - protected by 'connOnce'
	connOnce sync.Once          // ensures we only push to 'connCh' once

	m       ProcManager
	appName string

	startTimeMx sync.RWMutex
	startTime   time.Time

	statusMx sync.RWMutex
	status   string
}

// NewProc constructs `Proc`.
func NewProc(mLog *logging.MasterLogger, conf appcommon.ProcConfig, disc appdisc.Updater, m ProcManager,
	appName string) *Proc {
	if mLog == nil {
		mLog = logging.NewMasterLogger()
	}
	moduleName := fmt.Sprintf("proc:%s:%s", conf.AppName, conf.ProcKey)

	cmd := exec.Command(conf.BinaryLoc, conf.ProcArgs...) // nolint:gosec
	cmd.Dir = conf.ProcWorkDir
	cmd.Env = append(os.Environ(), conf.Envs()...)

	appLog, appLogDB := appcommon.NewProcLogger(conf)
	cmd.Stdout = appLog.WithField("_module", moduleName).WithField("func", "(STDOUT)").Writer()
	cmd.Stderr = appLog.WithField("_module", moduleName).WithField("func", "(STDERR)").Writer()

	return &Proc{
		disc:    disc,
		conf:    conf,
		log:     mLog.PackageLogger(moduleName),
		logDB:   appLogDB,
		cmd:     cmd,
		connCh:  make(chan struct{}, 1),
		m:       m,
		appName: appName,
	}
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

func (p *Proc) awaitConn() bool {
	rpcS := rpc.NewServer()
	if err := rpcS.RegisterName(p.conf.ProcKey.String(), p.rpcGW); err != nil {
		panic(err)
	}

	connDelta := p.rpcGW.cm.AddDeltaInformer()
	go func() {
		for n := range connDelta.Chan() {
			if err := p.disc.ChangeValue(appdisc.ConnCountValue, []byte(strconv.Itoa(n))); err != nil {
				p.log.WithError(err).WithField("value", appdisc.ConnCountValue).
					Error("Failed to change app discovery value.")
			}
		}
	}()

	lisDelta := p.rpcGW.lm.AddDeltaInformer()
	go func() {
		for n := range lisDelta.Chan() {
			if err := p.disc.ChangeValue(appdisc.ListenerCountValue, []byte(strconv.Itoa(n))); err != nil {
				p.log.WithError(err).WithField("value", appdisc.ListenerCountValue).
					Error("Failed to change app discovery value.")
			}
		}
	}()

	go rpcS.ServeConn(p.conn)

	p.log.Info("Associated and serving proc conn.")
	return true
}

// Start starts the application.
func (p *Proc) Start() error {
	if !atomic.CompareAndSwapInt32(&p.isRunning, 0, 1) {
		return errProcAlreadyRunning
	}

	// Acquire lock immediately.
	p.waitMx.Lock()

	if err := p.cmd.Start(); err != nil {
		p.waitMx.Unlock()
		return err
	}

	p.startTimeMx.Lock()
	p.startTime = time.Now().UTC()
	p.startTimeMx.Unlock()

	go func() {
		waitErrCh := make(chan error)
		go func() {
			waitErrCh <- p.cmd.Wait()
			close(waitErrCh)
		}()

		defer func() {
			// here will definitely be an error notifying that the process
			// is already stopped. We do this to remove proc from the manager,
			// therefore giving the correct app status to hypervisor.
			_ = p.m.Stop(p.appName) //nolint:errcheck
		}()

		select {
		case _, ok := <-p.connCh:
			if !ok {
				// in this case app got stopped from the outer code before initializing the connection,
				// just kill the process and exit.
				_ = p.cmd.Process.Kill() //nolint:errcheck
				p.waitMx.Unlock()

				return
			}
		case waitErr := <-waitErrCh:
			// in this case app process finished before initializing the connection. Happens if an
			// error occurred during app startup.
			p.waitErr = waitErr
			p.waitMx.Unlock()

			// channel won't get closed outside, close it now.
			p.connOnce.Do(func() { close(p.connCh) })

			return
		}

		// here, the connection is established, so we're not blocked by awaiting it anymore,
		// execution may be continued as usual.

		if ok := p.awaitConn(); !ok {
			_ = p.cmd.Process.Kill() //nolint:errcheck
			p.waitMx.Unlock()
			return
		}

		// App discovery start/stop.
		p.disc.Start()
		defer p.disc.Stop()

		// Wait for proc to exit.
		p.waitErr = <-waitErrCh

		// Close proc conn and associated listeners and connections.
		if err := p.conn.Close(); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			p.log.WithError(err).Warn("Closing proc conn returned unexpected error.")
		}
		p.rpcGW.cm.CloseAll()
		p.rpcGW.lm.CloseAll()

		// Unlock.
		p.waitMx.Unlock()
	}()

	return nil
}

// Stop stops the application.
func (p *Proc) Stop() error {
	if atomic.LoadInt32(&p.isRunning) == 0 {
		return errProcNotStarted
	}

	if p.cmd.Process != nil {
		err := p.cmd.Process.Signal(os.Interrupt)
		if err != nil {
			return err
		}
	}

	// the lock will be acquired as soon as the cmd finishes its work
	p.waitMx.Lock()
	defer func() {
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

	p.status = status
}

// DetailedStatus gets proc's detailed status.
func (p *Proc) DetailedStatus() string {
	p.statusMx.RLock()
	defer p.statusMx.RUnlock()

	return p.status
}

// ConnectionSummary sums up the connection stats.
type ConnectionSummary struct {
	IsAlive           bool          `json:"is_alive"`
	Latency           time.Duration `json:"latency"`
	UploadSpeed       uint32        `json:"upload_speed"`
	DownloadSpeed     uint32        `json:"download_speed"`
	BandwidthSent     uint64        `json:"bandwidth_sent"`
	BandwidthReceived uint64        `json:"bandwidth_received"`
	Error             string        `json:"error"`
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
	rpcGW.cm.DoRange(func(id uint16, v interface{}) bool {
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
			IsAlive:           skywireConn.IsAlive(),
			Latency:           skywireConn.Latency(),
			UploadSpeed:       skywireConn.UploadSpeed(),
			DownloadSpeed:     skywireConn.DownloadSpeed(),
			BandwidthSent:     skywireConn.BandwidthSent(),
			BandwidthReceived: skywireConn.BandwidthReceived(),
		})

		return true
	})

	return summaries
}
