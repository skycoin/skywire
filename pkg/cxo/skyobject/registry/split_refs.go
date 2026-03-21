package registry

import (
	"github.com/skycoin/skycoin/src/cipher"
)

// fake pack to load Refs for the
// Split method
type fakePack struct {
	s Splitter
}

func (f *fakePack) Registry() (r *Registry) {
	return f.s.Registry()
}

func (f *fakePack) Get(key cipher.SHA256) (val []byte, err error) {
	val, _, err = f.s.Get(key)
	return
}

func (*fakePack) Set(cipher.SHA256, []byte) error {
	panic("fake method called")
}

func (*fakePack) Add([]byte) (cipher.SHA256, error) {
	panic("fake method called")
}

func (*fakePack) Degree() Degree         { return MaxDegree /* any valid */ }
func (*fakePack) SetDegree(Degree) error { panic("fake method called") }
func (*fakePack) Flags() (_ Flags)       { return }
func (*fakePack) AddFlags(Flags)         { panic("fake method called") }
func (*fakePack) ClearFlags(Flags)       { panic("fake method called") }

// get and cache value, and return true
// if hard rc of value is zero
func (r *Refs) splitHash(
	fp *fakePack, //       :
	hash cipher.SHA256, // :
) (
	load bool, //          :
) {

	var rc, err = fp.s.Pre(hash)

	if err != nil {
		fp.s.Fail(err)
		return
	}

	return (rc == 0) // laod if hard rc == 0
}

// Split used by the node package to fill the Dynamic.
// Unlike the Walk the Split desigend for fresh Refs
// received by network the the Split never reset the
// Refs if it loads it and never updates hashes of
// the Refs if they are not actual
func (r *Refs) Split(s Splitter, el Schema) {

	var fp = fakePack{s} // fake Pack

	if r.Hash == (cipher.SHA256{}) {
		return // done
	}

	// first of all, check the Refs.Hash

	if r.splitHash(&fp, r.Hash) == false {
		return
	}

	// load to walk
	if err := r.initialize(&fp); err != nil {
		s.Fail(err)
		return
	}

	r.splitNode(&fp, el, r.refsNode, r.depth)

}

func (r *Refs) splitNodeAsync(
	fp *fakePack, // : fake pack to load
	sch Schema, //   : schema of elements
	rn *refsNode, // : the node
	depth int, //    : depth of the node
) {
	fp.s.Go(func() { r.splitNode(fp, sch, rn, depth) })
}

func (r *Refs) splitNode(
	fp *fakePack, // : fake pack to load
	sch Schema, //   : schema of elements
	rn *refsNode, // : the node
	depth int, //    : depth of the node
) {

	if depth == 0 {

		for _, leaf := range rn.leafs {
			splitSchemaHashAsync(fp.s, sch, leaf.Hash)
		}

		return
	}

	// else if depth > 0 -> { branches }

	var toSplit []*refsNode // data-race protection

	for _, br := range rn.branches {
		if r.splitHash(fp, br.hash) == false {
			continue
		}

		if err := r.loadNodeIfNeed(fp, br, depth-1); err != nil {
			fp.s.Fail(err)
			return
		}

		toSplit = append(toSplit, br)
	}

	// data-race protection: load first, then split

	for _, br := range toSplit {
		r.splitNodeAsync(fp, sch, br, depth-1)
	}

}
