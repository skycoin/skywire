package appserver

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appdisc"
)

//go:generate mockery -name ProcManager -case underscore -inpkg

const (
	// ProcStartTimeout represents the duration in which a proc should have started and connected with the app server.
	ProcStartTimeout = time.Second * 5
)

var (
	// ErrAppAlreadyStarted is returned when trying to run the already running app.
	ErrAppAlreadyStarted = errors.New("app already started")
	errNoSuchApp         = errors.New("no such app")

	ErrClosed = errors.New("proc manager is already closed")
)

// ProcManager allows to manage skywire applications.
type ProcManager interface {
	io.Closer
	Start(conf appcommon.ProcConfig, stdout, stderr io.Writer) (appcommon.ProcID, error)
	Exists(name string) bool
	Stop(name string) error
	Wait(name string) error
	Range(next func(name string, proc *Proc) bool)
}

// procManager manages skywire applications. It implements `ProcManager`.
type procManager struct {
	log *logging.Logger

	addr    string // listening address
	lis     net.Listener
	conns   map[string]net.Conn
	connsWG sync.WaitGroup

	discF      *appdisc.Factory
	procs      map[string]*Proc
	procsByKey map[appcommon.ProcKey]*Proc

	mx       sync.RWMutex
	done     chan struct{}
	doneOnce sync.Once
}

// NewProcManager constructs `ProcManager`.
func NewProcManager(log *logging.Logger, discF *appdisc.Factory, addr string) (ProcManager, error) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	procM := &procManager{
		addr:       addr,
		log:        log,
		lis:        lis,
		conns:      make(map[string]net.Conn),
		discF:      discF,
		procs:      make(map[string]*Proc),
		procsByKey: make(map[appcommon.ProcKey]*Proc),
		done:       make(chan struct{}),
	}

	procM.connsWG.Add(1)
	go func() {
		defer procM.connsWG.Done()
		procM.serve()
	}()

	return procM, nil
}

func (m *procManager) serve() {
	defer func() {
		for _, conn := range m.conns {
			_ = conn.Close() //nolint:errcheck
		}
	}()

	for {
		conn, err := m.lis.Accept()
		if err != nil {
			if !isDone(m.done) {
				m.log.WithError(err).WithField("remote_addr", conn.RemoteAddr()).
					Info("Unexpected error occurred when accepting app conn.")
			}
			return
		}
		m.conns[conn.RemoteAddr().String()] = conn

		m.connsWG.Add(1)
		go func(conn net.Conn) {
			defer m.connsWG.Done()

			if ok := m.handleConn(conn); !ok {
				if err := conn.Close(); err != nil {
					m.log.WithError(err).WithField("remote_addr", conn.RemoteAddr()).
						Warn("Failed to close problematic app conn.")
				}
			}
		}(conn)
	}
}

func (m *procManager) handleConn(conn net.Conn) bool {
	log := m.log.WithField("remote", conn.RemoteAddr())
	log.Debug("Accepting proc conn...")

	// Read in and check key.
	var key appcommon.ProcKey
	if n, err := io.ReadFull(conn, key[:]); err != nil {
		log.WithError(err).
			WithField("n", n).
			Warn("Failed to read proc key.")
		return false
	}

	log = log.WithField("proc_key", key.String())
	log.Debug("Read proc key.")

	// Push conn to Proc.
	m.mx.RLock()
	proc, ok := m.procsByKey[key]
	m.mx.RUnlock()
	if !ok {
		log.Error("Failed to find proc of given key.")
		return false
	}
	if ok := proc.InjectConn(conn); !ok {
		log.Error("Failed to associate conn with proc.")
		return false
	}
	log.Info("Accepted proc conn.")
	return true
}

// Start starts the application according to its config and additional args.
func (m *procManager) Start(conf appcommon.ProcConfig, stdout, stderr io.Writer) (appcommon.ProcID, error) {
	m.mx.Lock()
	defer m.mx.Unlock()

	log := logging.MustGetLogger("proc:" + conf.AppName + ":" + conf.ProcKey.String())

	// isDone should be called within the protection of a mutex.
	// Otherwise we may be able to start an app after calling Close.
	if isDone(m.done) {
		return 0, ErrClosed
	}

	if _, ok := m.procs[conf.AppName]; ok {
		return 0, ErrAppAlreadyStarted
	}

	// Ensure proc key is unique (just in case - this is probably not necessary).
	for {
		if _, ok := m.procsByKey[conf.ProcKey]; ok {
			conf.EnsureKey()
			continue
		}
		break
	}

	disc, ok := m.discF.Updater(conf)
	if !ok {
		log.WithField("appName", conf.AppName).
			Debug("No app discovery associated with app.")
	}

	proc := NewProc(log, conf, disc, stdout, stderr)
	m.procs[conf.AppName] = proc
	m.procsByKey[conf.ProcKey] = proc

	if err := proc.Start(); err != nil {
		return 0, err
	}

	return appcommon.ProcID(proc.cmd.Process.Pid), nil
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

// stopAll stops all the apps run with this manager instance.
func (m *procManager) stopAll() {
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

// Close implements io.Closer
func (m *procManager) Close() error {
	m.mx.Lock()
	defer m.mx.Unlock()

	if isDone(m.done) {
		return ErrClosed
	}
	close(m.done)

	m.stopAll()
	err := m.lis.Close()
	m.connsWG.Wait()
	return err
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

func isDone(done chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}
