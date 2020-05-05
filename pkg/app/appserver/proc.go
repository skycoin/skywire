package appserver

import (
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"os"
	"os/exec"
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
	disc appdisc.Updater // App discovery client.
	conf appcommon.ProcConfig
	log  *logging.Logger

	cmd       *exec.Cmd
	isRunning int32
	waitMx    sync.Mutex
	waitErr   error

	connCh   chan net.Conn
	connOnce sync.Once
}

// NewProc constructs `Proc`.
func NewProc(conf appcommon.ProcConfig, disc appdisc.Updater) *Proc {

	moduleName := fmt.Sprintf("proc:%s:%s", conf.AppName, conf.ProcKey)

	cmd := exec.Command(conf.BinaryLoc, conf.ProcArgs...) // nolint:gosec
	cmd.Dir = conf.ProcWorkDir
	cmd.Env = append(os.Environ(), conf.Envs()...)

	log := conf.Logger()
	cmd.Stdout = log.WithField("_module", moduleName).WithField("func", "(STDOUT)").Writer()
	cmd.Stderr = log.WithField("_module", moduleName).WithField("func", "(STDERR)").Writer()

	return &Proc{
		disc:   disc,
		conf:   conf,
		log:    logging.MustGetLogger(moduleName),
		cmd:    cmd,
		connCh: make(chan net.Conn, 1),
	}
}

// InjectConn introduces the connection to the Proc after it is started.
func (p *Proc) InjectConn(conn net.Conn) bool {
	ok := false
	p.connOnce.Do(func() {
		p.connCh <- conn
		close(p.connCh)
		ok = true
	})
	return ok
}

func (p *Proc) awaitConn() bool {
	conn, ok := <-p.connCh
	if !ok {
		return false
	}
	rpcS := rpc.NewServer()
	if err := rpcS.RegisterName(p.conf.ProcKey.String(), NewRPCGateway(p.log)); err != nil {
		panic(err)
	}
	go rpcS.ServeConn(conn)
	p.log.Info("Associated and serving proc conn.")
	return true
}

// Start starts the application.
func (p *Proc) Start() error {
	if !atomic.CompareAndSwapInt32(&p.isRunning, 0, 1) {
		return errProcAlreadyRunning
	}

	// acquire lock immediately
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
		p.disc.Start()
		p.waitErr = p.cmd.Wait()
		p.disc.Stop()
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
		p.connOnce.Do(func() {
			close(p.connCh)
		})
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
