package alignment

import (
	"fmt"
	"reflect"
)

// PrintStruct prints detailed information of struct fields alignment.
func PrintStruct(v interface{}) {
	typ := reflect.TypeOf(v)
	fmt.Printf("Struct is %d bytes long\n", typ.Size())
	// We can run through the fields in the structure in order
	n := typ.NumField()
	for i := 0; i < n; i++ {
		field := typ.Field(i)
		fmt.Printf("%s at offset %v, size=%d, align=%d\n",
			field.Name, field.Offset, field.Type.Size(),
			field.Type.Align())
	}
}
