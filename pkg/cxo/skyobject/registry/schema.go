package registry

import (
	"fmt"
	"reflect"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

var (
	typeOfRef     = typeOf(Ref{})
	typeOfRefs    = typeOf(Refs{})
	typeOfDynamic = typeOf(Dynamic{})
)

// A ReferenceType represents type of a reference
type ReferenceType int

// possible reference
const (
	ReferenceTypeNone    ReferenceType = iota
	ReferenceTypeSingle                // Ref (cipher.SHA256)
	ReferenceTypeSlice                 // Refs (a'la []Ref)
	ReferenceTypeDynamic               // Dynamic (struct{Object, Schema Ref.})
)

// A Schema represents schema of a CX object
type Schema interface {
	// Reference of the Schema. The reference is valid after call of Done of
	// Registry by which the Schema created
	Reference() SchemaRef

	IsReference() bool            // is the schema reference
	ReferenceType() ReferenceType // type of the reference if it is a reference

	// HasReferences returns true if this Schema is a reference or contains
	// references on any level deep
	HasReferences() bool

	Kind() reflect.Kind // Kind of the Schema
	Name() string       // Name of the Schema if named
	Len() int           // Length if array
	Fields() []Field    // Fields if struct
	// Elem if array, slice or pointer (reference). The Elem returns nil
	// for other types and if it's Dynamic reference (because schema of
	// element is not specified by schema)
	Elem() (s Schema)

	RawName() []byte    // raw name if named
	IsRegistered() bool // is registered or not

	Encode() (b []byte) // encode the schema

	// Size of encoded data
	Size(p []byte) (n int, err error)

	fmt.Stringer // String() string
}

var nilSchema Schema = &schema{kind: reflect.Invalid}

// schema core

type schema struct {
	ref SchemaRef

	kind reflect.Kind
	name []byte
}

func (s *schema) IsReference() bool {
	return false
}

func (s *schema) ReferenceType() ReferenceType {
	return ReferenceTypeNone // not a reference
}

func (s *schema) Reference() SchemaRef {
	if s.ref == (SchemaRef{}) {
		s.ref = SchemaRef(cipher.SumSHA256(s.Encode()))
	}
	return s.ref
}

func (s *schema) HasReferences() bool {
	return false
}

func (s *schema) Kind() reflect.Kind {
	return s.kind
}

func (s *schema) Name() string {
	return string(s.name)
}

func (s *schema) RawName() []byte {
	return s.name
}

func (s *schema) IsRegistered() bool {
	return s.kind == reflect.Struct && len(s.name) > 0
}

func (s *schema) Len() int {
	return 0
}

func (s *schema) Fields() []Field {
	return nil
}

func (s *schema) Elem() Schema {
	return nil
}

func (s *schema) Size(p []byte) (n int, err error) {
	switch s.kind {
	case reflect.Bool, reflect.Int8, reflect.Uint8:
		n = 1
	case reflect.Int16, reflect.Uint16:
		n = 2
	case reflect.Int32, reflect.Uint32, reflect.Float32:
		n = 4
	case reflect.Int64, reflect.Uint64, reflect.Float64:
		n = 8
	case reflect.String:
		if n, err = getLength(p); err != nil {
			return
		}
		n += 4 // encoded length (uint32)
	default:
		err = ErrInvalidSchemaOrData
		return
	}
	if n > len(p) {
		err = ErrInvalidSchemaOrData
	}
	return
}

func (s *schema) encodedSchema() (x encodedSchema) {
	x.Kind = uint32(s.kind)
	x.Name = s.name
	return
}

func (s *schema) Encode() (b []byte) {
	b = encoder.Serialize(s.encodedSchema())
	return
}

func (s *schema) String() string {
	if s == nil {
		return "<missing>"
	}
	if len(s.name) > 0 {
		return s.Name()
	}
	return s.kind.String()
}

// reference

type referenceSchema struct {
	schema

	typ  ReferenceType
	elem Schema
}

func (r *referenceSchema) HasReferences() bool {
	return true // by design
}

func (r *referenceSchema) IsRegistered() bool {
	return false // Ref, Refs and Dynamic are not regsitered
}

func (r *referenceSchema) IsReference() bool {
	return true
}

func (r *referenceSchema) ReferenceType() ReferenceType {
	return r.typ
}

func (r *referenceSchema) Reference() SchemaRef {
	if r.ref == (SchemaRef{}) {
		r.ref = SchemaRef(cipher.SumSHA256(r.Encode()))
	}
	return r.ref
}

func (r *referenceSchema) Elem() Schema {
	return r.elem
}

func (r *referenceSchema) Size(p []byte) (n int, err error) {
	switch rt := r.typ; rt {
	case ReferenceTypeSingle:
		n = refSize
	case ReferenceTypeSlice:
		n = refsSize
	case ReferenceTypeDynamic:
		n = dynamicSize
	default:
		err = fmt.Errorf("[ERR] reference with invalid ReferenceType: %d", rt)
		return
	}
	if n > len(p) {
		err = ErrInvalidSchemaOrData
	}
	return
}

func (r *referenceSchema) encodedSchema() (x encodedSchema) {
	x.Kind = uint32(r.kind)
	x.ReferenceType = uint32(r.typ)
	// the schema of the Elem is registered allways
	if r.typ != ReferenceTypeDynamic {
		x.Elem = (&schema{
			SchemaRef{},
			r.elem.Kind(),
			r.elem.RawName(),
		}).Encode()
	}
	return
}

func (r *referenceSchema) Encode() (b []byte) {
	b = encoder.Serialize(r.encodedSchema())
	return
}

func (r *referenceSchema) String() string {
	if r == nil {
		return "<missing>"
	}
	switch r.typ {
	case ReferenceTypeSingle:
		return fmt.Sprintf("*%s", r.Elem().String())
	case ReferenceTypeSlice:
		return fmt.Sprintf("[]*%s", r.Elem().String())
	case ReferenceTypeDynamic:
		return "*(dynamic)"
	}
	return "<invalid>"
}

// slice

type sliceSchema struct {
	schema
	elem Schema
}

func (s *sliceSchema) HasReferences() bool {
	return s.elem.HasReferences()
}

func (s *sliceSchema) Reference() SchemaRef {
	if s.ref == (SchemaRef{}) {
		s.ref = SchemaRef(cipher.SumSHA256(s.Encode()))
	}
	return s.ref
}

func (s *sliceSchema) Elem() Schema {
	return s.elem
}

func (s *sliceSchema) Size(p []byte) (n int, err error) {
	var l int
	if l, err = getLength(p); err != nil {
		return
	}
	n, err = schemaArraySliceSize(s.Elem(), l, 4, p)
	if err == nil && n > len(p) {
		err = ErrInvalidSchemaOrData
	}
	return
}

func (s *sliceSchema) encodedSchema() (x encodedSchema) {
	x = s.schema.encodedSchema()
	if el := s.elem; el.IsRegistered() {
		x.Elem = (&schema{SchemaRef{}, el.Kind(), el.RawName()}).Encode()
	} else {
		x.Elem = s.elem.Encode()
	}
	return
}

func (s *sliceSchema) Encode() (b []byte) {
	b = encoder.Serialize(s.encodedSchema())
	return
}

func (s *sliceSchema) String() string {
	if s == nil {
		return "<missing>"
	}
	if len(s.name) > 0 {
		return s.Name()
	}
	return "[]" + s.elem.String()
}

// array

type arraySchema struct {
	sliceSchema
	length int
}

func (a *arraySchema) Len() int {
	return a.length
}

func (a *arraySchema) Reference() SchemaRef {
	if a.ref == (SchemaRef{}) {
		a.ref = SchemaRef(cipher.SumSHA256(a.Encode()))
	}
	return a.ref
}

func (a *arraySchema) Size(p []byte) (n int, err error) {
	n, err = schemaArraySliceSize(a.Elem(), a.Len(), 0, p)
	if err == nil && n > len(p) {
		err = ErrInvalidSchemaOrData
	}
	return
}

func (a *arraySchema) encodedSchema() (x encodedSchema) {
	x = a.sliceSchema.encodedSchema()
	x.Len = uint32(a.length)
	return
}

func (a *arraySchema) Encode() (b []byte) {
	b = encoder.Serialize(a.encodedSchema())
	return
}

func (a *arraySchema) String() string {
	if a == nil {
		return "<missing>"
	}
	if len(a.name) > 0 {
		return a.Name()
	}
	return fmt.Sprintf("[%d]%s", a.length, a.elem.String())
}

// struct

type structSchema struct {
	schema
	fields []Field
}

func (s *structSchema) HasReferences() (has bool) {
	for _, fl := range s.fields {
		has = has || fl.Schema().HasReferences()
	}
	return
}

func (s *structSchema) Reference() SchemaRef {
	if s.ref == (SchemaRef{}) {
		s.ref = SchemaRef(cipher.SumSHA256(s.Encode()))
	}
	return s.ref
}

func (s *structSchema) Fields() []Field {
	return s.fields
}

func (s *structSchema) Size(p []byte) (n int, err error) {
	var m int
	for _, sf := range s.Fields() {
		if n > len(p) {
			err = ErrInvalidSchemaOrData
			return
		}
		if m, err = sf.Schema().Size(p[n:]); err != nil {
			return
		}
		n += m
	}
	if n > len(p) {
		err = ErrInvalidSchemaOrData
	}
	return
}

func (s *structSchema) encodedSchema() (x encodedSchema) {
	x = s.schema.encodedSchema()
	if len(s.fields) == 0 {
		return
	}
	x.Fields = make([][]byte, 0, len(s.fields))
	for _, f := range s.fields {
		x.Fields = append(x.Fields, f.Encode())
	}
	return
}

func (s *structSchema) Encode() (b []byte) {
	b = encoder.Serialize(s.encodedSchema())
	return
}

//
// Field
//

// A Field represents struct field
type Field interface {
	Schema() Schema     // Schema of the Field
	Kind() reflect.Kind // kind of the Field (short hand)

	Name() string    // Name of the Filed
	RawName() []byte // raw name of the Filed

	Tag() reflect.StructTag // Tag of the Filed
	RawTag() []byte         // raw tag of the Field

	Encode() (b []byte) // Encode field

	fmt.Stringer // String() string
}

// core

type field struct {
	name   []byte
	tag    []byte
	schema Schema
}

func (f *field) Name() string {
	return string(f.name)
}

func (f *field) RawName() []byte {
	return f.name
}

func (f *field) Tag() reflect.StructTag {
	return reflect.StructTag(f.tag)
}

func (f *field) RawTag() []byte {
	return f.tag
}

func (f *field) Schema() Schema {
	return f.schema
}

func (f *field) Kind() reflect.Kind {
	return f.schema.Kind()
}

func (f *field) encodedField() (x encodedField) {
	x.Name = f.name
	x.Tag = f.tag
	x.Schema = f.schema.Encode()
	return
}

func (f *field) Encode() (b []byte) {
	b = encoder.Serialize(f.encodedField())
	return
}

func (f *field) String() string {
	return fmt.Sprintf("%s %s `%s`", f.Name(), f.Schema().String(), f.Tag())
}

// encoded

type encodedSchema struct {
	ReferenceType uint32
	Kind          uint32
	Name          []byte
	Len           uint32
	Fields        [][]byte
	Elem          []byte // encoded schema
}

type encodedField struct {
	Name   []byte
	Tag    []byte
	Kind   uint32
	Schema []byte
}
