package appserver

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app/appcommon"
)

//go:generate mockery -name ProcManager -case underscore -inpkg

var (
	// ErrAppAlreadyStarted is returned when trying to run the already running app.
	ErrAppAlreadyStarted = errors.New("app already started")
	errNoSuchApp         = errors.New("no such app")
)

// ProcManager allows to manage skywire applications.
type ProcManager interface {
	Start(log *logging.Logger, c appcommon.Config, args []string, stdout, stderr io.Writer) (appcommon.ProcID, error)
	Exists(name string) bool
	Stop(name string) error
	Wait(name string) error
	Range(next func(name string, proc *Proc) bool)
	StopAll()
}

// procManager allows to manage skywire applications.
// Implements `ProcManager`.
type procManager struct {
	log       *logging.Logger
	procs     map[string]*Proc
	mx        sync.RWMutex
	rpcServer *Server
}

// NewProcManager constructs `ProcManager`.
func NewProcManager(log *logging.Logger, rpcServer *Server) ProcManager {
	return &procManager{
		log:       log,
		procs:     make(map[string]*Proc),
		rpcServer: rpcServer,
	}
}

// Start start the application according to its config and additional args.
func (m *procManager) Start(log *logging.Logger, c appcommon.Config, args []string,
	stdout, stderr io.Writer) (appcommon.ProcID, error) {
	if m.Exists(c.Name) {
		return 0, ErrAppAlreadyStarted
	}

	p, err := NewProc(log, c, args, stdout, stderr)
	if err != nil {
		return 0, err
	}

	if err := m.rpcServer.Register(p.key); err != nil {
		return 0, err
	}

	m.mx.Lock()
	m.procs[c.Name] = p
	m.mx.Unlock()

	if err := p.Start(); err != nil {
		return 0, err
	}

	return appcommon.ProcID(p.cmd.Process.Pid), nil
}

// Exists check whether app exists in the manager instance.
func (m *procManager) Exists(name string) bool {
	m.mx.RLock()
	defer m.mx.RUnlock()

	_, ok := m.procs[name]

	return ok
}

// Stop stops the application.
func (m *procManager) Stop(name string) error {
	p, err := m.pop(name)
	if err != nil {
		return err
	}

	return p.Stop()
}

// Wait waits for the application to exit.
func (m *procManager) Wait(name string) error {
	p, err := m.get(name)
	if err != nil {
		return err
	}

	// While waiting for p.Wait() call, we need app to present in the processes list,
	// so we cannot pop it before p.Wait().
	if err := p.Wait(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			err = fmt.Errorf("failed to run app executable %s: %v", name, err)
		}

		if _, err := m.pop(name); err != nil {
			m.log.Debugf("Remove app <%v>: %v", name, err)
		}

		return err
	}

	_, err = m.pop(name)

	return err
}

// Range allows to iterate over running skywire apps. Calls `next` on
// each iteration. If `next` returns falls - stops iteration.
func (m *procManager) Range(next func(name string, proc *Proc) bool) {
	m.mx.RLock()
	defer m.mx.RUnlock()

	for name, proc := range m.procs {
		if !next(name, proc) {
			break
		}
	}
}

// StopAll stops all the apps run with this manager instance.
func (m *procManager) StopAll() {
	m.mx.Lock()
	defer m.mx.Unlock()

	for name, proc := range m.procs {
		log := m.log.WithField("app_name", name)
		if err := proc.Stop(); err != nil && strings.Contains(err.Error(), "process already finished") {
			log.WithError(err).Error("Failed to stop app.")
			continue
		}
		log.Infof("App stopped successfully.")
	}

	m.procs = make(map[string]*Proc)
}

// pop removes application from the manager instance and returns it.
func (m *procManager) pop(name string) (*Proc, error) {
	m.mx.Lock()
	defer m.mx.Unlock()

	p, ok := m.procs[name]
	if !ok {
		return nil, errNoSuchApp
	}

	delete(m.procs, name)

	return p, nil
}

// get returns application from the manager instance.
func (m *procManager) get(name string) (*Proc, error) {
	m.mx.Lock()
	defer m.mx.Unlock()

	p, ok := m.procs[name]
	if !ok {
		return nil, errNoSuchApp
	}

	return p, nil
}
