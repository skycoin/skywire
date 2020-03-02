package restart

import (
	"errors"
	"log"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	// ErrAlreadyStarting is returned on starting attempt when starting is in progress.
	ErrAlreadyStarting = errors.New("already starting")
)

const (
	// DefaultCheckDelay is a default delay for checking if a new instance is started successfully.
	DefaultCheckDelay = 1 * time.Second
	extraWaitingTime  = 1 * time.Second
	delayArgName      = "--delay"
)

// Context describes data required for restarting visor.
type Context struct {
	log         logrus.FieldLogger
	cmd         *exec.Cmd
	checkDelay  time.Duration
	isStarting  int32
	appendDelay bool // disabled in tests
}

// CaptureContext captures data required for restarting visor.
// Data used by CaptureContext must not be modified before,
// therefore calling CaptureContext immediately after starting executable is recommended.
func CaptureContext() *Context {
	cmd := exec.Command(os.Args[0], os.Args[1:]...) // nolint:gosec

	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	return &Context{
		cmd:         cmd,
		checkDelay:  DefaultCheckDelay,
		appendDelay: true,
	}
}

// RegisterLogger registers a logger instead of standard one.
func (c *Context) RegisterLogger(logger logrus.FieldLogger) {
	if c != nil {
		c.log = logger
	}
}

// SetCheckDelay sets a check delay instead of standard one.
func (c *Context) SetCheckDelay(delay time.Duration) {
	if c != nil {
		c.checkDelay = delay
	}
}

// CmdPath returns path of cmd to be run.
func (c *Context) CmdPath() string {
	return c.cmd.Path
}

// Start starts a new executable using Context.
func (c *Context) Start() error {
	if !atomic.CompareAndSwapInt32(&c.isStarting, 0, 1) {
		return ErrAlreadyStarting
	}

	defer atomic.StoreInt32(&c.isStarting, 0)

	errCh := c.startExec()

	ticker := time.NewTicker(c.checkDelay)
	defer ticker.Stop()

	select {
	case err := <-errCh:
		c.errorLogger()("Failed to start new instance: %v", err)
		return err
	case <-ticker.C:
		c.infoLogger()("New instance started successfully, exiting from the old one")
		return nil
	}
}

func (c *Context) startExec() chan error {
	errCh := make(chan error, 1)

	go func() {
		defer close(errCh)

		c.adjustArgs()

		c.infoLogger()("Starting new instance of executable (args: %q)", c.cmd.Args)

		if err := c.cmd.Start(); err != nil {
			errCh <- err
			return
		}

		if err := c.cmd.Wait(); err != nil {
			errCh <- err
			return
		}
	}()

	return errCh
}

func (c *Context) adjustArgs() {
	args := c.cmd.Args

	i := 0
	l := len(args)

	for i < l {
		if args[i] == delayArgName && i < len(args)-1 {
			args = append(args[:i], args[i+2:]...)
			l -= 2
		} else {
			i++
		}
	}

	if c.appendDelay {
		delay := c.checkDelay + extraWaitingTime
		args = append(args, delayArgName, delay.String())
	}

	c.cmd.Args = args
}

func (c *Context) infoLogger() func(string, ...interface{}) {
	if c.log != nil {
		return c.log.Infof
	}

	logger := log.New(os.Stdout, "[INFO] ", log.LstdFlags)

	return logger.Printf
}

func (c *Context) errorLogger() func(string, ...interface{}) {
	if c.log != nil {
		return c.log.Errorf
	}

	logger := log.New(os.Stdout, "[ERROR] ", log.LstdFlags)

	return logger.Printf
}
