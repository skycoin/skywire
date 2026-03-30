package registry

import (
	"fmt"

	gotree "github.com/DiSiqueira/GoTree"
	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

// A Refs represents slice of references that
// represented as Merkle-tree with some degree.
// E.g. every branch of the tree has degree-th
// subtrees (branches or leafs). And all values
// stored in leafs. The Merkle-tree is required
// to share the Refs over network, and share
// changes of the Refs easy way. The goal is
// reducing network pressure
//
// The Refs can have internal hash-table index if
// Pack that initailizes (used for first access
// the Refs) has HashTableIndex flag. All falgs
// related to the Refs stored inside the Refs
// during initialization of the Refs. The flags
// are not stored in DB and doesn't sent through
// network
//
// So, the Refs uses lazy-loading strategy by
// default (if no flags set). I.e. all branches
// of the tree are not loaded and loads by needs.
// It's possible to load entire Refs providing
// the EntireRefs flag
//
// There is a note about the degree. Since all
// blank Refs are equal, then the degree can't
// be kept if the Refs is blank. E.g. if you are
// using non-default degree and want to keep the
// degree for a while, then you have to handle
// it yourself. It's because, all blank Refs are
// not stored in DB and have blank hash. Since the
// hash is blank, then the Refs can't store anything
// in DB
//
// The Refs is not thread safe
type Refs struct {
	// Hash that represnts the Refs. If Refs is
	// blank then this Hash is blank too
	Hash cipher.SHA256

	depth  int    `enc:"-"` // depth - 1
	degree Degree `enc:"-"` // degree

	refsIndex `enc:"-"` // hash-table index
	*refsNode `enc:"-"` // leafs, branches, mods and length (pointer)

	flags Flags `enc:"-"` // first use (load) flags

	// stack of iterators, if element is true, then length of the Refs
	// has been changed and the iterator have to find next element
	// from the Root (and set next element of the iterators slice to true
	// for next iterator). This way, the Refs provides a way to
	// iterate inside another iterator, modify tree insisde an iterator,
	// etc
	iterators []bool `enc:"-"`
}

// String implements fmt.Stringer interface and
// returns hexadecimal encoded hash of the Refs
func (r *Refs) String() string {
	return r.Hash.Hex()
}

// Short string
func (r *Refs) Short() string {
	return r.Hash.Hex()[:7]
}

func (r *Refs) initialize(pack Pack) (err error) {

	if r.refsNode != nil && r.mods != 0 {
		return err // already initialized
	}

	// r.refsNode.hash is always blank
	r.refsNode = new(refsNode)

	r.mods = loadedMod     // mark as loaded
	r.flags = pack.Flags() // keep current flags

	r.degree = pack.Degree() // use default degree

	if err = r.degree.Validate(); err != nil {
		panic("invalid Degree of the Pack") // test the Pack
	}

	if r.flags&HashTableIndex != 0 {
		r.refsIndex = make(refsIndex)
	}

	if r.Hash == (cipher.SHA256{}) {
		return err // blank Refs, don't need to load
	}

	var er encodedRefs
	if err = get(pack, r.Hash, &er); err != nil {
		return err // get or decoding error
	}

	r.depth = int(er.Depth)
	r.degree = Degree(er.Degree) // overwrite from saved

	r.length = int(er.Length)

	if r.length == 0 || r.degree.Validate() != nil {
		return ErrInvalidEncodedRefs // invalid state
	}

	return r.loadSubtree(pack, r.refsNode, er.Elements, r.depth)
}

// Init does nothing, but if the Refs are not
// initialized, then the Init method initilizes
// it saving curretn flags of given pack
//
// It's possible to use some other methods to
// initialize the Refs (Len for example), but
// some methods of the Refs may not initialze
// it
//
// If the Refs is already initialized then
// it will not be initialized anymore
func (r *Refs) Init(pack Pack) (err error) {
	return r.initialize(pack)
}

// Len returns length of the Refs
func (r *Refs) Len(pack Pack) (ln int, err error) {
	if err = r.initialize(pack); err != nil {
		return
	}
	ln = r.length
	return
}

// Depth return real depth of the Refs
func (r *Refs) Depth(pack Pack) (depth int, err error) {
	if err = r.initialize(pack); err != nil {
		return
	}
	depth = r.depth + 1
	return
}

// Degree returns degree of the Refs tree
func (r *Refs) Degree(pack Pack) (degree Degree, err error) {
	if err = r.initialize(pack); err != nil {
		return
	}
	degree = r.degree
	return
}

// SetDegree rebuild the Refs with given degree
func (r *Refs) SetDegree(pack Pack, degree Degree) (err error) {

	if err = degree.Validate(); err != nil {
		return
	}

	if err = r.initialize(pack); err != nil {
		return
	}

	if r.Hash == (cipher.SHA256{}) {
		r.degree = degree
		return // it's enough
	}

	// replace this Refs with new one with new degree
	var nr = newRefs(degree, r.flags, r.length)

	if err = nr.Append(pack, r); err != nil {
		return
	}

	nr.iterators = r.iterators // copy iterators

	*r = *nr

	return
}

// Flags returns current flags of the Refs.
// If the Refs is not initialized it returns
// zero. The method deosn't initializes
// Refs and can be used (e.g. is useful) with
// initialized Refs only. The Frags are flags
// with which the Refs has been initialized
// before. You can check HashTableIndex flag
// and other
//
//	if refs.Flags()&registry.HashTableIndex != 0 {
//	    // hash-table index used
//	} else {
//	    // no hash-table index
//	}
func (r *Refs) Flags() (flags Flags) {
	return r.flags
}

// an encodedRefs represents encoded Refs
// that contains length, degree, depth and
// hashes of nested elements
type encodedRefs struct {
	Depth    uint32
	Degree   uint32
	Length   uint32
	Elements []cipher.SHA256
}

// Reset the Refs. If the Refs can't be loaded
// or the Refs is broken, then it can't be used
// anymore. To reload the Refs to make it useful
// use this method. The Reset method doesn't reloads
// the Refs, but allows reloading for other methods.
// It's impossible to reset during an iteration.
// The Reset resets flags of the Refs too
func (r *Refs) Reset() (err error) {

	if len(r.iterators) > 0 {
		return ErrRefsIterating // can't reset during iterating
	}

	var hash = r.Hash // }
	*r = Refs{}       // }reset
	r.Hash = hash     // }

	return
}

// 1. only 'hash' of refsNode has meaning
// 2. refsNode loaded
// 3. refsNode loaded with all subtrees

// HasHash returns false if the Refs doesn't have given hash. It returns
// true if contains, or first error. It never returns ErrNotFound
//
// The big O of the call is O(1) if the HashTableIndex flag has been set.
// Otherwise, the big O is O(n), where n is real length of the Refs
func (r *Refs) HasHash(
	pack Pack, //          : pack to load (if not loaded yet)
	hash cipher.SHA256, // : hash to check out
) (
	ok bool, //            : true if has got
	err error, //          : error if any
) {

	if err = r.initialize(pack); err != nil {
		return
	}

	if r.flags&HashTableIndex != 0 {
		_, ok = r.refsIndex[hash]
		return
	}

	// else -> iterate ascending

	err = r.Ascend(pack, func(_ int, elHash cipher.SHA256) (err error) {
		if elHash == hash {
			ok = true
			return ErrStopIteration
		}
		return // continue
	})

	return
}

// ValueByHash decodes value by given hash. If HashTableIndex
// is set, then hash table used. If given hash is blank then
// the ValueByHash returns ErrRefsElementIsNil if the Refs
// contans blank element or elements. The ValueByHash returns
// ErrNotFound if the Refs doesn't contain an element with given
// hash. If the HashTableIndex flag has been set, then the
// hash-table used to check presence.
//
// The big O of the call is the same as the big O of the
// HasHash call
func (r *Refs) ValueByHash(
	pack Pack, //          : pack to laod
	hash cipher.SHA256, // : hash to get
	obj interface{}, //    : pointer to object to decode
) (
	err error, //          : error if any
) {

	// initilize() inside the HashHash

	var ok bool
	if ok, err = r.HasHash(pack, hash); err != nil {
		return
	}
	if !ok {
		return ErrNotFound
	}
	if hash == (cipher.SHA256{}) {
		return ErrRefsElementIsNil
	}

	err = get(pack, hash, obj)
	return
}

func (r *Refs) indexOfHashByHashTable(hash cipher.SHA256) (i int, err error) {

	if res, ok := r.refsIndex[hash]; ok {
		return res[len(res)-1].indexInRefs()
	}

	err = ErrNotFound
	return
}

// IndexOfHash returns index of element by hash. It returns ErrNotfound
// if the Refs doesn't have given hash. If HashTableIndex flag is set then
// and the Refs contains many elements with given hash, then which of them
// will be returned, is undefined
//
// The big O of the call is O(1) if the Refs doesn't contain element(s)
// with given hash, or O(depth) if there is at least one lement with
// given hash. Where the depth is depth (height) of the Refs tree
//
// But if HashTableIndex flag is not set, then the big O is O(n)
func (r *Refs) IndexOfHash(
	pack Pack, //          : pack to load
	hash cipher.SHA256, // : hsah to find
) (
	i int, //              : index of the element
	err error, //          : error if any
) {

	if err = r.initialize(pack); err != nil {
		return i, err
	}

	if r.flags&HashTableIndex != 0 {
		return r.indexOfHashByHashTable(hash)
	}

	// else -> iterate ascending

	var found bool

	err = r.Ascend(pack, func(index int, elHash cipher.SHA256) (err error) {
		if elHash == hash {
			found = true
			i = index
			return ErrStopIteration // break
		}
		return // continue
	})

	if err == nil && found == false { //nolint:staticcheck
		err = ErrNotFound
	}

	return i, err
}

// indicesOfHashUsingHashTable finds indices of all elements by given hash
func (r *Refs) indicesOfHashUsingHashTable(
	hash cipher.SHA256, // : hash to find
) (
	is []int, //           : indices of elements
	err error, //          : error if any
) {

	var i int

	if res, ok := r.refsIndex[hash]; ok {
		is = make([]int, 0, len(res))
		for _, re := range res {
			if i, err = re.indexInRefs(); err != nil {
				return
			}
			is = append(is, i)
		}
		return
	}

	err = ErrNotFound
	return
}

// IndicesByHash returns indices of all elements with given hash.
// It returns ErrNotFound if the Refs doesn't contain such elements.
// Order of the indices, the IndicesByHash returns, is undefined.
//
// The big O of the call is O(m * depth), where m is number of
// elements with given hash in the Refs if HashTableInex flag used
// by the Refs. Otherwise, the big O is O(n), where n is real length
// of the Refs
//
// But if HashTableIndex flag is not set, then the big O is O(n)
func (r *Refs) IndicesByHash(
	pack Pack, //          : pack to load
	hash cipher.SHA256, // : hash to find
) (
	is []int, //           : indices
	err error, //          : error if any
) {

	if err = r.initialize(pack); err != nil {
		return
	}

	if r.flags&HashTableIndex != 0 {
		return r.indicesOfHashUsingHashTable(hash)
	}

	// else -> iterate ascending

	err = r.Ascend(pack, func(index int, elHash cipher.SHA256) (err error) {
		if elHash == hash {
			is = append(is, index)
		}
		return // continue
	})

	if err == nil && len(is) == 0 {
		err = ErrNotFound
	}

	return
}

// ValueOfHashWithIndex decodes value by given hash
// and returns index of the value. It returns actual
// index and ErrRefsElementIsNil if given hash is
// blank but exists in the Refs. The ValueOfHashWithIndex
// method is usablility wrapper over the IndexOfHash with
// related notes, the big O and limitations
func (r *Refs) ValueOfHashWithIndex(
	pack Pack, //          : pack to laod
	hash cipher.SHA256, // : hash to find
	obj interface{}, //    : pointer to object to decode
) (
	i int, //              : index of the element if found
	err error, //          : error if any
) {

	// initialize() inside the IndexOfHash

	if i, err = r.IndexOfHash(pack, hash); err != nil {
		return
	}

	if hash == (cipher.SHA256{}) {
		err = ErrRefsElementIsNil // can't get and decode "nil"
		return
	}

	err = get(pack, hash, obj) // get and decode
	return
}

func validateIndex(i int, length int) (err error) {
	if i < 0 || i >= length {
		err = ErrIndexOutOfRange
	}
	return
}

// HashByIndex returns hash by index
//
// The big O of the call is O(depth)
func (r *Refs) HashByIndex(
	pack Pack, //          : pack to load
	i int, //              : index to find
) (
	hash cipher.SHA256, // : hash of the element if found
	err error, //          : error if any
) {

	if err = r.initialize(pack); err != nil {
		return
	}

	if err = validateIndex(i, r.length); err != nil {
		return
	}

	var el *refsElement
	if el, err = r.elementByIndex(pack, r.refsNode, i, r.depth); err != nil {
		return
	}

	return el.Hash, nil
}

// ValueByIndex returns value by index or ErrNotFound
// or another error. It also returns hash of the value.
// The ValueByIndex returns ErrRefsElementIsNil if element
// has been found but represents nil (blank hash)
func (r *Refs) ValueByIndex(
	pack Pack, //          : pack to load
	i int, //              : index to find
	obj interface{}, //    : pointer to object to decode
) (
	hash cipher.SHA256, // : hash of the element
	err error, //          : error if any
) {

	// initialize() inside the HashByIndex

	if hash, err = r.HashByIndex(pack, i); err != nil {
		return
	}

	if hash == (cipher.SHA256{}) {
		err = ErrRefsElementIsNil
		return
	}

	err = get(pack, hash, obj)
	return
}

//
// encode
//

// encode as is
func (r *Refs) encode() []byte {
	var er encodedRefs

	er.Degree = uint32(r.degree) //nolint:gosec
	er.Depth = uint32(r.depth)   //nolint:gosec
	er.Length = uint32(r.length) //nolint:gosec

	if r.depth == 0 {

		er.Elements = make([]cipher.SHA256, 0, len(r.leafs))
		for _, el := range r.leafs {
			er.Elements = append(er.Elements, el.Hash)
		}

	} else {

		er.Elements = make([]cipher.SHA256, 0, len(r.branches))
		for _, br := range r.branches {
			er.Elements = append(er.Elements, br.hash)
		}

	}

	return encoder.Serialize(er)
}

//
// change element hash
//

// updateHashIfNeed if the need argument is true,
// otherwise set Refs.mods contentMod flag and return
func (r *Refs) updateHashIfNeed(pack Pack, need bool) (err error) {

	if need == false { //nolint:staticcheck
		r.mods |= contentMod // but not saved
		return
	}

	return r.updateHash(pack)
}

// updateHash in all cases, it clears Refs.mods
// contentMod flag and sets originMod flag
func (r *Refs) updateHash(pack Pack) (err error) {

	if r.length == 0 {
		r.Hash = cipher.SHA256{}       // blank hash is blank Refs
		r.mods &^= contentMod          // clear the flag
		r.mods |= originMod            // the Refs has been changed
		r.leafs, r.branches = nil, nil // clear
		return
	}

	val := r.encode()
	hash := cipher.SumSHA256(val)

	if r.Hash != hash {

		if err = pack.Set(hash, val); err != nil {
			return // saving error
		}

		r.Hash = hash

	}

	r.mods &^= contentMod // clear the flag if set
	r.mods |= originMod   // origin has been modified

	return
}

// SetHashByIndex replaces hash of element with given index with
// given hash
//
// The big O of the call is O(depth)
func (r *Refs) SetHashByIndex(
	pack Pack, //          : pack to load and save
	i int, //              : index to set
	hash cipher.SHA256, // : new hash
) (
	err error, //          : error if any
) {

	if err = r.initialize(pack); err != nil {
		return
	}

	if err = validateIndex(i, r.length); err != nil {
		return
	}

	var el *refsElement
	if el, err = r.elementByIndex(pack, r.refsNode, i, r.depth); err != nil {
		return
	}

	return r.setElementHash(pack, el, hash)
}

// SetValueByIndex saves given value calculating its hash and sets this
// hash to given index. You must be sure that schema of given element is
// schema of the Refs. Otherwise, Refs will be broken. Use nil to set
// blank hash
func (r *Refs) SetValueByIndex(
	pack Pack, //       : pack to load and save
	i int, //           : index to find
	obj interface{}, // : object to save
) (
	err error, //       : error if any
) {

	// initialize() inside the SetHashByIndex

	var hash cipher.SHA256

	if isNil(obj) == false { //nolint:staticcheck
		if hash, err = pack.Add(encoder.Serialize(obj)); err != nil {
			return
		}
	}

	return r.SetHashByIndex(pack, i, hash)
}

//
// delete
//

// if a deleting performed inside iterators, then we should
// notify them, that they have to find next index from Root,
// because the Refs structure has been changed
func (r *Refs) rewindIterators() {
	if li := len(r.iterators); li > 0 {
		r.iterators[li-1] = true // mark
	}
}

// DeleteByIndex deletes single element from
// the Refs changing the Refs. E.g. the method
// removes element shifting elements after.
//
// The big O of the call is O(depth * 2)
func (r *Refs) DeleteByIndex(
	pack Pack, // : pack to load and save
	i int, //     : index to delete
) (
	err error, // : error if any
) {

	if err = r.initialize(pack); err != nil {
		return
	}

	if err = validateIndex(i, r.length); err != nil {
		return
	}

	if err = r.deleteElementByIndex(pack, r.refsNode, i, r.depth); err != nil {
		return // an error
	}

	if err = r.updateHashIfNeed(pack, r.flags&LazyUpdating == 0); err != nil {
		return // an error
	}

	r.rewindIterators() // for iterators
	return
}

// deleteByHashUsingHashTable deletes all elements with given
// hash using hash-table index
func (r *Refs) deleteByHashUsingHashTable(
	pack Pack, //          : pack to save
	hash cipher.SHA256, // : hash to delete
) (
	err error, //          : error if any
) {

	var ok bool
	var els []*refsElement

	if els, ok = r.refsIndex[hash]; !ok {
		return ErrNotFound // nothing to delete
	}

	// if the Refs contains one element with the hash,
	// then we update subtree in place, otherwise
	// we call walkUpdating in the end

	var one = len(els) == 1 // only one element

	delete(r.refsIndex, hash) // remove all from the index

	for _, el := range els {
		if err = r.deleteElement(pack, el, one); err != nil {
			return err // error
		}
	}

	// if the one is true, then everything is up to date
	// and we have to update the Refs (the root) only;
	// otherwise, we update all modified subtrees

	// if not lazy
	if r.flags&LazyUpdating == 0 {
		if one == true { //nolint:staticcheck
			err = r.updateHash(pack)
		} else {
			err = r.walkUpdating(pack)
		}
		if err != nil {
			return err
		}
	}

	r.rewindIterators() // for iterators
	return err
}

// DeleteByHash deletes all elements by given hash, reducing length of the
// Refs. The method returns ErrNotfound if the Refs doesn't contain such
// elements
//
// The big O of the call is O(m * depth), where m is number of elements with
// given hash, if HashTableIndex flag has been set. If the flag is not used
// by the Refs, then the big O is O(n), where n is real length of the Refs
func (r *Refs) DeleteByHash(
	pack Pack, //          : pack to load and save
	hash cipher.SHA256, // : hash of element to delete
) (
	err error, //          : error if any
) {

	if err = r.initialize(pack); err != nil {
		return err
	}

	if r.flags&HashTableIndex != 0 {
		return r.deleteByHashUsingHashTable(pack, hash)
	}

	// A combined descend+delete method would avoid double traversal.
	//                   that joins descending and deleting

	// else -> iterate descending

	// https://play.golang.org/p/5tvrkq5692

	var deleted bool // at least one

	err = r.Descend(pack, func(i int, elHash cipher.SHA256) (err error) {
		if elHash == hash {
			deleted = true
			return r.DeleteByIndex(pack, i) // continue
		}
		return // continue
	})

	if err == nil && deleted == false { //nolint:staticcheck
		err = ErrNotFound
	}

	return err
}

// DeleteSliceByRange deletes a contiguous range [from, to) from the Refs,
// similar to Go's slice[from:to]. Indices must satisfy 0 <= from < to <= Len().
// Elements are deleted in reverse order to avoid index shifting, and hash
// updates are deferred until all deletions complete.
func (r *Refs) DeleteSliceByRange(
	pack Pack, // : pack to load and save
	from int, //  : start index (inclusive)
	to int, //    : end index (exclusive)
) (
	err error, // : error if any
) {

	if err = r.initialize(pack); err != nil {
		return err
	}

	if from < 0 || to < 0 || from >= to || to > r.length {
		return fmt.Errorf("invalid range [%d:%d) for Refs of length %d", from, to, r.length)
	}

	// Delete in reverse order so indices remain valid after each deletion.
	// Use deleteElementByIndex directly to avoid per-element hash updates.
	for i := to - 1; i >= from; i-- {
		if err = r.deleteElementByIndex(pack, r.refsNode, i, r.depth); err != nil {
			return err
		}
	}

	// Single hash update after all deletions
	if err = r.updateHashIfNeed(pack, r.flags&LazyUpdating == 0); err != nil {
		return err
	}

	r.rewindIterators()
	return nil
}

// startIteration allows to track modifications
// inside an iteration
func (r *Refs) startIteration() {
	r.iterators = append(r.iterators, false)
}

// stopIteration pops some service information
// pushed by the Refs.startIteration; the
// stopIteration method should be called using
// defer statement
func (r *Refs) stopIteration() {
	r.iterators = r.iterators[:len(r.iterators)-1]
}

// isFindingIteratorsIndexFromRootRequired used by an iterator
// for every element after user-prvided function call; if the
// Refs has been modified by the function, then this method
// return true (only if length of the Refs has been changed);
// the true forces an iterator to find next element from root;
// see also Refs.rewindIterators method
func (r *Refs) isFindingIteratorsIndexFromRootRequired() (yep bool) {
	if r.iterators[len(r.iterators)-1] == true { //nolint:staticcheck
		if len(r.iterators) > 1 {
			r.iterators[len(r.iterators)-2] = true // pass to next
		}
		r.iterators[len(r.iterators)-1] = false // reset current
		yep = true                              // rewind
	}
	return // nop
}

// An IterateFunc represents function for iterating over
// the Refs ascending or desceidig order. It receives
// index of element and hash of the element. Use
// ErrStopIteration to break an iteration. The
// ErrStopIteration will be hidden, but any other error
// returned by this function will be returned by iterator
type IterateFunc func(i int, hash cipher.SHA256) (err error)

// ascendFrom iterates elemnets of the Refs starting from
// the element with given index
func (r *Refs) ascendFrom(
	pack Pack, //              : pack to load
	from int, //               : start from here
	ascendFunc IterateFunc, // : the function
) (
	err error, //              : errro if any
) {

	if err = r.initialize(pack); err != nil {
		return err
	}

	if from == 0 && r.length == 0 {
		return err // nothing to iterate
	}

	if err = validateIndex(from, r.length); err != nil {
		return err
	}

	if r.length == 0 {
		return err // empty Refs
	}

	r.startIteration()
	defer r.stopIteration()

	var rewind bool // find next element from root of the Refs
	var i = from    // i is next index to find
	var pass int    // passed items by current step

	for {

		// - pass is number of elements iterated (not skipped)
		//        for current step
		// - rewind means that we need to find next element from
		//        root of the Refs, because the Refs has been
		//        changed
		// - err is loading error, malformed Refs error or
		//        ErrStopIteration
		pass, rewind, err = r.ascendNode(pack, r.refsNode, r.depth, 0, i,
			ascendFunc)

		// so, we need to find next element from root of the Refs
		if rewind == true { //nolint:staticcheck
			i += pass // shift the i to don't repeat elements
			continue  // find next index from root of the Refs
		}

		// the loop is necessary for rewinding only
		break

		// if the rewind is true, then the err is nil;
		// thus we can defer the err check
	}

	if err == ErrStopIteration {
		err = nil // clear the ErrStopIteration
	}

	return err
}

// Ascend iterates over all values ascending order until
// first error or until the end. Use ErrStopIteration to
// break the iteration. Any error is returned by given function
// (except the ErrStopIteration) is returned by the Ascend()
// method
//
// The Ascend allows to cahnge the Refs inside given
// ascendFunc. Be careful, this way you can start an infinity
// Ascend (for example, if the ascenFunc appends something to
// the Refs every itteration). It's possile to iterate
// inside the ascendFunc too
func (r *Refs) Ascend(
	pack Pack, //              : pack to load
	ascendFunc IterateFunc, // : user-provided function
) (
	err error, //              : error if any
) {
	return r.ascendFrom(pack, 0, ascendFunc)
}

// AscendFrom iterates over all values ascending order starting
// from the 'from' element until first error or the end. Use
// ErrStopIteration to break iteration. Any error is returned
// by given function (except the ErrStopIteration) is returned
// by the AscendFrom() method
func (r *Refs) AscendFrom(
	pack Pack, //              : pack to load
	from int, //               : starting index
	ascendFunc IterateFunc, // : the function
) (
	err error, //              : error if any
) {

	return r.ascendFrom(pack, from, ascendFunc)
}

// descendFrom iterates elements descending
// starting from element with given index
func (r *Refs) descendFrom(
	pack Pack, //               : pack to load
	from int, //                : index to start from
	descendFunc IterateFunc, // : the function
) (
	err error, //               : error if any
) {

	// the Refs are already initialized by Descend or DescendFrom methods

	if err = validateIndex(from, r.length); err != nil {
		return err
	}

	if r.length == 0 {
		return err // empty Refs
	}

	r.startIteration()
	defer r.stopIteration()

	var rewind bool          // find next element from root of the Refs
	var i = from             // i is next index to find
	var shift = r.length - 1 // subtree ending index (shift from the end)
	var pass int             // passed items by current step

	for {
		// - pass is number of elements iterated (not skipped)
		//        for current step
		// - rewind means that we need to find next element from
		//        root of the Refs, because the Refs has been
		//        changed
		// - err is loading error, malformed Refs error or
		//        ErrStopIteration
		pass, rewind, err = r.descendNode(pack, r.refsNode, r.depth, shift, i,
			descendFunc)

		// so, we need to find next element from root of the Refs
		if rewind == true { //nolint:staticcheck
			i -= pass // shift the i to don't repeat elements

			// if the length has been changed
			if i >= r.length {
				return ErrIndexOutOfRange // out
			}
			shift = r.length - 1 // set shift

			continue // find next index from root of the Refs
		}

		// the loop is necessary for rewinding only
		break
	}

	if err == ErrStopIteration {
		err = nil // clear the ErrStopIteration
	}

	return err

}

// Descend iterates over all values descending order until
// first error or the end. Use ErrStopIteration to
// break iteration. Any error is returned by given function
// (except the ErrStopIteration) is returned by the Descend()
// method
//
// The Descend allows to cahnge the Refs inside given
// ascendFunc. It's possile to iterate inside the descendFunc
// too
func (r *Refs) Descend(
	pack Pack, //               : pack to load
	descendFunc IterateFunc, // : the function
) (
	err error, //               : error if any
) {

	// we have to initialize the Refs here to get actual length of
	// the Refs to use it as argument to the descendFrom method

	if err = r.initialize(pack); err != nil {
		return
	}

	if r.length == 0 {
		return // otherwise, r.length-1 causes ErrIndexOutOfRange error
	}

	return r.descendFrom(pack, r.length-1, descendFunc)
}

// DescendFrom iterates over all values descending order starting
// from the 'from' element until first error or the end. Use
// ErrStopIteration to break iteration. Any error is returned
// by given function (except the ErrStopIteration) is returned
// by the DescendFrom() method
func (r *Refs) DescendFrom(
	pack Pack, //               : pack to load
	from int, //                : index of element to start from
	descendFunc IterateFunc, // : the function
) (
	err error, //               : error if any
) {

	// we have to initialize the Refs here to find its length

	if err = r.initialize(pack); err != nil {
		return
	}

	return r.descendFrom(pack, from, descendFunc)
}

// validateSliceIndices validates [i:j]
// indices for the Refs with given length
func validateSliceIndices(i, j, length int) (err error) {
	if i < 0 || j < 0 || i > length || j > length {
		err = ErrIndexOutOfRange
	} else if i > j {
		err = ErrInvalidSliceIndex
	}
	return
}

// pow is interger power (a**b)
func pow(a, b int) (p int) {
	p = 1
	for b > 0 {
		if b&1 != 0 {
			p *= a
		}
		b >>= 1
		a *= a
	}
	return p
}

// dpethToFit returns depth; the Refs with this depth
// can fit the 'fit' elements
//
// since, max number of elements, that the Refs can fit,
// is pow(r.degree, r.depth+1), then required depth is
// the base r.degree logarithm of the fit; but since we
// need interger number, we are using loop to find the
// number
//
// so, both the start argumen and the reply of the function
// are Refs.depth. E.g. Refs.depth = 'real depth'-1
func depthToFit(
	degree Degree, // : degree of the Refs
	start int, //     : starting depth
	fit int, //       : number of elements to fit
) (
	depth int, //     : the needle
) {

	if fit <= int(degree) {
		return 0
	}

	if start == 0 {
		start++
	}

	// the start is 'real depth', but we need
	// Refs' depth that is 'real depth' - 1
	for ; pow(int(degree), start) < fit; start++ {
	}

	return start - 1 // found (-1 turns the start to be Refs.depth)
}

// appnedCreatingSliceFunc returns IterateFunc that creates slice
// (the r) from elements of origin. Short words:
//
//	refs.AscendFrom(pack, i, slice.appnedCreatingSliceFunc(j))
//
// where i and j are slice indices [i:j]
func (r *Refs) appnedCreatingSliceFunc(j int) (iter IterateFunc) {

	// in the IterateFunc we are tracking current node and
	// depth of the current node to avoid unnecessary walking
	// from root from every element

	var cn = r.refsNode // current node
	var depth = r.depth // depth of the cn

	// we are using r, j, depth and the cn inside the IterateFunc below

	iter = func(i int, hash cipher.SHA256) (err error) {
		if i == j {
			return ErrStopIteration // we are done
		}

		// the call will panic if the slice (the r) is invalid
		cn, depth = r.appendCreatingSliceNode(cn, depth, hash)

		return // continue
	}

	return

}

// walkUpdatingSlice walks through the refs
// setting hash and length fields of nodes
// and mark them as loaded
func (r *Refs) walkUpdatingSlice(
	pack Pack, //    : pack to save
) (
	err error, //    : error if any
) {

	err = r.walkUpdatingSliceNode(pack, r.refsNode, r.depth)

	if err != nil {
		return
	}

	return r.updateHashIfNeed(pack, true)
}

// create new Refs using given degree, flags and amount of elements the
// new Refs can fit. The fit argument used to set depth
func newRefs(
	degree Degree, //  : degree of the new Refs
	flags Flags, //    : flags of the new Refs
	fit int, //        : fit elements
) (
	nr *Refs, //       : the new Refs
) {

	nr = new(Refs)

	nr.degree = degree
	nr.flags = flags

	if fit > int(degree) {
		nr.depth = depthToFit(degree, 0, fit) // depth > 0
	}

	if flags&HashTableIndex != 0 {
		nr.refsIndex = make(refsIndex)
	}

	nr.refsNode = new(refsNode)
	nr.mods |= loadedMod

	return
}

// Slice returns new Refs that contains values of this Refs from
// given i (inclusive) to given j (exclusive). If i and j are valid
// and equal, then the Slcie return new empty Refs.
// The slice will have the same flags and the same degree. The
// method returns pointer to the Refs, don't forget dereference
// the pointer to place the slice
func (r *Refs) Slice(
	pack Pack, //   : pack to load
	i int, //       : start of the range (inclusive)
	j int, //       : end of the range (exclusive)
) (
	slice *Refs, // : wanted slice
	err error, //   : error if any
) {

	// https://play.golang.org/p/4tP7_MuCN9

	if err = r.initialize(pack); err != nil {
		return slice, err
	}

	if err = validateSliceIndices(i, j, r.length); err != nil {
		return slice, err
	}

	var ln = j - i                         // length of the new slice
	slice = newRefs(r.degree, r.flags, ln) // create

	if ln == 0 {
		return slice, err // done (new blank Refs has been created)
	}

	err = r.AscendFrom(pack, i, slice.appnedCreatingSliceFunc(j))

	if err != nil {
		slice = nil // for GC
		return slice, err
	}

	// the slice contains all necessary elements, but
	// length and hash fields are empty

	if err = slice.walkUpdatingSlice(pack); err != nil {
		slice = nil // GC
		return slice, err
	}

	return slice, err // done
}

// hasEnoughFreeSpaceOnTail look for tail
// of the Refs and returns true if the Refs
// can fit given number of elements without
// increasing depth
func (r *Refs) hasEnoughFreeSpaceOnTail(
	pack Pack, // : pack to load
	fit int, //   : number of elements to fit
) (
	ok bool, //   : can fit if true
	err error, // : error if any
) {

	var fsotn int
	fsotn, err = r.freeSpaceOnTailNode(pack, r.refsNode, r.depth, fit)

	if err != nil {
		return
	}

	ok = fsotn >= fit
	return

}

type appendPoint struct {
	rn       *refsNode // current node
	depth    int       // depth of the rn
	increase int       // length diff for current subtree
}

// appendFunc returns an IterateFunc that appends
// given hashes to the Refs; the appendFunc also
// returns fini functions that used to finialize
// the appending; short words
//
//	var af, fini = dstRefs.appendFunc(pack)
//	if err = srcRefs.Ascend(pack, af); err != nil {
//	     // handle the err
//	}
//	if err = fini(); err != nil {
//	    // handle the err
//	}
//	// elements of the srcRefs appended to the dstRefs
func (r *Refs) appnedFunc(
	pack Pack, //               : pack to save new nodes
) (
	iter IterateFunc, //        : append iterating
	fini func() (err error), // : finialize
) {

	// in the IterateFunc we are tracking current node and
	// depth of the current node to avoid unnecessary walking
	// from root from every element

	// append point

	var ap = appendPoint{
		rn:       r.refsNode,
		depth:    r.depth,
		increase: 0,
	}

	iter = func(_ int, hash cipher.SHA256) (err error) {

		// the call will panic if the r (the destination) is invalid
		err = r.appendNode(pack, &ap, hash)

		return // continue
	}

	fini = func() (err error) {

		if ap.increase == 0 {
			return // nothing to finialize
		}

		// ap.increase > 0, ap.rn contains leafs

		for ; ap.rn != nil; ap.rn, ap.depth = ap.rn.upper, ap.depth+1 {

			if ap.depth > 0 {
				ap.rn.length += ap.increase
			}

			if err = ap.rn.updateHashIfNeed(pack, ap.depth, true); err != nil {
				return // saving error
			}

		}

		// LazyUpdating: defer tree rebalance until commit.

		err = r.updateHashIfNeed(pack, true)
		return err

	}

	return iter, fini
}

// walkUpdating walks through the refs
// setting hash fields of nodes to
// actual values if a node is changed
// and contentMod flag of the node is
// set
func (r *Refs) walkUpdating(
	pack Pack, //    : pack to save
) (
	err error, //    : error if any
) {

	err = r.walkUpdatingNode(pack, r.refsNode, r.depth)

	if err != nil {
		return
	}

	return r.updateHashIfNeed(pack, true)
}

// Append another Refs to this one. This Refs
// will be increased if it can't fit all new
// elements
//
// Append optimization: batch insert without per-element rebalance.
// to append the Refs itself is not implemented
// yet. Behavior in this case is undefined
func (r *Refs) Append(
	pack Pack, //  : pack to load and save
	refs *Refs, // : the Refs to append to current one
) (
	err error, //  : error if any
) {

	// init

	if err = r.initialize(pack); err != nil {
		return err
	}

	if err = refs.initialize(pack); err != nil {
		return err
	}

	if refs.length == 0 {
		return err // short curcit if the refs is blank
	}

	// ok, let's find free space on tail of this Refs (r)

	var canFit bool
	if canFit, err = r.hasEnoughFreeSpaceOnTail(pack, refs.length); err != nil {
		return err // loading error
	}

	// So, can this Refs fit new elements without rebuilding?

	if canFit == false { //nolint:staticcheck // have to be rebuilt

		// we have to rebuild this Refs increasing its depth to fit new elements

		// 1) create new Refs
		// 2) copy existing Refs to new one
		// 3) copy given Refs to new one

		// so, here we are using copy+copy to avoid unnecessary
		// hash calculating

		// (1) create
		var nr = newRefs(r.degree, r.flags, refs.length+r.length)

		// just the the j to bigger then length of the r and of the refs
		var acsf = nr.appnedCreatingSliceFunc(
			max(r.length, refs.length),
		)

		// (2) copy
		if err = r.Ascend(pack, acsf); err != nil {
			return err // error
		}

		// (3) copy
		if err = refs.Ascend(pack, acsf); err != nil {
			return err // error
		}

		// set length and hash fields and laodedMod flag
		if err = nr.walkUpdatingSlice(pack); err != nil {
			return err // error
		}

		*r = *nr // replace this Refs with new extended

		return err // done

	}

	// ok, here we have enough place to fit all new elements

	var af, fini = r.appnedFunc(pack)

	if err = refs.Ascend(pack, af); err != nil {
		return err // error
	}

	if err = fini(); err != nil {
		return err
	}

	return err // done

}

// AppendValues to this Refs. The values msut
// be schema of the Refs. There are no internal
// checks for the schema. Use nil for blank hash
func (r *Refs) AppendValues(
	pack Pack, //             : pack to load and save
	values ...interface{}, // : values to append
) (
	err error, //             : error if any
) {

	if len(values) == 0 {
		return err // short curcit (nothing to append)
	}

	var (
		hashes = make([]cipher.SHA256, 0, len(values)) //
		hash   cipher.SHA256                           // current
	)

	for _, val := range values {

		if isNil(val) == true { //nolint:staticcheck

			hash = cipher.SHA256{}

		} else {

			if hash, err = pack.Add(encoder.Serialize(val)); err != nil {
				return err
			}

		}

		hashes = append(hashes, hash)

	}

	return r.AppendHashes(pack, hashes...)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// AppendHashes to this Refs. The hashes msut
// point to objects of schema of the Refs.
// There are no internal checks for the Schema
func (r *Refs) AppendHashes(
	pack Pack, //               : pack to load and save
	hashes ...cipher.SHA256, // : hashes to append
) (
	err error, //               : error if any
) {

	if len(hashes) == 0 {
		return err // short curcit (nothing to append)
	}

	if err = r.initialize(pack); err != nil {
		return err // ititialization failed
	}

	// ok, let's find free space on tail of this Refs (r)

	var canFit bool
	if canFit, err = r.hasEnoughFreeSpaceOnTail(pack, len(hashes)); err != nil {
		return err // loading error
	}

	// So, can this Refs fit new elements without rebuilding?

	if canFit == false { //nolint:staticcheck // have to be rebuilt

		// we have to rebuild this Refs increasing its depth to fit new elements

		// 1) create new Refs
		// 2) copy existing Refs to new one
		// 3) copy given hashes to the new Refs

		// so, here we are using copy+copy to avoid unnecessary
		// hash calculating

		// (1) create
		var nr = newRefs(r.degree, r.flags, len(hashes)+r.length)

		// actually argument will not be used,
		// we set the max to be sure that all
		// elements will be proceeded
		var acsf = nr.appnedCreatingSliceFunc(
			max(r.length, len(hashes)),
		)

		// (2) copy
		if err = r.Ascend(pack, acsf); err != nil {
			return err // error
		}

		// (3) copy
		for i, hash := range hashes {

			if err = acsf(i, hash); err != nil {
				return err
			}

		}

		// set length and hash fields and laodedMod flag
		if err = nr.walkUpdatingSlice(pack); err != nil {
			return err // error
		}

		nr.iterators = r.iterators // copy iterators
		*r = *nr                   // replace this Refs with new extended

		return err // done

	}

	// ok, here we have enough place to fit all new elements

	var af, fini = r.appnedFunc(pack) // the func

	for i, hash := range hashes {

		if err = af(i, hash); err != nil {
			return err
		}

	}

	if err = fini(); err != nil {
		return err // error
	}

	return err // done

}

// Clear the Refs making it blank
func (r *Refs) Clear() {
	*r = Refs{}
}

// Rebuild the Refs if need. The Refs can contain
// unsaved changes (depending on flags) and this
// method saves this changes. Also, the Rebuild
// can reduce depth of the Refs if it's possible.
//
// The Rebuild can't be called insisde an iterator.
// It returns ErrRefsIterating in this case.
//
// If LazyUpdatingFlag is set, then you have to
// call the Rebuild to make the hash actual.
//
// The Rebuild does nothing if the Refs in actual
// state and its depth can't be reduced
func (r *Refs) Rebuild(
	pack Pack, // : pack to load and save
) (
	err error, // : error if any
) {

	if len(r.iterators) > 0 {
		return ErrRefsIterating
	}

	if err = r.initialize(pack); err != nil {
		return err
	}

	// Origin modification tracking not implemented.

	// can we reduce depth of the Refs?

	// Algorithm could be optimized for large refs collections.
	var dif = depthToFit(r.degree, 0, r.length)

	if dif != r.depth {
		// the Slice includes walkUpdating steps
		var slice *Refs
		if slice, err = r.Slice(pack, 0, r.length); err != nil {
			return err
		}
		*r = *slice // replace
	} else {
		err = r.walkUpdating(pack)
	}

	return err
}

// Tree returns string that represents the Refs tree.
// By default, it doesn't load branhces that is not
// loaded yet. But the forceLoad argument forces the
// Tree method to load them using given pack. So, if
// the forceLaod argument is false, then the pack
// argument is not used. And if the Refs is not loaded
// then the Tree method prints only hash
func (r *Refs) Tree(
	pack Pack, //      : pack to load
	forceLoad bool, // : load unloaded branches
) (
	tree string, //    : the tree
	err error, //      : loading error
) {

	if forceLoad == true { //nolint:staticcheck
		if err = r.initialize(pack); err != nil {
			return tree, err
		}
	}

	var gtName string
	gtName = "[](refs) " + r.Short()

	if forceLoad == true || (r.refsNode != nil && r.mods&loadedMod != 0) { //nolint:staticcheck
		gtName += fmt.Sprintf(" length: %d, degree: %d, depth: %d",
			r.length, r.degree, r.depth)
	} else {
		gtName += " (not loaded)"
	}

	gt := gotree.New(gtName)

	if forceLoad == true || (r.refsNode != nil && r.mods&loadedMod != 0) { //nolint:staticcheck
		err = r.addTreeNode(gt, pack, forceLoad, r.refsNode, r.depth)
	}

	tree = gt.Print()
	return tree, err
}

func (r *Refs) addTreeNode(
	parent gotree.Tree, //         : parent tree to add items to
	pack Pack, //                  : pack to laod
	forceLoad bool, //             : force load subtrees
	rn *refsNode, //               : the node
	depth int, //                  : depth of the node
) (
	err error, //                  : error if any
) {

	// hash and length of the node are already printed

	if depth == 0 {

		if len(rn.leafs) == 0 {
			parent.Add("(empty)")
			return err
		}

		for _, el := range rn.leafs {
			parent.Add(el.Hash.Hex()[:7]) // short
		}

		return err
	}

	// else if depth > 0

	for _, br := range rn.branches {

		if forceLoad == true { //nolint:staticcheck
			if err = r.loadNodeIfNeed(pack, br, depth-1); err != nil {
				return err
			}
		}

		if br.isLoaded() == false { //nolint:staticcheck
			parent.Add(br.hash.Hex()[:7] + " (not loaded)")
			continue
		}

		item := gotree.New(br.hash.Hex()[:7] + " " + fmt.Sprint(br.length))
		err = r.addTreeNode(item, pack, forceLoad, br, depth-1)

		if err != nil {
			return err
		}

		parent.AddTree(item)

	}

	return err
}
