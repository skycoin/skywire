package registry

import (
	"fmt"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

// A Dynamic represents reference to object
// of some type. The Dynamic keeps reference
// to object and reference to the type
// It is like an interface
type Dynamic struct {
	Hash   cipher.SHA256 // Hash of CX object
	Schema SchemaRef     // Schema is reference to schema
}

// IsValid returns true if the Dynamic is blank, full
// or has reference to shcema only. A Dynamic is invalid if
// it has reference to object but deosn't have reference to
// shcema
func (d *Dynamic) IsValid() bool {
	if d.Schema.IsBlank() {
		return d.Hash == (cipher.SHA256{})
	}
	return true
}

// Short string
func (d *Dynamic) Short() string {
	return fmt.Sprintf("{%s:%s}", d.Schema.Short(), d.Hash.Hex()[:7])
}

// String implements fmt.Stringer interface
func (d *Dynamic) String() string {
	return "{" + d.Schema.String() + ", " + d.Hash.Hex() + "}"
}

// IsBlank returns true if the Dynamic is blank
func (d *Dynamic) IsBlank() bool {
	return *d == Dynamic{}
}

// Value of the Dynamic. The obj argument
// must be a non-nil pointer
func (d *Dynamic) Value(
	pack Pack, //       : pack to get
	obj interface{}, // : pointer to object to decode to
) (
	err error, //       : get or decode error
) {

	if false == d.IsValid() {
		return ErrInvalidDynamicReference
	}

	if true == d.IsBlank() {
		return ErrReferenceRepresentsNil
	}

	return get(pack, d.Hash, obj)
}

// SetValue replacing the Dynamic.Hash with new.
// Use nil to make it blank. Be careful, the SetValue
// never checks and sets Schema hash. E.g. the
// SchemaRef field will not be changed
func (d *Dynamic) SetValue(
	pack Pack, //       : pack to save
	obj interface{}, // : an encodable object
) (
	err error, //       : saving error
) {

	if true == isNil(obj) {
		d.Clear()
		return
	}

	var hash cipher.SHA256
	if hash, err = pack.Add(encoder.Serialize(obj)); err != nil {
		return
	}

	d.Hash = hash

	return
}

// Clear the Dynamic making it blank.
// It clears both object reference and
// schema reference
func (d *Dynamic) Clear() {
	d.Hash = cipher.SHA256{}
	d.Schema = SchemaRef{}
}

// Walk through the Dynamic. The walkFunc never be called with
// SchemaRef. It receive only hash of object. See WalkFunc for
// details
func (d *Dynamic) Walk(
	pack Pack,
	walkFunc WalkFunc,
) (
	err error,
) {

	if walkFunc == nil {
		panic("walkFunc is nil") // for developers
	}

	if d.IsValid() == false {
		return ErrInvalidDynamicReference
	}

	var deepper bool
	if deepper, err = walkFunc(d.Hash, 0); err != nil {
		if err == ErrStopIteration {
			err = nil // suppress this error
		}
		return
	}

	if deepper == false {
		return
	}

	if d.Hash == (cipher.SHA256{}) {
		return
	}

	var reg *Registry
	if reg = pack.Registry(); reg == nil {
		return ErrMissingRegistry
	}

	var sch Schema
	if sch, err = reg.SchemaByReference(d.Schema); err != nil {
		return
	}

	err = walkSchemaHash(pack, sch, d.Hash, walkFunc)

	if err == ErrStopIteration {
		err = nil // suppress this error
	}

	return
}

// Split used by the node package to fill the Dynamic.
func (d *Dynamic) Split(s Splitter) {

	if d.IsValid() == false {
		s.Fail(ErrInvalidDynamicReference)
		return
	}

	if d.IsBlank() == true {
		return // nothing to split
	}

	var sch, err = s.Registry().SchemaByReference(d.Schema)

	if err != nil {
		s.Fail(err)
		return
	}

	splitSchemaHash(s, sch, d.Hash)

}
