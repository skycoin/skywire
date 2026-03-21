package registry

import (
	"reflect"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

//
// Ref
//

// A Ref represents reference to object.
// It is like a pointer
type Ref struct {
	Hash cipher.SHA256 // Hash of CX object
}

// IsBlank returns true if the Ref is blank
func (r *Ref) IsBlank() bool {
	return r.Hash == (cipher.SHA256{})
}

// Short returns first 7 bytes of Stirng
func (r *Ref) Short() string {
	return r.Hash.Hex()[:7]
}

// String implements fmt.Stringer interface
func (r *Ref) String() string {
	return r.Hash.Hex()
}

// Value of the Ref
func (r *Ref) Value(pack Pack, obj interface{}) (err error) {

	if true == r.IsBlank() {
		return ErrReferenceRepresentsNil
	}

	// obtain encoded object
	var val []byte
	if val, err = pack.Get(r.Hash); err != nil {
		return
	}

	_, err = encoder.DeserializeRaw(val, obj)

	return
}

// SetValue replacing the Ref with new. Use nil-interface{} to clear
func (r *Ref) SetValue(
	pack Pack, //       : pack to save
	obj interface{}, // : the value
) (
	err error, //       : error if any
) {

	if true == isNil(obj) {
		r.Clear()
		return
	}

	var hash cipher.SHA256
	if hash, err = pack.Add(encoder.Serialize(obj)); err != nil {
		return
	}

	r.Hash = hash

	return

}

// Clear the Ref making it blank
func (r *Ref) Clear() {
	r.Hash = cipher.SHA256{}
}

// isNil interface or nil pointer of a type
func isNil(obj interface{}) bool {

	if obj == nil {
		return true
	}

	val := reflect.ValueOf(obj)

	return val.Kind() == reflect.Ptr && val.IsNil()
}

// Walk through the Ref. See WalkFunc for details. The
// Schema is used only if the walkFunc goes deepper.
// Otherwise, the sch argument can be nil. The Schema
// must be schema of element of the Ref, not schema of
// the Ref
func (r *Ref) Walk(
	pack Pack, //         :
	sch Schema, //        :
	walkFunc WalkFunc, // :
) (
	err error,
) {

	var deepper bool
	if deepper, err = walkFunc(r.Hash, 0); err != nil || deepper == false {
		return
	}

	if r.Hash == (cipher.SHA256{}) {
		return // ignore the deepper
	}

	err = walkSchemaHash(pack, sch, r.Hash, walkFunc)

	if err == ErrStopIteration {
		err = nil
	}

	return

}

// Split used by the node package to fill the Ref
func (r *Ref) Split(s Splitter, el Schema) {
	splitSchemaHashAsync(s, el, r.Hash)
}
