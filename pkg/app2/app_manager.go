package app2

import (
	"sync"

	"github.com/pkg/errors"
)

var (
	ErrAppExists = errors.New("app with such pid already exists")
)

type AppManager struct {
	apps map[string]*App
	mx   sync.RWMutex
}

func NewAppManager() *AppManager {
	return &AppManager{}
}

func (m *AppManager) Add(a *App) error {
	m.mx.Lock()
	defer m.mx.Unlock()

	if _, ok := m.apps[a.config.Name]; ok {
		return ErrAppExists
	}

	m.apps[a.config.Name] = a

	return nil
}

func (m *AppManager) App(name string) (*App, bool) {
	m.mx.RLock()
	defer m.mx.RUnlock()

	a, ok := m.apps[name]

	return a, ok
}

func (m *AppManager) Exists(name string) bool {
	m.mx.RLock()
	defer m.mx.RUnlock()

	_, ok := m.apps[name]

	return ok
}

func (m *AppManager) Remove(name string) {
	m.mx.Lock()
	defer m.mx.Unlock()

	delete(m.apps, name)
}

func (m *AppManager) Range(next func(name string, app *App) bool) {
	m.mx.RLock()
	defer m.mx.RUnlock()

	for name, app := range m.apps {
		if !next(name, app) {
			return
		}
	}
}
