package restart

import (
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	// ErrMalformedArgs is returned when executable args are malformed.
	ErrMalformedArgs = errors.New("malformed args")
	// ErrAlreadyRestarting is returned on restarting attempt when restarting is in progress.
	ErrAlreadyRestarting = errors.New("already restarting")
)

// DefaultCheckDelay is a default delay for checking if a new instance is started successfully.
const DefaultCheckDelay = 1 * time.Second

// Context describes data required for restarting visor.
type Context struct {
	log              logrus.FieldLogger
	checkDelay       time.Duration
	workingDirectory string
	args             []string
	isRestarting     int32
	appendDelay      bool // disabled in tests
}

// CaptureContext captures data required for restarting visor.
// Data used by CaptureContext must not be modified before,
// therefore calling CaptureContext immediately after starting executable is recommended.
func CaptureContext() (*Context, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	args := os.Args

	context := &Context{
		checkDelay:       DefaultCheckDelay,
		workingDirectory: wd,
		args:             args,
		appendDelay:      true,
	}

	return context, nil
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

// Start starts a new executable using Context.
func (c *Context) Start() error {
	if !atomic.CompareAndSwapInt32(&c.isRestarting, 0, 1) {
		return ErrAlreadyRestarting
	}

	defer atomic.StoreInt32(&c.isRestarting, 0)

	if len(c.args) == 0 {
		return ErrMalformedArgs
	}

	execPath := c.args[0]
	if !filepath.IsAbs(execPath) {
		execPath = filepath.Join(c.workingDirectory, execPath)
	}

	errCh := c.startExec(execPath)

	ticker := time.NewTicker(c.checkDelay)
	defer ticker.Stop()

	select {
	case err := <-errCh:
		c.errorLogger()("Failed to start new instance: %v", err)
		return err
	case <-ticker.C:
		c.infoLogger()("New instance started successfully, exiting")
		return nil
	}
}

func (c *Context) startExec(path string) chan error {
	errCh := make(chan error, 1)

	go func(path string) {
		defer close(errCh)

		normalizedPath, err := exec.LookPath(path)
		if err != nil {
			errCh <- err
			return
		}

		if len(c.args) == 0 {
			errCh <- ErrMalformedArgs
			return
		}

		args := c.startArgs()
		cmd := exec.Command(normalizedPath, args...) // nolint:gosec

		cmd.Stdout = os.Stdout
		cmd.Stdin = os.Stdin
		cmd.Stderr = os.Stderr
		cmd.Env = os.Environ()

		c.infoLogger()("Starting new instance of executable (path: %q, args: %q)", path, args)

		if err := cmd.Start(); err != nil {
			errCh <- err
			return
		}

		if err := cmd.Wait(); err != nil {
			errCh <- err
			return
		}
	}(path)

	return errCh
}

const extraWaitingTime = 1 * time.Second

func (c *Context) startArgs() []string {
	args := c.args[1:]

	const delayArgName = "--delay"

	l := len(args)
	for i := 0; i < l; i++ {
		if args[i] == delayArgName && i < len(args)-1 {
			args = append(args[:i], args[i+2:]...)
			i--
			l -= 2
		}
	}

	if c.appendDelay {
		delay := c.checkDelay + extraWaitingTime
		args = append(args, delayArgName, delay.String())
	}

	return args
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
