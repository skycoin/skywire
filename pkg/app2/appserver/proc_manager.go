package appserver

import (
	"sync"

	"github.com/pkg/errors"

	"github.com/skycoin/skycoin/src/util/logging"
)

// ProcManager allows to manage skywire applications.
type ProcManager struct {
	procs map[string]*Proc
	mx    sync.RWMutex
}

// NewProcManager constructs `ProcManager`.
func NewProcManager() *ProcManager {
	return &ProcManager{
		procs: make(map[string]*Proc),
	}
}

// Run runs the application according to its config and additional args.
func (m *ProcManager) Run(log *logging.Logger, c Config, args []string) error {
	// TODO: pass another logging instance?
	p, err := NewProc(log, c, args)
	if err != nil {
		return err
	}

	if err := p.Run(); err != nil {
		return err
	}

	m.mx.Lock()
	m.procs[c.Name] = p
	m.mx.Unlock()

	return nil
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

	return p.Wait()
}

func (m *ProcManager) pop(name string) (*Proc, error) {
	m.mx.Lock()
	p, ok := m.procs[name]
	if !ok {
		m.mx.Unlock()
		return nil, errors.New("no such app")
	}
	delete(m.procs, name)
	m.mx.Unlock()

	return p, nil
}
