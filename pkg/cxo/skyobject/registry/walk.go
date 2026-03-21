package registry

import (
	"fmt"
	"reflect"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

// walkSchemaHash walks usng given Schema and
// hash of the object (the Schema is Schema of the
// object the hash points to); the WalkFunc is
// already called with given hash and now it
// goes deepper. The hash is not blank
func walkSchemaHash(
	pack Pack, //          : pack to get
	sch Schema, //         : schema of the object
	hash cipher.SHA256, // : hash of the object
	walkFunc WalkFunc, //  : the function
) (
	err error, //          : an error
) {

	// So, we can use sch.Hashreferences() method
	// to avoid unnecessary walking deepper. If
	// the sch has not references then we can't
	// go deepper and we can skip all bleow the
	// check

	if sch.HasReferences() == false {
		return // nothing to walk through
	}

	// get object

	var val []byte
	if val, err = pack.Get(hash); err != nil {
		return
	}

	return walkSchemaData(pack, sch, val, walkFunc)
}

// walkSchemaData walks usng given Schema and
// encoded object
func walkSchemaData(
	pack Pack, //         : pack to get
	sch Schema, //        : schema of the object
	val []byte, //        : encoded object
	walkFunc WalkFunc, // : the function
) (
	err error, //         : an error
) {

	// the object represents Ref, Refs or Dynamic
	if sch.IsReference() == true {
		return walkSchemaReference(pack, sch, val, walkFunc)
	}

	switch sch.Kind() {
	case reflect.Array:
		return walkArray(pack, sch, val, walkFunc)
	case reflect.Slice:
		return walkSlice(pack, sch, val, walkFunc)
	case reflect.Struct:
		return walkStruct(pack, sch, val, walkFunc)
	}

	return fmt.Errorf("invalid Schema to walk through: %s", sch)

}

func walkSchemaReference(
	pack Pack, //         : pack to get
	sch Schema, //        : schema of the object
	val []byte, //        : encoded reference
	walkFunc WalkFunc, // : the function
) (
	err error, //         : an error
) {

	switch rt := sch.ReferenceType(); rt {

	case ReferenceTypeSingle: // Ref

		var el Schema
		if el = sch.Elem(); el == nil {
			return fmt.Errorf("sSchema of Ref with nil element: %s", sch)
		}

		var ref Ref
		if _, err = encoder.DeserializeRaw(val, &ref); err != nil {
			return
		}

		return ref.Walk(pack, el, walkFunc)

	case ReferenceTypeSlice: // Refs

		var el Schema
		if el = sch.Elem(); el == nil {
			return fmt.Errorf("sSchema of Ref with nil element: %s", sch)
		}

		var refs Refs
		if _, err = encoder.DeserializeRaw(val, &refs); err != nil {
			return
		}

		return refs.Walk(pack, el, walkFunc)

	case ReferenceTypeDynamic: // Dynamic

		var dr Dynamic
		if _, err = encoder.DeserializeRaw(val, &dr); err != nil {
			return
		}
		return dr.Walk(pack, walkFunc)

	default:

		return fmt.Errorf("invalid ReferenceType %d to walk through", rt)

	}

}

func walkArray(
	pack Pack, //         : pack to get
	sch Schema, //        : schema of the array
	val []byte, //        : encoded array
	walkFunc WalkFunc, // : the function
) (
	err error, //         : an error
) {

	var el Schema // Schema of the element
	if el = sch.Elem(); el != nil {
		// just avoid panic if the Scehma is invlaid;
		// any invalid Schema shuld not break CXO, since
		// we are not trusting remote nodes, even if they
		// sign their objects; any attacker can provide
		// invalid signed Registry to brak every nodes;
		// but we just return the error
		return fmt.Errorf("Schema of element of array %q is nil", sch)
	}

	return walkArraySlice(pack, el, sch.Len(), val, walkFunc)

}

func walkSlice(
	pack Pack, //         : pack to get
	sch Schema, //        : schema of the slice
	val []byte, //        : encoded slice
	walkFunc WalkFunc, // : the function
) (
	err error, //         : an error
) {

	var ln int // length of the slice
	if ln, err = getLength(val); err != nil {
		return
	}

	var el Schema // Schema of the element
	if el = sch.Elem(); el != nil {
		return fmt.Errorf("Schema of element of slice %q is nil", sch)
	}

	return walkArraySlice(pack, el, ln, val[4:], walkFunc)

}

func walkArraySlice(
	pack Pack, //         : pack to get
	el Schema, //         : shcema of an element
	ln int, //            : length of the array or slice (> 0)
	val []byte, //        : encoded array or slice starting from first element
	walkFunc WalkFunc, // : the function
) (
	err error, //         : an error
) {

	// doesn't need to walk through the zero-length
	// array or slice even if it contains references

	if ln == 0 {
		return
	}

	var shift, m int

	for i := 0; i < ln; i++ {

		if shift > len(val) {
			err = fmt.Errorf("unexpected end of encoded  array or slice "+
				"of <%s>, length: %d, index: %d", el, ln, i)
			return
		}

		if m, err = el.Size(val[shift:]); err != nil {
			return
		}

		// if we are here, then the el contains references
		// and we don't need to call el.HasReferences(),
		// we just walk the element

		err = walkSchemaData(pack, el, val[shift:shift+m], walkFunc)

		if err != nil {
			return
		}

		shift += m

	}

	return

}

func walkStruct(
	pack Pack, //         : pack to get
	sch Schema, //        : schema of the struct
	val []byte, //        : encoded struct
	walkFunc WalkFunc, // : the function
) (
	err error, //         : an error
) {

	var shift, s int

	for i, fl := range sch.Fields() {

		if shift > len(val) {
			err = fmt.Errorf("unexpected end of encoded struct <%s>, "+
				"field number: %d, field name: %q, schema of field: %s",
				sch.String(),
				i,
				fl.Name(),
				fl.Schema().String())
			return
		}

		if s, err = fl.Schema().Size(val[shift:]); err != nil {
			return
		}

		// skip all fields that doesn't contains references
		if fl.Schema().HasReferences() == false {
			shift += s
			continue
		}

		err = walkSchemaData(pack, fl.Schema(), val[shift:shift+s], walkFunc)

		if err != nil {
			return
		}

		shift += s

	}

	return

}
