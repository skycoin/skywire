package appserver

import (
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/skycoin/skywire/pkg/app2/appcommon"

	"github.com/pkg/errors"

	"github.com/skycoin/skycoin/src/util/logging"
)

var (
	errAppAlreadyExists = errors.New("app already exists")
	errNoSuchApp        = errors.New("no such app")
)

// ProcManager allows to manage skywire applications.
type ProcManager struct {
	log   *logging.Logger
	procs map[string]*Proc
	mx    sync.RWMutex
}

// NewProcManager constructs `ProcManager`.
func NewProcManager(log *logging.Logger) *ProcManager {
	return &ProcManager{
		log:   log,
		procs: make(map[string]*Proc),
	}
}

// Run runs the application according to its config and additional args.
func (m *ProcManager) Run(log *logging.Logger, c appcommon.Config, args []string,
	stdout, stderr io.Writer) (appcommon.ProcID, error) {
	if m.Exists(c.Name) {
		return 0, errAppAlreadyExists
	}

	p, err := NewProc(log, c, args, stdout, stderr)
	if err != nil {
		return 0, err
	}

	if err := p.Run(); err != nil {
		return 0, err
	}

	m.mx.Lock()
	if _, ok := m.procs[c.Name]; ok {
		m.mx.Unlock()
		if err := p.Stop(); err != nil {
			m.log.WithError(err).Error("error stopping app")
		}
		return 0, errAppAlreadyExists
	}
	m.procs[c.Name] = p
	m.mx.Unlock()

	return appcommon.ProcID(p.cmd.Process.Pid), nil
}

// Exists check whether app exists in the manager instance.
func (m *ProcManager) Exists(name string) bool {
	m.mx.RLock()
	defer m.mx.RUnlock()

	_, ok := m.procs[name]
	return ok
}

// Stop stops the application.
func (m *ProcManager) Stop(name string) error {
	p, err := m.pop(name)
	if err != nil {
		return err
	}

	return p.Stop()
}

// Wait waits for the application to exit.
func (m *ProcManager) Wait(name string) error {
	p, err := m.pop(name)
	if err != nil {
		return err
	}

	if err := p.Wait(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			err = fmt.Errorf("failed to run app executable: %s", err)
		}

		return err
	}

	return nil
}

// Range allows to iterate over running skywire apps. Calls `next` on
// each iteration. If `next` returns falls - stops iteration.
func (m *ProcManager) Range(next func(name string, proc *Proc) bool) {
	m.mx.RLock()
	defer m.mx.RUnlock()

	for name, proc := range m.procs {
		if !next(name, proc) {
			break
		}
	}
}

// pop removes application from the manager instance and returns it.
func (m *ProcManager) pop(name string) (*Proc, error) {
	m.mx.Lock()
	defer m.mx.Unlock()

	p, ok := m.procs[name]
	if !ok {
		return nil, errNoSuchApp
	}

	delete(m.procs, name)

	return p, nil
}
