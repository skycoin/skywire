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

	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appdisc"
)

var (
	errProcAlreadyRunning = errors.New("process already running")
	errProcNotStarted     = errors.New("process is not started")
)

// Proc is a wrapper for a skywire app. Encapsulates
// the running process itself and the RPC server for
// app/visor communication.
type Proc struct {
	disc appdisc.Updater // app discovery client
	conf appcommon.ProcConfig
	log  *logging.Logger

	logDB appcommon.LogStore

	cmd       *exec.Cmd
	isRunning int32
	waitMx    sync.Mutex
	waitErr   error

	rpcGW    *RPCGateway   // gateway shared over 'conn' - introduced AFTER proc is started
	conn     net.Conn      // connection to proc - introduced AFTER proc is started
	connCh   chan struct{} // push here when conn is received - protected by 'connOnce'
	connOnce sync.Once     // ensures we only push to 'connCh' once
}

// NewProc constructs `Proc`.
func NewProc(conf appcommon.ProcConfig, disc appdisc.Updater) *Proc {
	moduleName := fmt.Sprintf("proc:%s:%s", conf.AppName, conf.ProcKey)

	cmd := exec.Command(conf.BinaryLoc, conf.ProcArgs...) // nolint:gosec
	cmd.Dir = conf.ProcWorkDir
	cmd.Env = append(os.Environ(), conf.Envs()...)

	appLog, appLogDB := appcommon.NewProcLogger(conf)
	cmd.Stdout = appLog.WithField("_module", moduleName).WithField("func", "(STDOUT)").Writer()
	cmd.Stderr = appLog.WithField("_module", moduleName).WithField("func", "(STDERR)").Writer()

	return &Proc{
		disc:   disc,
		conf:   conf,
		log:    logging.MustGetLogger(moduleName),
		logDB:  appLogDB,
		cmd:    cmd,
		connCh: make(chan struct{}, 1),
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

// InjectConn introduces the connection to the Proc after it is started.
// Only the first call will return true.
// It also prepares the RPC gateway.
func (p *Proc) InjectConn(conn net.Conn) bool {
	ok := false

	p.connOnce.Do(func() {
		ok = true
		p.conn = conn
		p.rpcGW = NewRPCGateway(p.log)

		// Send ready signal.
		p.connCh <- struct{}{}
		close(p.connCh)
	})

	return ok
}

func (p *Proc) awaitConn() bool {
	if _, ok := <-p.connCh; !ok {
		return false
	}

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

	go func() {
		if ok := p.awaitConn(); !ok {
			_ = p.cmd.Process.Kill() //nolint:errcheck
			p.waitMx.Unlock()
			return
		}

		// App discovery start/stop.
		p.disc.Start()
		defer p.disc.Stop()

		// Wait for proc to exit.
		p.waitErr = p.cmd.Wait()

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
	if atomic.LoadInt32(&p.isRunning) != 1 {
		return errProcNotStarted
	}

	err := p.cmd.Process.Signal(os.Interrupt)
	if err != nil {
		return err
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
