package cxds

import (
	"sync"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

type memoryCXDS struct {
	mx  sync.RWMutex
	kvs map[cipher.SHA256]memoryObject

	amountAll  int
	amountUsed int

	voluemAll  int
	volumeUsed int
}

// object stored in memory
type memoryObject struct {
	rc  uint32
	val []byte
}

// NewMemoryCXDS creates CXDS-databse in
// memory. The database based on golang map
func NewMemoryCXDS() data.CXDS {
	return &memoryCXDS{kvs: make(map[cipher.SHA256]memoryObject)}
}

func (m *memoryCXDS) av(rc, nrc uint32, vol int) {

	if rc == 0 { // was dead
		if nrc > 0 { // an be resurrected
			m.amountUsed++
			m.volumeUsed += vol
		}
		return // else -> as is
	}

	// rc > 0 (was alive)

	if nrc == 0 { // an be killed
		m.amountUsed--
		m.volumeUsed -= vol
	}

}

func (m *memoryCXDS) incr(
	key cipher.SHA256,
	mo memoryObject,
	rc uint32,
	inc int,
) (
	nrc uint32,
) {

	switch {
	case inc == 0:
		nrc = rc // no changes
		return
	case inc < 0:
		inc = -inc // change the sign

		if uinc := uint32(inc); uinc >= rc {
			nrc = 0
		} else {
			nrc = rc - uinc
		}
	case inc > 0:
		nrc = rc + uint32(inc)
	}

	mo.rc = nrc
	m.kvs[key] = mo

	m.av(rc, nrc, len(mo.val))
	return

}

// Get value and change rc
func (m *memoryCXDS) Get(
	key cipher.SHA256,
	inc int,
) (
	val []byte,
	rc uint32,
	err error,
) {

	if inc == 0 { // read only
		m.mx.RLock()
		defer m.mx.RUnlock()
	} else { // read-write
		m.mx.Lock()
		defer m.mx.Unlock()
	}

	if mo, ok := m.kvs[key]; ok {
		val, rc = mo.val, mo.rc
		rc = m.incr(key, mo, rc, inc)
		return
	}
	err = data.ErrNotFound
	return
}

// Set value and change rc
func (m *memoryCXDS) Set(
	key cipher.SHA256,
	val []byte,
	inc int,
) (
	rc uint32,
	err error,
) {

	if inc <= 0 {
		panicf("invalid inc argument is Set: %d", inc)
	}

	if len(val) == 0 {
		err = ErrEmptyValue
		return
	}

	m.mx.Lock()
	defer m.mx.Unlock()

	if mo, ok := m.kvs[key]; ok {
		rc = m.incr(key, mo, mo.rc, inc)
		return
	}

	// created

	m.amountAll++
	m.voluemAll += len(val)

	m.amountUsed++
	m.volumeUsed += len(val)

	rc = uint32(inc)
	m.kvs[key] = memoryObject{rc, val}

	return
}

// Inc changes rc
func (m *memoryCXDS) Inc(
	key cipher.SHA256,
	inc int,
) (
	rc uint32,
	err error,
) {

	if inc == 0 { // presence check
		m.mx.RLock()
		defer m.mx.RUnlock()
	} else { // changes
		m.mx.Lock()
		defer m.mx.Unlock()
	}

	if mo, ok := m.kvs[key]; ok {
		rc = m.incr(key, mo, mo.rc, inc)
		return
	}

	err = data.ErrNotFound
	return
}

// Del deletes value unconditionally
func (m *memoryCXDS) Del(key cipher.SHA256) (_ error) {
	m.mx.Lock()
	defer m.mx.Unlock()

	var mo, ok = m.kvs[key]

	if ok == false {
		return // not found
	}

	if mo.rc > 0 {
		m.amountUsed--
		m.volumeUsed -= len(mo.val)
	}

	m.amountAll--
	m.voluemAll -= len(mo.val)

	return
}

// Iterate all keys
func (m *memoryCXDS) Iterate(iterateFunc data.IterateObjectsFunc) (err error) {

	m.mx.Lock()
	defer m.mx.Unlock()

	for k, mo := range m.kvs {
		if err = iterateFunc(k, mo.rc, mo.val); err != nil {
			if err == data.ErrStopIteration {
				err = nil
			}
			return
		}
	}

	return
}

// IterateDel all keys deleting
func (m *memoryCXDS) IterateDel(
	iterateFunc data.IterateObjectsDelFunc,
) (
	err error,
) {

	m.mx.Lock()
	defer m.mx.Unlock()

	var del bool

	for k, mo := range m.kvs {
		if del, err = iterateFunc(k, mo.rc, mo.val); err != nil {
			if err == data.ErrStopIteration {
				err = nil
			}
			return
		}
		if del == true {
			delete(m.kvs, k)
			if mo.rc > 0 {
				m.amountUsed--
				m.volumeUsed -= len(mo.val)
			}
			m.amountAll--
			m.voluemAll -= len(mo.val)
		}
	}

	return
}

// amount of objects
func (m *memoryCXDS) Amount() (all, used int) {
	m.mx.RLock()
	defer m.mx.RUnlock()

	return m.amountAll, m.amountUsed
}

// Volume of objects
func (m *memoryCXDS) Volume() (all, used int) {
	m.mx.RLock()
	defer m.mx.RUnlock()

	return m.voluemAll, m.volumeUsed
}

// Close DB
func (m *memoryCXDS) Close() (_ error) {
	m.mx.Lock()
	defer m.mx.Unlock()

	m.kvs = nil // clear
	return
}
