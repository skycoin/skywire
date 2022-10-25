// Package idmanager pkg/app/idmanager/manager.go
package idmanager

import (
	"errors"
	"fmt"
	"io"
	"sync"
)

var (
	// ErrNoMoreAvailableValues is returned when all the slots are reserved.
	ErrNoMoreAvailableValues = errors.New("no more available values")

	// ErrValueAlreadyExists is returned when value associated with the specified key already exists.
	ErrValueAlreadyExists = errors.New("value already exists")
)

// Manager manages allows to store and retrieve arbitrary values
// associated with the `uint16` key in a thread-safe manner.
// Provides method to generate key.
type Manager struct {
	values map[uint16]interface{}
	mx     sync.RWMutex
	lstID  uint16

	di *DeltaInformer // optional
}

// New constructs new `Manager`.
func New() *Manager {
	return &Manager{
		values: make(map[uint16]interface{}),
	}
}

// AddDeltaInformer adds a DeltaInformer to the id manager and returns the DeltaInformer.
func (m *Manager) AddDeltaInformer() *DeltaInformer {
	di := NewDeltaInformer()
	m.mx.Lock()
	m.di = di
	m.mx.Unlock()
	return di
}

// ReserveNextID reserves next free slot for the value and returns the id for it.
func (m *Manager) ReserveNextID() (id *uint16, free func() bool, err error) {
	m.mx.Lock()

	nxtID := m.lstID + 1
	for ; nxtID != m.lstID; nxtID++ {
		if _, ok := m.values[nxtID]; !ok {
			break
		}
	}

	if nxtID == m.lstID {
		m.mx.Unlock()
		return nil, nil, ErrNoMoreAvailableValues
	}

	m.values[nxtID] = nil
	m.lstID = nxtID

	m.di.Trigger(len(m.values))
	m.mx.Unlock()

	return &nxtID, m.constructFreeFunc(nxtID), nil
}

// Pop removes value specified by `id` from the idManager instance and
// returns it.
func (m *Manager) Pop(id uint16) (interface{}, error) {
	m.mx.Lock()

	v, ok := m.values[id]
	if !ok {
		m.mx.Unlock()
		return nil, fmt.Errorf("no value with id %d", id)
	}

	if v == nil {
		m.mx.Unlock()
		return nil, fmt.Errorf("value with id %d is not set", id)
	}

	delete(m.values, id)

	m.di.Trigger(len(m.values))
	m.mx.Unlock()

	return v, nil
}

// Add adds the new value `v` associated with `id`.
func (m *Manager) Add(id uint16, v interface{}) (free func() bool, err error) {
	m.mx.Lock()

	if _, ok := m.values[id]; ok {
		m.mx.Unlock()
		return nil, ErrValueAlreadyExists
	}

	m.values[id] = v

	m.di.Trigger(len(m.values))
	m.mx.Unlock()

	return m.constructFreeFunc(id), nil
}

// Set sets value `v` associated with `id`.
func (m *Manager) Set(id uint16, v interface{}) error {
	m.mx.Lock()

	l, ok := m.values[id]
	if !ok {
		m.mx.Unlock()
		return errors.New("id is not reserved")
	}

	if l != nil {
		m.mx.Unlock()
		return ErrValueAlreadyExists
	}

	m.values[id] = v

	m.mx.Unlock()

	return nil
}

// Get gets the value associated with the `id`.
func (m *Manager) Get(id uint16) (interface{}, bool) {
	m.mx.RLock()
	lis, ok := m.values[id]
	m.mx.RUnlock()

	if lis == nil {
		return nil, false
	}

	return lis, ok
}

// DoRange performs range over the manager contents. Loop stops when
// `next` returns false.
func (m *Manager) DoRange(next func(id uint16, v interface{}) bool) {
	m.mx.RLock()
	for id, v := range m.values {
		if !next(id, v) {
			break
		}
	}
	m.mx.RUnlock()
}

// Len returns the combined count of both reserved and used IDs.
func (m *Manager) Len() int {
	m.mx.RLock()
	out := len(m.values)
	m.mx.RUnlock()
	return out
}

// CloseAll closes and removes all internal values that implements io.Closer
func (m *Manager) CloseAll() {
	wg := new(sync.WaitGroup)

	m.mx.Lock()
	for k, v := range m.values {
		c, ok := v.(io.Closer)
		if !ok {
			continue
		}
		delete(m.values, k)

		wg.Add(1)
		go func(c io.Closer) {
			_ = c.Close() // nolint:errcheck
			wg.Done()
		}(c)
	}
	m.di.Stop()
	m.mx.Unlock()

	wg.Wait()
}

// constructFreeFunc constructs new func responsible for clearing
// a slot with the specified `id`.
func (m *Manager) constructFreeFunc(id uint16) func() bool {
	var once sync.Once

	return func() bool {
		var freed bool

		once.Do(func() {
			freed = true

			m.mx.Lock()
			delete(m.values, id)
			m.mx.Unlock()
		})

		return freed
	}
}
