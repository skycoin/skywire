package registry

import (
	"reflect"
)

// A Reg creates new Registry
type Reg struct {
	tn map[reflect.Type]string // type -> registered name
}

func newReg() *Reg {
	return &Reg{
		tn: make(map[reflect.Type]string),
	}
}

// Register type of given value with given name. If
// givne value is pointer, then it will be converted to
// non-pointer inside. E.g. it registers non-pointer types
// only
func (r *Reg) Register(name string, val interface{}) {
	if name == "" {
		panic("empty name")
	}
	typ := typeOf(val)
	switch typ {
	case typeOfRef, typeOfRefs, typeOfDynamic:
		panic("can't register reference type")
	default:
	}

	for _, n := range r.tn {
		if n == name {
			panic("this name already registered: " + name)
		}
	}

	r.tn[typ] = name
}

// use (reflect.Type).Name() or name provided to Register;
// if there aren't, then return nil
func (r *Reg) typeName(typ reflect.Type) []byte {
	if name, ok := r.tn[typ]; ok {
		return []byte(name)
	}
	if name := typ.Name(); name != "" {
		return []byte(name)
	}
	return nil
}

func (r *Reg) getSchema(typ reflect.Type) Schema {

	if typ == typeOfDynamic { // dynamic reference
		return &referenceSchema{
			schema: schema{
				ref:  SchemaRef{},
				kind: reflect.Interface, // Dynamic is kind of interface{}
			},
			typ: ReferenceTypeDynamic,
		}
	}

	if typ == typeOfRef || typ == typeOfRefs {
		panic("Ref or Refs are not allowed in arrays and slices")
	}

	switch typ.Kind() {

	case reflect.Bool, reflect.Int8, reflect.Uint8,
		reflect.Int16, reflect.Uint16,
		reflect.Int32, reflect.Uint32, reflect.Float32,
		reflect.Int64, reflect.Uint64, reflect.Float64,
		reflect.String:

		s := new(schema)
		s.kind, s.name = typ.Kind(), r.typeName(typ)
		return s

	case reflect.Slice:

		// get schema of element

		ss := new(sliceSchema)
		ss.kind, ss.name = typ.Kind(), r.typeName(typ)

		el := r.getSchema(typ.Elem())

		if el.IsRegistered() {
			ss.elem = &schema{SchemaRef{}, el.Kind(), el.RawName()}
			return ss
		}

		ss.elem = el
		return ss

	case reflect.Array:

		// get schema of element and length

		as := new(arraySchema)
		as.kind, as.name = typ.Kind(), r.typeName(typ)
		as.length = typ.Len()

		el := r.getSchema(typ.Elem())

		if el.IsRegistered() {
			as.elem = &schema{SchemaRef{}, el.Kind(), el.RawName()}
			return as
		}

		as.elem = el
		return as

	case reflect.Struct:

		// get schemas of fields

		ss := new(structSchema)
		ss.kind, ss.name = typ.Kind(), r.typeName(typ)

		for i, nf := 0, typ.NumField(); i < nf; i++ {

			sf := typ.Field(i)
			if sf.Tag.Get("enc") == "-" || sf.PkgPath != "" || sf.Name == "_" {
				continue
			}
			ss.fields = append(ss.fields, r.getField(sf))

		}

		return ss

	default:
	}

	panic("invlaid type: " + typ.String())

}

func (r *Reg) getField(sf reflect.StructField) Field {

	f := new(field)

	f.name = []byte(sf.Name)
	f.tag = []byte(sf.Tag)

	t := sf.Type // reflect.Type

	switch t {
	case typeOfRef: // reference
		tagRef := mustTagSchemaName(sf.Tag)
		f.schema = &referenceSchema{
			schema: schema{
				ref:  SchemaRef{},
				kind: reflect.Ptr, // Ref is pointer
			},
			typ:  ReferenceTypeSingle,
			elem: &schema{kind: reflect.Struct, name: []byte(tagRef)},
		}
		return f
	case typeOfRefs: // references
		tagRef := mustTagSchemaName(sf.Tag)
		f.schema = &referenceSchema{
			schema: schema{
				ref:  SchemaRef{},
				kind: reflect.Ptr, // Refs is pointer (actually []*T)
			},
			typ:  ReferenceTypeSlice,
			elem: &schema{kind: reflect.Struct, name: []byte(tagRef)},
		}
		return f
	case typeOfDynamic: // dynamic reference
		f.schema = &referenceSchema{
			schema: schema{
				ref:  SchemaRef{},
				kind: reflect.Interface, // Dynamic is interface{}
			},
			typ: ReferenceTypeDynamic,
		}
		return f
	default:
	}

	if s := r.getSchema(sf.Type); s.IsRegistered() {
		f.schema = &schema{SchemaRef{}, s.Kind(), s.RawName()}
	} else {
		f.schema = s
	}

	return f

}
