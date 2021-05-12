package visorinit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/skycoin/skycoin/src/util/logging"
)

// Hook is a function that can be run at some point as part
// of module initialization
// This function will be called with initialization context. Pass your custom
// data via that context, and retrieve it within your hooks.
type Hook func(ctx context.Context, log *logging.Logger) error

// Module is a single system unit that represents a part of the system that must
// be initialized. Module can have dependencies, that should be initialized before
// module can start its own initialization
type Module struct {
	Name    string
	init    Hook
	err     error
	done    chan struct{}
	deps    []*Module
	mux     *sync.Mutex
	started bool
	log     *logging.Logger
}

// DoNothing is an initialization hook that does nothing
var DoNothing Hook = func(ctx context.Context, log *logging.Logger) error {
	return nil
}

// ErrNoInit is returned when module init function is not set
var ErrNoInit = errors.New("module initialization function is not set")

// MakeModule returns a new module with given init function and dependencies
func MakeModule(name string, init Hook, ml *logging.MasterLogger, deps ...*Module) Module {
	done := make(chan struct{})
	mux := new(sync.Mutex)
	return Module{
		Name: name,
		init: init,
		deps: deps,
		done: done,
		mux:  mux,
		log:  ml.PackageLogger(name),
	}
}

// start module initialiation process
// return true if successfully started, false otherwise
// start can fail in case if init has already started concurrently,
// or if the module has finished initializing
func (m *Module) start() bool {
	m.mux.Lock()
	defer m.mux.Unlock()
	if m.started || m.isFinished() {
		return false
	}
	m.started = true
	return true
}

// finish module initialization process
func (m *Module) stop() {
	m.mux.Lock()
	defer m.mux.Unlock()
	if m.started && !m.isFinished() {
		close(m.done)
	}
}

func (m *Module) isFinished() bool {
	select {
	case <-m.done:
		return true
	default:
		return false
	}
}

// Wait for the module to be initialized
// return initialization error if any
func (m *Module) Wait(ctx context.Context) error {
	select {
	case <-m.done:
		if m.err != nil {
			return m.err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// InitConcurrent initializes all module dependencies recursively and concurrently.
// If module depends on modules a and b, this function will try to run init functions for a and b
// in each in a separate goroutine. It will block and wait on modules whose dependencies are not
// yet fully initialized themselves
// This function blocks until all dependencis are initialized
func (m *Module) InitConcurrent(ctx context.Context) {
	ok := m.start()
	// either init process has been started
	if !ok {
		return
	}
	defer m.stop()
	m.log.Info("Starting")
	start := time.Now()
	// start init in every dependency
	for _, dep := range m.deps {
		go dep.InitConcurrent(ctx)
	}

	// wait for every dependency to finish
	// collect error status for each, and set own error in case
	// any dependency errored
	// when canceled return immediately
	// todo: waitgroup + errors channel might be quicker to fail than
	// iterating and waiting
	for _, dep := range m.deps {
		err := dep.Wait(ctx)
		if err != nil {
			m.err = err
			return
		}
	}
	if m.init == nil {
		m.err = fmt.Errorf("init module %s error: %w", m.Name, ErrNoInit)
		return
	}
	startSelf := time.Now()
	// init the module itself
	err := m.init(ctx, m.log)
	m.log.Infof("Initialized in %s (%s with dependencies)", time.Since(startSelf), time.Since(start))
	if err != nil {
		m.err = err
	}
}
