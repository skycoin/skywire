package registry

import (
	"reflect"

	"github.com/skycoin/skycoin/src/cipher/encoder"
)

var refSize, refsSize, dynamicSize int

func init() {
	for _, x := range []struct {
		val *int
		obj interface{}
	}{
		{&refSize, Ref{}},
		{&refsSize, Refs{}},
		{&dynamicSize, Dynamic{}},
	} {
		*x.val = len(encoder.Serialize(x.obj))
	}
}

// schemaArraySliceSize iterates over encoded elements of array or slice
// to get size used by them; l is length of array or slice, shift is
// shift in p slice from which data begins, el is schema of element
func schemaArraySliceSize(el Schema, l, shift int, p []byte) (n int,
	err error) {

	n += shift

	if s := fixedSize(el.Kind()); s > 0 {
		n += l * s
	} else {
		var m int
		for i := 0; i < l; i++ {
			if n > len(p) {
				err = ErrInvalidSchemaOrData
				return
			}
			if m, err = el.Size(p[n:]); err != nil {
				return
			}
			n += m
		}
	}
	return
}

// getLength of length prefixed values
// (like slice of string)
func getLength(p []byte) (l int, err error) {
	var u uint32
	_, err = encoder.DeserializeRaw(p, &u)
	l = int(u)
	return
}

// fixedSize returns -1 if given kind represents a
// variable size value (like array, slice or struct);
// in other cases it returns appropriate size
// (1, 2, 4 or 8)
func fixedSize(kind reflect.Kind) (n int) {
	switch kind {
	case reflect.Bool, reflect.Int8, reflect.Uint8:
		n = 1
	case reflect.Int16, reflect.Uint16:
		n = 2
	case reflect.Int32, reflect.Uint32, reflect.Float32:
		n = 4
	case reflect.Int64, reflect.Uint64, reflect.Float64:
		n = 8
	default:
		n = -1
	}
	return
}
