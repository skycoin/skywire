package appserver

import (
	"context"
	"errors"
	"io"
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

	connCh chan net.Conn
}

// NewProc constructs `Proc`.
func NewProc(log *logging.Logger, conf appcommon.ProcConfig, disc appdisc.Updater, stdout, stderr io.Writer) *Proc {
	cmd := exec.Command(conf.BinaryLoc(), conf.ProcArgs...) // nolint:gosec
	cmd.Env = conf.Envs()
	cmd.Dir = conf.WorkDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	return &Proc{
		disc:   disc,
		conf:   conf,
		log:    log,
		cmd:    cmd,
		connCh: make(chan net.Conn, 1),
	}
}

// InjectConn introduces the connection to the Proc after it is started.
func (p *Proc) InjectConn(conn net.Conn) bool {
	select {
	case p.connCh <- conn:
		return true
	default:
		return false
	}
}

func (p *Proc) awaitConn(ctx context.Context) error {
	select {
	case conn := <-p.connCh:
		rpcS := rpc.NewServer()
		if err := rpcS.RegisterName(p.conf.ProcKey.String(), NewRPCGateway(p.log)); err != nil {
			panic(err)
		}
		go rpcS.ServeConn(conn)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Start starts the application.
func (p *Proc) Start(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&p.isRunning, 0, 1) {
		return errProcAlreadyRunning
	}

	// acquire lock immediately
	p.waitMx.Lock()

	if err := p.cmd.Start(); err != nil {
		p.waitMx.Unlock()
		return err
	}
	if err := p.awaitConn(ctx); err != nil {
		_ = p.cmd.Process.Kill() //nolint:errcheck
		p.waitMx.Unlock()
		return err
	}

	go func() {
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
	defer p.waitMx.Unlock()

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
