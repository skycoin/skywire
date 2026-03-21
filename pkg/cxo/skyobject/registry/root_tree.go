package registry

import (
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"

	gotree "github.com/DiSiqueira/GoTree"
	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

// Tree used to print Root tree. The forceLoad
// argument used to  laod subtrees if they are
// not loaded. The Pack should have related
// Registry
func (r *Root) Tree(pack Pack) (tree string, err error) {

	gt := gotree.New(fmt.Sprintf("(root) %s %s (reg: %s)",
		r.Hash.Hex()[:7], r.Short(), r.Reg.Short()))

	var reg = pack.Registry()

	if reg == nil {
		err = errors.New("(*registry.Root).Tree: missing registry in Pack")
		return
	}

	if len(r.Refs) == 0 {

		gt.Add("(empty)")

	} else {

		for _, dr := range r.Refs {
			gt.AddTree(rootTreeDynamic(&dr, pack))
		}

	}

	tree = gt.Print()
	return

}

func rootTreeDynamic(d *Dynamic, pack Pack) (it gotree.Tree) {

	if d.IsValid() == false {
		it = gotree.New("*(dynamic) err: " + ErrInvalidDynamicReference.Error())
		return
	}

	if d.IsBlank() == true {
		it = gotree.New("*(dynamic) nil")
		return
	}

	var (
		sch Schema
		err error
	)

	if sch, err = pack.Registry().SchemaByReference(d.Schema); err != nil {
		it = gotree.New("*(dynamic) err: " + err.Error())
		return
	}

	if d.Hash == (cipher.SHA256{}) {
		it = gotree.New(fmt.Sprintf("*(dynamic) nil (type %s)", sch.String()))
		return
	}

	it = gotree.New("*(dynamic) " + d.Short())
	it.AddTree(rootTreeHash(pack, sch, d.Hash))

	return
}

func rootTreeHash(
	pack Pack,
	sch Schema,
	hash cipher.SHA256,
) (
	it gotree.Tree,
) {

	var (
		val []byte
		err error
	)

	if val, err = pack.Get(hash); err != nil {
		it = gotree.New("(err) " + err.Error())
		return
	}

	return rootTreeData(pack, sch, val)
}

func rootTreeData(
	pack Pack,
	sch Schema,
	val []byte,
) (
	it gotree.Tree,
) {

	if sch.IsReference() == true {
		return rootTreeReferences(pack, sch, val)
	}

	switch sch.Kind() {
	case reflect.Bool:

		return rootTreeBool(sch, val)

	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:

		return rootTreeInt(sch, val)

	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:

		return rootTreeUint(sch, val)

	case reflect.Float32, reflect.Float64:

		return rootTreeFloat(sch, val)

	case reflect.String:

		return rootTreeString(sch, val)

	case reflect.Array, reflect.Slice:

		return rootTreeSlice(pack, sch, val)

	case reflect.Struct:

		return rootTreeStruct(pack, sch, val)

	default:

		it = gotree.New(fmt.Sprintf("(err) invalid Kind <%s> of Schema %q",
			sch.Kind().String(),
			sch.String()))

	}

	return
}

func rootTreeReferences(
	pack Pack, //             :
	sch Schema, //            :
	val []byte, //            :
) (
	it gotree.Tree, // :
) {

	switch rt := sch.ReferenceType(); rt {

	case ReferenceTypeSingle:

		return rootTreeRef(pack, sch, val)

	case ReferenceTypeSlice:

		return rootTreeRefs(pack, sch, val)

	case ReferenceTypeDynamic:

		var (
			dr  Dynamic
			err error
		)

		if _, err = encoder.DeserializeRaw(val, &dr); err != nil {
			it = gotree.New("*(dynamic) err: " + err.Error())
			return
		}

		return rootTreeDynamic(&dr, pack)

	default:

		it = gotree.New(fmt.Sprintf(
			"invalid schema (%s): reference with invalid type %d",
			sch.String(),
			rt,
		))
	}

	return
}

func rootTreeValue(sch Schema, val interface{}) (it gotree.Tree) {

	var name string

	if name = sch.Name(); name != "" {
		it = gotree.New(fmt.Sprintf("%v (type %s)", val, name))
		return
	}

	it = gotree.New(fmt.Sprint(val))
	return

}

func rootTreeBool(sch Schema, val []byte) (it gotree.Tree) {

	var (
		x   bool
		err error
	)

	if _, err = encoder.DeserializeRaw(val, &x); err != nil {
		it = gotree.New("(err) " + err.Error())
		return
	}

	return rootTreeValue(sch, x)
}

func rootTreeInt(sch Schema, val []byte) (it gotree.Tree) {

	var (
		x   int64
		err error
	)

	switch sch.Kind() {
	case reflect.Int8:

		var y int8
		_, err = encoder.DeserializeRaw(val, &y)
		x = int64(y)

	case reflect.Int16:

		var y int16
		_, err = encoder.DeserializeRaw(val, &y)
		x = int64(y)

	case reflect.Int32:

		var y int32
		_, err = encoder.DeserializeRaw(val, &y)
		x = int64(y)

	case reflect.Int64:

		_, err = encoder.DeserializeRaw(val, &x)

	}

	if err != nil {
		it = gotree.New("(err) " + err.Error())
		return
	}

	return rootTreeValue(sch, x)
}

func rootTreeUint(sch Schema, val []byte) (it gotree.Tree) {

	var (
		x   uint64
		err error
	)

	switch sch.Kind() {
	case reflect.Uint8:

		var y uint8
		_, err = encoder.DeserializeRaw(val, &y)
		x = uint64(y)

	case reflect.Uint16:

		var y uint16
		_, err = encoder.DeserializeRaw(val, &y)
		x = uint64(y)

	case reflect.Uint32:

		var y uint32
		_, err = encoder.DeserializeRaw(val, &y)
		x = uint64(y)

	case reflect.Uint64:

		_, err = encoder.DeserializeRaw(val, &x)

	}
	if err != nil {
		it = gotree.New("(err) " + err.Error())
		return
	}

	return rootTreeValue(sch, x)
}

func rootTreeFloat(sch Schema, val []byte) (it gotree.Tree) {

	var (
		x   float64
		err error
	)

	switch sch.Kind() {
	case reflect.Float32:

		var y float32
		_, err = encoder.DeserializeRaw(val, &y)
		x = float64(y)

	case reflect.Float64:

		_, err = encoder.DeserializeRaw(val, &x)

	}

	if err != nil {
		it = gotree.New("(err) " + err.Error())
		return
	}

	return rootTreeValue(sch, x)
}

func rootTreeString(
	sch Schema,
	val []byte,
) (
	it gotree.Tree,
) {

	var (
		x   string
		err error
	)

	if _, err = encoder.DeserializeRaw(val, &x); err != nil {
		it = gotree.New("(err) " + err.Error())
		return
	}

	return rootTreeValue(sch, x)
}

// slice or array
func rootTreeSlice(pack Pack, sch Schema, val []byte) (it gotree.Tree) {

	var (
		el  Schema
		err error
	)

	if el = sch.Elem(); el == nil {
		it = gotree.New(fmt.Sprintf("(err) invalid schema %q: nil-element",
			sch.String()))
		return
	}

	// special case for []byte
	if sch.Kind() == reflect.Slice && el.Kind() == reflect.Uint8 {

		var x []byte
		if _, err = encoder.DeserializeRaw(val, &x); err != nil {
			it = gotree.New("(err) " + err.Error())
			return
		}

		it = gotree.New("([]byte) " + hex.EncodeToString(x))
		return
	}

	var (
		ln    int // length
		shift int // shift
	)

	var itName string

	if sch.Kind() == reflect.Array {

		ln = sch.Len()

		if name := sch.Name(); name != "" {
			itName = fmt.Sprintf("[%d]%s (%s)", ln, el.String(), name)
		} else {
			itName = fmt.Sprintf("[%d]%s", ln, el.String())
		}

	} else {

		// reflect.Slice
		if name := sch.Name(); name != "" {
			itName = fmt.Sprintf("[]%s (%s)", el.String(), name)
		} else {
			itName = fmt.Sprintf("[]%s", el.String())
		}

		if ln, err = getLength(val); err != nil {
			it = gotree.New(itName + " (err) " + err.Error())
			return
		}

		itName += fmt.Sprintf(" (length %d)", ln)
		shift = 4

	}

	it = gotree.New(itName)

	var m, s, k int

	if s = fixedSize(el.Kind()); s < 0 {

		for k = 0; k < ln; k++ {

			if shift+s > len(val) {
				it.Add(fmt.Sprintf(
					"(err) unexpected end of %s at %d element",
					sch.Kind().String(), k))
				return
			}

			it.AddTree(rootTreeData(pack, el, val[shift:shift+s]))
			shift += s

		}

	} else {

		for k = 0; k < ln; k++ {

			if shift > len(val) {
				it.Add(fmt.Sprintf(
					"(err) unexpected end of %s at %d element",
					sch.Kind().String(), k))
				return
			}

			if m, err = el.Size(val[shift:]); err != nil {
				return
			}

			it.AddTree(rootTreeData(pack, el, val[shift:shift+m]))
			shift += m

		}

	}

	return
}

func rootTreeStruct(
	pack Pack,
	sch Schema,
	val []byte,
) (
	it gotree.Tree,
) {

	var (
		shift int
		s     int
		err   error
	)

	it = gotree.New(sch.String())

	for _, f := range sch.Fields() {

		if shift > len(val) {

			// detailed error: we can't change text after creation with this API,
			// so add an error child node instead
			it.Add(fmt.Sprintf(
				"(err) unexpected end of encoded struct '%s' "+
					"at field '%s', schema of field: '%s'",
				sch.String(),
				f.Name(),
				f.Schema().String()))
			return

		}

		if s, err = f.Schema().Size(val[shift:]); err != nil {
			it.Add("(err) " + err.Error())
			return
		}

		fit := rootTreeData(pack, f.Schema(), val[shift:shift+s])
		fieldTree := gotree.New(f.Name() + ": " + fit.Text())
		for _, child := range fit.Items() {
			fieldTree.AddTree(child)
		}
		it.AddTree(fieldTree)
		shift += s

	}

	return
}

func rootTreeRef(pack Pack, sch Schema, val []byte) (it gotree.Tree) {

	var (
		ref Ref
		el  Schema
		err error
	)

	if el = sch.Elem(); el == nil {

		it = gotree.New(fmt.Sprintf(
			"*(<ref>) err: missing schema of element: %s ",
			sch,
		))

		return
	}

	if _, err = encoder.DeserializeRaw(val, &ref); err != nil {
		it = gotree.New(fmt.Sprintf("*(%s) err: %s", el.String(), err.Error()))
		return
	}

	if ref.Hash == (cipher.SHA256{}) {
		it = gotree.New(fmt.Sprintf("*(%s) nil", el.String()))
		return
	}

	it = gotree.New(fmt.Sprintf("*(%s) %s", el.String(), ref.Short()))
	it.AddTree(rootTreeHash(pack, el, ref.Hash))

	return
}

func rootTreeRefs(pack Pack, sch Schema, val []byte) (it gotree.Tree) {

	var (
		refs Refs
		el   Schema
		err  error
	)

	if el = sch.Elem(); el == nil {
		it = gotree.New("[]*(<refs>) err: missing schema of element")
		return
	}

	if _, err = encoder.DeserializeRaw(val, &refs); err != nil {
		it = gotree.New(fmt.Sprintf("[]*(-----) %s err: %s", el.String(), err.Error()))
		return
	}

	// initialize first

	var ln int
	if ln, err = refs.Len(pack); err != nil {
		it = gotree.New(fmt.Sprintf("[]*(%s) %s err: %s", el.String(), refs.Short(),
			err.Error()))
		return
	}

	// can be blank

	if ln == 0 {
		it = gotree.New(fmt.Sprintf("[]*(%s) %s nil", el.String(), refs.Short()))
		return
	}

	it = gotree.New(fmt.Sprintf("[]*(%s) %s length: %d", el.String(), refs.Short(),
		ln))

	err = refs.Walk(pack, el, func(
		hash cipher.SHA256,
		depth int,
	) (
		deepper bool,
		err error,
	) {

		if depth != 0 {
			return true, nil
		}

		it.AddTree(rootTreeHash(pack, el, hash))
		return true, nil
	})

	if err != nil {
		it.Add("err: " + err.Error())
	}

	return
}
