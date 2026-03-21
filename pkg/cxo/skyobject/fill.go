package skyobject

import (
	"sync"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// A Filler implements registry.Splitter interface
// and used for filling.
type Filler struct {
	c *Container
	r *registry.Root

	reg *registry.Registry

	rq chan<- cipher.SHA256

	mx   sync.Mutex
	incs map[cipher.SHA256]int
	pre  map[cipher.SHA256]struct{} // prerequested by RC

	limit chan struct{} // max

	errq chan error

	await sync.WaitGroup

	closeq chan struct{}
	closeo sync.Once
}

//
// methods of the registry.Splitter
//

// Registry of the Filler
func (f *Filler) Registry() (reg *registry.Registry) {
	return f.reg
}

func (f *Filler) get(
	key cipher.SHA256,
	inc int,
) (
	val []byte,
	rc int,
	err error,
) {

	// try to get from DB first
	val, rc, err = f.c.Get(key, inc) // incrementing the rc to hold the object

	if err == nil {
		if inc > 0 {
			rc = f.inc(key, rc) // ++
		}
		return val, rc, err
	}

	if err != data.ErrNotFound {
		fatal("DB failure:", err) // fatality
	}

	err = nil // clear if it's data.ErrNotFound

	// not found
	var gc = make(chan Object, 1) // wait for the object

	f.c.Want(key, gc, inc)    //nolint:errcheck,gosec
	defer f.c.Unwant(key, gc) // to be memory safe

	// requset the object using the rq channel
	if f.requset(key) == false { //nolint:staticcheck
		return val, rc, err
	}

	select {
	case obj := <-gc:
		if err = obj.Err; err != nil {
			return val, rc, err
		}
		val = obj.Val
		if inc > 0 {
			rc = f.inc(key, obj.RC)
		} else {
			rc = obj.RC
		}
	case <-f.closeq:
		err = ErrTerminated
	}

	return val, rc, err
}

// Pre used to prerequest an item to get it late. The Get increments
// filling rc in the Cache. And to not increment the rc twice for
// an item this method used. The method doesn't return value, because
// nobobdy need it. The Pre used to obtain "hard rc" of an item
func (f *Filler) Pre(key cipher.SHA256) (rc int, err error) {
	if _, rc, err = f.get(key, 1); err != nil {
		return
	}

	f.mx.Lock()
	defer f.mx.Unlock()

	f.pre[key] = struct{}{} // set
	return
}

func (f *Filler) isPrerequested(key cipher.SHA256) (ok bool) {
	f.mx.Lock()
	defer f.mx.Unlock()

	_, ok = f.pre[key]
	return
}

// Get object from DB or request it usung provided
// channel. The Get increments references counter
// of value
func (f *Filler) Get(key cipher.SHA256) (val []byte, rc int, err error) {

	var inc = 1

	if f.isPrerequested(key) == true { //nolint:staticcheck
		inc = 0 // prerequested
	}

	val, rc, err = f.get(key, inc)
	return
}

// Fail used to terminate the Filler with
// provided error
func (f *Filler) Fail(err error) {
	select {
	case f.errq <- err:
	case <-f.closeq:
	}
}

//
// internal methods
//

// if the item was requested by this filler,
// then we can force split* methods to skip it
// even if the item relates to this filling
// Root only (is not hard)
func (f *Filler) inc(key cipher.SHA256, drc int) (rc int) {
	f.mx.Lock()
	defer f.mx.Unlock()

	rc = drc

	var finc, ok = f.incs[key]

	if ok == true { //nolint:staticcheck
		rc++
	}

	f.incs[key] = finc + 1 // +1 want (+1 finc)
	return
}

func (f *Filler) requset(key cipher.SHA256) (ok bool) {

	select {
	case f.rq <- key:
		ok = true
	case <-f.closeq:
	}
	return
}

// Close terminates the Split walking and waits for
// goroutines the split creates
func (f *Filler) Close() {
	f.closeo.Do(func() {
		close(f.closeq)
		f.await.Wait()
	})
}

// Fill given Root returns Filler that fills given
// Root object. To request objects, the DB doesn't
// have, given rq channel used. The Fill used by
// the node package to fill Root objects. The filler
// must be closed after using
func (c *Container) Fill(
	r *registry.Root, //        : the Root to fill
	rq chan<- cipher.SHA256, // : request object from peers
	maxParall int, //           : max subtrees processing at the same time
) (
	f *Filler, //               : the Filler
) {

	f = new(Filler)

	f.c = c
	f.r = r

	f.rq = rq
	f.incs = make(map[cipher.SHA256]int)
	f.pre = make(map[cipher.SHA256]struct{})

	if maxParall > 0 {
		f.limit = make(chan struct{}, maxParall)
	}

	f.errq = make(chan error, 1)
	f.closeq = make(chan struct{})

	return
}

func (f *Filler) apply() {
	for key, inc := range f.incs {
		if err := f.c.Finc(key, inc); err != nil {
			panic("DB failure: " + err.Error()) // TODO: handle the error
		}
	}
}

func (f *Filler) reject() {
	for key, inc := range f.incs {
		if err := f.c.Finc(key, -inc); err != nil {
			panic("DB failure: " + err.Error()) // TODO: handle the error
		}
	}
}

func (f *Filler) acquire() (parall bool) {

	if f.limit == nil { // no limit
		parall = true
		return
	}

	select {
	case f.limit <- struct{}{}: // limit
		parall = true
	default:
		// limit reached
	}

	return

}

// Go performs some task dependig on parallelism.
func (f *Filler) Go(fn func()) {

	if f.acquire() == true { //nolint:staticcheck

		// parallel

		f.await.Add(1)
		go func() {
			defer f.await.Done()
			<-f.limit // release
			fn()
		}()

		return
	}

	// otherwise, in the very goroutine

	fn()

}

// Run the Filler. The Run method blocks
// until finish or first error
func (f *Filler) Run() (err error) {

	// save Root

	if _, err = f.c.Set(f.r.Hash, f.r.Encode(), 1); err != nil {
		return err
	}

	f.inc(f.r.Hash, 0) // increment

	defer func() {
		if err != nil {
			f.r.IsFull = false // reset
			f.reject()
		} else {
			f.apply()
		}
	}()

	if err = f.getRegistry(); err != nil {
		return err
	}

	for _, dr := range f.r.Refs {

		// the closure is data-race protection
		func(dr registry.Dynamic) {
			f.Go(func() { dr.Split(f) })
		}(dr)

	}

	var done = make(chan struct{})

	go func() {
		f.await.Wait() // wait group
		close(done)
	}()

	select {
	case err = <-f.errq:
	case <-done:
		f.r.IsFull = true // full!
		_, err = f.c.AddRoot(f.r)
	}

	f.Close() //nolint:errcheck,gosec

	return err
}

func (f *Filler) getRegistry() (err error) {

	if f.r.Reg == (registry.RegistryRef{}) {
		return ErrBlankRegistryRef
	}

	// incrementing
	if _, _, err = f.Get(cipher.SHA256(f.r.Reg)); err != nil {
		return
	}

	var reg *registry.Registry

	if reg, err = f.c.Registry(f.r.Reg); err != nil {
		return
	}

	f.reg = reg

	return
}
