// Package cliutil cmd/skywire-cli/cliutil/jsonfilter.go c4-vis-cli
//
// Output post-processors shared by every command that emits JSON via
// PrintOutput:
//
//   - --jq <filter>: run the JSON value through gojq (already vendored)
//     before printing, e.g. `visor info --jq '.ar_registration.entries[].type'`.
//     Implies --json. Lets a coding agent (or human) pull one field out of a
//     large payload instead of dumping and eyeballing it.
//   - --shape: print the SKELETON of the output type — every field at its zero
//     value, with omitempty fields forced visible — instead of live data. Lets
//     you learn a command's output schema without a populated response.
//
// Both hang off PrintOutput's single chokepoint, so all ~68 PrintOutput call
// sites get them for free.
package cliutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/itchyny/gojq"
)

// JQString is the name of the jq-filter flag.
var JQString = "jq"

// ShapeString is the name of the shape flag.
var ShapeString = "shape"

// applyJQ runs filter over the JSON in src and returns the printed results
// (one per line). Object/array results are pretty-printed; scalars print as
// their JSON literal (a quoted string, number, bool), matching jq's default
// (non -r) behavior.
func applyJQ(src []byte, filter string) (string, error) {
	query, err := gojq.Parse(filter)
	if err != nil {
		return "", fmt.Errorf("invalid --jq filter: %w", err)
	}
	var input interface{}
	if err := json.Unmarshal(src, &input); err != nil {
		return "", fmt.Errorf("decode JSON for --jq: %w", err)
	}
	var sb strings.Builder
	iter := query.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := v.(error); ok {
			return "", fmt.Errorf("--jq: %w", err)
		}
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return "", fmt.Errorf("--jq encode result: %w", err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

// orderedMap marshals its entries in insertion order (unlike a Go map, which
// json sorts). Used so a --shape skeleton preserves struct field order.
// MarshalIndent re-indents the compact bytes MarshalJSON returns, so nested
// orderedMaps still pretty-print.
type orderedMap struct {
	keys []string
	vals map[string]interface{}
}

func newOrderedMap() *orderedMap { return &orderedMap{vals: map[string]interface{}{}} }

func (o *orderedMap) set(k string, v interface{}) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

var jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()

// jsonSkeleton returns a shape skeleton for v's type: every exported field at
// its zero value, omitempty ignored so nothing is hidden. Types with a custom
// MarshalJSON (cipher.PubKey, time.Time, uuid.UUID, …) are rendered as their
// zero marshaled form rather than recursed into.
func jsonSkeleton(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	return skeletonType(reflect.TypeOf(v), map[reflect.Type]bool{})
}

func skeletonType(t reflect.Type, seen map[reflect.Type]bool) interface{} {
	if t == nil {
		return nil
	}
	// Custom JSON marshaler → use its zero value's marshaled shape.
	if t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType) {
		if s, ok := marshalZero(t); ok {
			return s
		}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return skeletonType(t.Elem(), seen)
	case reflect.Interface:
		return nil
	case reflect.Struct:
		if seen[t] {
			return "<recursive:" + t.Name() + ">"
		}
		seen[t] = true
		defer delete(seen, t)
		om := newOrderedMap()
		addStructFields(t, om, seen)
		return om
	case reflect.Map:
		inner := newOrderedMap()
		inner.set("<key>", skeletonType(t.Elem(), seen))
		return inner
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return "" // []byte marshals as a base64 string
		}
		return []interface{}{skeletonType(t.Elem(), seen)}
	case reflect.String:
		return ""
	case reflect.Bool:
		return false
	case reflect.Float32, reflect.Float64:
		return 0.0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return 0
	default:
		return nil
	}
}

// addStructFields fills om with the skeleton of t's exported fields, honoring
// json tags and flattening anonymous (embedded) structs the way encoding/json
// does.
func addStructFields(t reflect.Type, om *orderedMap, seen map[reflect.Type]bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		tag := f.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		// Anonymous struct with no explicit json name → promote its fields.
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && !(ft.Implements(jsonMarshalerType) || reflect.PointerTo(ft).Implements(jsonMarshalerType)) {
				addStructFields(ft, om, seen)
				continue
			}
		}
		if name == "" {
			name = f.Name
		}
		om.set(name, skeletonType(f.Type, seen))
	}
}

// marshalZero marshals a zero value of t and decodes it back into a generic
// value so it composes into the skeleton. ok is false if t's zero value can't
// be marshaled (leaves the caller to recurse structurally instead).
func marshalZero(t reflect.Type) (interface{}, bool) {
	b, err := json.Marshal(reflect.Zero(t).Interface())
	if err != nil {
		return nil, false
	}
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, false
	}
	return v, true
}
