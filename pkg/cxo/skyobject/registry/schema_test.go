package registry

import (
	"reflect"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

func testCalculateSchemaRef(s Schema) SchemaRef {
	return SchemaRef(cipher.SumSHA256(s.Encode()))
}

func TestSchema_Reference(t *testing.T) {
	// Reference() SchemaRef

	var (
		reg = testRegistry()

		sc, sg Schema
		sr     SchemaRef

		err error
	)

	for _, tt := range testTypes() {

		t.Log(tt.Name)

		if sc, err = reg.SchemaByName(tt.Name); err != nil {
			t.Error(err)
			continue
		}

		if sr = sc.Reference(); sr == (SchemaRef{}) {
			t.Error("blank SchemaRef")
			continue
		}

		if cc := testCalculateSchemaRef(sc); cc != sr {
			t.Errorf("wrong ScehmaRef: want %s, got %s", cc.Short(), sr.Short())
			continue
		}

		if sg, err = reg.SchemaByReference(sr); err != nil {
			t.Error("can't get Schema by SchemaRef:", err)
			continue
		}

		if sg != sc {
			t.Error("different ponters to the same schema (memory pressure)")
		}

	}

}

func TestSchema_IsReference(t *testing.T) {
	// IsReference() bool

	var (
		reg = testRegistry()

		ts  Schema
		err error
	)

	for _, tt := range testTypes() {

		t.Log(tt.Name)

		if ts, err = reg.SchemaByName(tt.Name); err != nil {
			t.Error("can't find Shema by name:", err)
			continue
		}

		if true == ts.IsReference() {
			t.Error("IsReference() returns true for non-reference type")
			// keep going on
		}

		for _, fl := range ts.Fields() {
			t.Log(tt.Name, ">", fl.Name())

			if fs := fl.Schema(); true == fs.IsReference() {

				if tt.Name != "test.Group" {
					t.Error("unexpected reference")
					continue
				}

				switch typ := fs.ReferenceType(); typ {
				case ReferenceTypeNone:
					t.Error("IsReference() returns true but ReferenceType is " +
						"ReferenceTypeNone")
				case ReferenceTypeSingle:
					if fl.Name() != "Curator" {
						t.Error("unexpected Ref")
						continue
					}
				case ReferenceTypeSlice:
					if fl.Name() != "Members" {
						t.Error("unexpected Refs")
						continue
					}
				case ReferenceTypeDynamic:
					if fl.Name() != "Developer" {
						t.Error("unexpected Dynamic reference")
					}
					continue
				default:
					t.Errorf("IsReference() returns true but ReferenceType is "+
						"undefined %d", typ)
					continue
				}

				if el := fs.Elem(); el.Name() != "test.User" {
					t.Error("unknown Schema of reference")
				} else if gs, err := reg.SchemaByName("test.User"); err != nil {
					t.Error(err)
				} else if gs != el {
					t.Error("unnecessary memory overhead")
				}

			}

		}

	}

}

func TestSchema_ReferenceType(t *testing.T) {
	// ReferenceType() ReferenceType

	var (
		reg = testRegistry()

		gr  Schema
		err error
	)

	if gr, err = reg.SchemaByName("test.Group"); err != nil {
		t.Fatal(err)
	}

	for _, fl := range gr.Fields() {
		t.Log(fl.Name(), fl.Schema())

		var fs = fl.Schema()

		if true == fs.IsReference() {
			switch fs.ReferenceType() {
			case ReferenceTypeSingle, ReferenceTypeSlice, ReferenceTypeDynamic:
			default:
				t.Error("malformed reference (wrong ref. type):", fs)
			}
			continue
		}

		if rt := fs.ReferenceType(); rt != ReferenceTypeNone {
			t.Error("non-reference has ReferenceType", rt)
		}

	}

}

func TestSchema_HasReferences(t *testing.T) {
	// HasReferences() bool

	var (
		reg = testRegistry()

		s   Schema
		err error
	)

	// todo inclding deep nested schemas with references

	for _, tt := range testTypes() {

		t.Log(tt.Name)

		if s, err = reg.SchemaByName(tt.Name); err != nil {
			continue
		}

		if true == s.HasReferences() {
			if s.Name() == "test.Group" {
				continue // ok
			} else {
				t.Error(tt.Name, "has references")
			}
		}

	}

	// deep nested reference

	type A struct {
		Name string
	}

	type B struct {
		A Ref `skyobject:"schema=test.A"`
	}

	type C struct {
		B B
	}

	type D struct {
		S []B
	}

	type E struct {
		A [3]D
	}

	reg = NewRegistry(func(r *Reg) {
		r.Register("test.A", A{})
		r.Register("test.B", B{})
		r.Register("test.C", C{})
		r.Register("test.D", D{})
		r.Register("test.E", E{})
	})

	if s, err = reg.SchemaByName("test.E"); err != nil {
		t.Fatal(err)
	}

	if false == s.HasReferences() {
		t.Error("has not references (deep)")
	}

}

func TestSchema_Kind(t *testing.T) {
	// Kind() reflect.Kind

	// TODO (kostyarin): low priority

}

func TestSchema_Name(t *testing.T) {
	// Name() string

	var reg = testRegistry()

	for _, tt := range testTypes() {

		t.Log(tt.Name)

		if s, err := reg.SchemaByName(tt.Name); err != nil {
			t.Error(err)
		} else if name := s.Name(); name != tt.Name {
			t.Errorf("wrong Schema name: want %q, got %q", tt.Name, name)
		}

	}

}

func TestSchema_Len(t *testing.T) {
	// Len() int

	var (
		reg = testRegistry()

		s   Schema
		err error
	)

	defer shouldNotPanic(t) // reflect methods can panic

	for _, tt := range testTypes() {

		t.Log(tt.Name)

		if s, err = reg.schemaByName(tt.Name); err != nil {
			t.Error(err)
			continue
		}

		if s.Len() != 0 {
			t.Error("got non-zero Len of struct")
		}

		var rt = reflect.Indirect(reflect.ValueOf(tt.Val)).Type()

		for i, fl := range s.Fields() {

			t.Log(tt.Name, ">", fl.Name())

			var rf = rt.FieldByIndex([]int{i}).Type // reflect.Type of the field

			if kind := fl.Kind(); kind == reflect.Array {

				if rf.Kind() != reflect.Array {
					t.Error("unexpected array")
					continue
				}

				if sl, rl := fl.Schema().Len(), rf.Len(); sl != rl {
					t.Errorf("invalid length of array: want %d, got %d", sl, rl)
				}

			} else if s.Len() != 0 {

				t.Error("got non-zero Len of", kind.String())

			}

		}

	}

}

func TestSchema_Fields(t *testing.T) {
	// Fields() []Field

	var (
		reg = testRegistry()

		s   Schema
		err error
	)

	defer shouldNotPanic(t) // reflect methods can panic

	for _, tt := range testTypes() {

		t.Log(tt.Name)

		if s, err = reg.schemaByName(tt.Name); err != nil {
			t.Error(err)
			continue
		}

		var rt = reflect.Indirect(reflect.ValueOf(tt.Val)).Type()

		for i, fl := range s.Fields() {

			t.Log(tt.Name, ">", fl.Name())

			var rf = rt.FieldByIndex([]int{i}) // reflect.StructField

			// ccompare names

			if name := fl.Name(); name != rf.Name {
				t.Errorf("wrong field name: want %q, got %q", rf.Name, name)
			}

			var kind = fl.Kind()

			if kind == reflect.Ptr || kind == reflect.Interface {
				continue // Ref, Refs, and Dynaimc (skip them)
			} else if reflectKind := rf.Type.Kind(); kind != reflectKind {
				t.Errorf("invalid kind of struct field: want %s, got %s",
					reflectKind.String(), kind.String())
			}

		}

	}

}

func TestSchema_Elem(t *testing.T) {
	// Elem() (s Schema)

	// The Elem is element of a reference (except
	// Dynaimc), of an array, or of a slice

	var (
		reg = testRegistry()

		s   Schema
		err error
	)

	defer shouldNotPanic(t) // reflect methods can panic

	for _, tt := range testTypes() {

		t.Log(tt.Name)

		if s, err = reg.schemaByName(tt.Name); err != nil {
			t.Error(err)
			continue
		}

		if s.Elem() != nil {
			t.Error("unexpected Elem of struct")
		}

		var rt = reflect.Indirect(reflect.ValueOf(tt.Val)).Type()

		for i, fl := range s.Fields() {

			t.Log(tt.Name, ">", fl.Name())

			var rf = rt.FieldByIndex([]int{i}) // reflect.StructField

			// Ref, Refs, array of slice

			var fs = fl.Schema()
			var el = fs.Elem()

			switch kind := fs.Kind(); kind {
			case reflect.Ptr:

				switch fs.ReferenceType() {
				case ReferenceTypeSingle, ReferenceTypeSlice:

					if el == nil {
						t.Error("misisng Elem")
						continue
					}

					var tsn string
					if tsn, err = TagSchemaName(rf.Tag); err != nil {
						t.Error("missing tagged schema")
					}

					if el.Name() != tsn {
						t.Errorf("wrong schema iof reference: want %q, got %q",
							tsn, el.Name())
					}

				default:

					if el != nil {
						t.Error("unexpected Elem")
					}

				}

			case reflect.Array, reflect.Slice:

				if el == nil {
					t.Error("missing Elem")
					continue
				}

				if ek, rk := el.Kind(), rf.Type.Elem().Kind(); ek != rk {
					t.Errorf("invalid kind of Elem: want %s, got %s",
						rk.String(), ek.String())
				}

			default:

				if el != nil {
					t.Error("unexpected Elem")
				}

			}

		}

	}

}

func TestSchema_RawName(t *testing.T) {
	// RawName() []byte

	var reg = testRegistry()

	for _, tt := range testTypes() {

		t.Log(tt.Name)

		if s, err := reg.SchemaByName(tt.Name); err != nil {
			t.Error(err)
		} else if name := string(s.RawName()); name != tt.Name {
			t.Errorf("wrong Schema RawName: want %q, got %q", tt.Name, name)
		}

	}

}

func TestSchema_IsRegistered(t *testing.T) {
	// IsRegistered() bool

	var (
		reg = testRegistry()

		s   Schema
		err error
	)

	for _, tt := range testTypes() {

		t.Log(tt.Name)

		if s, err = reg.SchemaByName(tt.Name); err != nil {
			t.Error(err)
			continue
		}

		if false == s.IsRegistered() {
			t.Error("IsRegistered returns false for registered schema")
			// keep going on
		}

		// only named structures can be registered

		for _, fl := range s.Fields() {

			var fs = fl.Schema()

			if fs.Kind() == reflect.Struct {
				// TODO (kostyarin): for future to increase coverage easy way
				continue
			}

			if true == fs.IsRegistered() {
				t.Error("IsRegistered of non-struct returns true")
			}

		}

	}

}

func TestSchema_Encode(t *testing.T) {
	// Encode() (b []byte)

	// TODO: low priority

}

func TestSchema_Size(t *testing.T) {
	// Size(p []byte) (n int, err error)

	var (
		reg = testRegistry()

		s   Schema
		ss  int
		err error
	)

	for _, tt := range testTypes() {

		t.Log(tt.Name)

		if s, err = reg.SchemaByName(tt.Name); err != nil {
			t.Error(err)
			continue
		}

		var data = encoder.Serialize(tt.Val)

		if ss, err = s.Size(data); err != nil {
			t.Error(err)
		} else if ss != len(data) {
			t.Errorf("wrong struct Size: want %d, got %d", len(data), ss)
		}

		var rv = reflect.Indirect(reflect.ValueOf(tt.Val))

		for i, fl := range s.Fields() {

			var rf = rv.Field(i)

			data = encoder.Serialize(rf.Interface())

			if ss, err := fl.Schema().Size(data); err != nil {
				t.Error(err)
			} else if ss != len(data) {
				t.Errorf("wrong Schema Size: want %d, got %d", len(data), ss)
			}

		}

	}

}

func TestSchema_String(t *testing.T) {
	// String() string

	// TODO: low priority

}
