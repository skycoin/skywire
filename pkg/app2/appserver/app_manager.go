package appserver

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
	if _, ok := m.apps[a.config.Name]; ok {
		m.mx.Unlock()
		return ErrAppExists
	}
	m.apps[a.config.Name] = a
	m.mx.Unlock()

	return nil
}

func (m *AppManager) App(name string) (*App, bool) {
	m.mx.RLock()
	a, ok := m.apps[name]
	m.mx.RUnlock()
	return a, ok
}
