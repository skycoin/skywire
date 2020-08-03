package appserver

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app/appcommon"
)

var (
	errProcAlreadyRunning = errors.New("process already running")
	errProcNotStarted     = errors.New("process is not started")
)

// Proc is a wrapper for a skywire app. Encapsulates
// the running process itself and the RPC server for
// app/visor communication.
type Proc struct {
	key       appcommon.Key
	config    appcommon.Config
	log       *logging.Logger
	cmd       *exec.Cmd
	isRunning int32
	waitMx    sync.Mutex
	waitErr   error
}

// NewProc constructs `Proc`.
func NewProc(log *logging.Logger, c appcommon.Config, args []string, stdout, stderr io.Writer) (*Proc, error) {
	key := appcommon.GenerateAppKey()

	binaryPath := getBinaryPath(c.BinaryDir, c.Name)

	const (
		appKeyEnvFormat     = appcommon.EnvAppKey + "=%s"
		serverAddrEnvFormat = appcommon.EnvServerAddr + "=%s"
		visorPKEnvFormat    = appcommon.EnvVisorPK + "=%s"
	)

	env := make([]string, 0, 4)
	env = append(env, fmt.Sprintf(appKeyEnvFormat, key))
	env = append(env, fmt.Sprintf(serverAddrEnvFormat, c.ServerAddr))
	env = append(env, fmt.Sprintf(visorPKEnvFormat, c.VisorPK))

	cmd := exec.Command(binaryPath, args...) // nolint:gosec

	cmd.Env = env
	cmd.Dir = c.WorkDir

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	return &Proc{
		key:    key,
		config: c,
		log:    log,
		cmd:    cmd,
	}, nil
}

// Start starts the application.
func (p *Proc) Start() error {
	if !atomic.CompareAndSwapInt32(&p.isRunning, 0, 1) {
		return errProcAlreadyRunning
	}

	if err := p.cmd.Start(); err != nil {
		return err
	}

	// acquire lock immediately
	p.waitMx.Lock()
	go func() {
		defer p.waitMx.Unlock()
		p.waitErr = p.cmd.Wait()
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

// getBinaryPath formats binary path using app dir, name and version.
func getBinaryPath(dir, name string) string {
	return filepath.Join(dir, name)
}
