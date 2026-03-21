package registry

import (
	"errors"

	"github.com/skycoin/skycoin/src/cipher"
)

// dummy pack

type dummyPack struct {
	reg    *Registry
	flags  Flags
	degree Degree
	vals   map[cipher.SHA256][]byte
}

func (d *dummyPack) Registry() *Registry {
	return d.reg
}

func (d *dummyPack) Get(key cipher.SHA256) (val []byte, err error) {
	var ok bool
	if val, ok = d.vals[key]; !ok {
		err = ErrNotFound
	}
	return
}

func (d *dummyPack) Set(key cipher.SHA256, val []byte) (_ error) {
	d.vals[key] = val
	return
}

func (d *dummyPack) Add(val []byte) (key cipher.SHA256, err error) {
	key = cipher.SumSHA256(val)
	err = d.Set(key, val)
	return
}

func (d *dummyPack) Degree() Degree {
	return d.degree
}

func (d *dummyPack) SetDegree(degree Degree) (err error) {
	if err = degree.Validate(); err != nil {
		return
	}
	d.degree = degree
	return
}

func (d *dummyPack) Flags() Flags {
	return d.flags
}

func (d *dummyPack) AddFlags(flags Flags) {
	d.flags |= flags
}

func (d *dummyPack) ClearFlags(flags Flags) {
	d.flags &^= flags
}

func (d *dummyPack) Has(key cipher.SHA256) (ok bool) {
	_, ok = d.vals[key]
	return
}

func testPackReg(reg *Registry) (dp *dummyPack) {
	dp = new(dummyPack)
	dp.vals = make(map[cipher.SHA256][]byte)
	dp.reg = reg
	dp.degree = 3 // use 3 for tests
	return
}

func getTestPack() (dp *dummyPack) {
	return testPackReg(testRegistry())
}

// pack that returns error

var errTest = errors.New("test error")

type errorPack struct {
	*dummyPack
}

func (e *errorPack) Get(cipher.SHA256) (_ []byte, err error) {
	err = errTest
	return
}

func (e *errorPack) Set(cipher.SHA256, []byte) (_ error) {
	return errTest
}

func (e *errorPack) Add([]byte) (_ cipher.SHA256, err error) {
	err = errTest
	return
}
