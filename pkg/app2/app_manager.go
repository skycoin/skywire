package app2

import (
	"sync"

	"github.com/pkg/errors"
)

var (
	// ErrAppExists is returned when trying to add
	// app to the manager which already exists.
	ErrAppExists = errors.New("app with such name already exists")
)

// AppManager allows to store and retrieve skywire apps.
type AppManager struct {
	apps map[string]*App
	mx   sync.RWMutex
}

// NewAppManager constructs `AppManager`.
func NewAppManager() *AppManager {
	return &AppManager{}
}

// Add adds app `a` to the manager instance.
func (m *AppManager) Add(a *App) error {
	m.mx.Lock()
	defer m.mx.Unlock()

	if _, ok := m.apps[a.config.Name]; ok {
		return ErrAppExists
	}

	m.apps[a.config.Name] = a

	return nil
}

// App gets the app from the manager if it exists. Returns bool
// flag to indicate operation success.
func (m *AppManager) App(name string) (*App, bool) {
	m.mx.RLock()
	defer m.mx.RUnlock()

	a, ok := m.apps[name]

	return a, ok
}

// Exists checks whether app exists in the manager instance.
func (m *AppManager) Exists(name string) bool {
	m.mx.RLock()
	defer m.mx.RUnlock()

	_, ok := m.apps[name]

	return ok
}

// Remove removes app with the name `name` from the manager instance.
func (m *AppManager) Remove(name string) {
	m.mx.Lock()
	defer m.mx.Unlock()

	delete(m.apps, name)
}

// Range allows to iterate over stored apps. Calls `next` on each iteration.
// Stops execution once `next` returns false.
func (m *AppManager) Range(next func(name string, app *App) bool) {
	m.mx.RLock()
	defer m.mx.RUnlock()

	for name, app := range m.apps {
		if !next(name, app) {
			return
		}
	}
}
