// Package registry pkg/cxo/skyobject/registry/wire_kind.go c2-net-cxo
package registry

import "reflect"

// A schema's kind goes on the wire as a number, and reflect.Kind's numbers
// are the runtime's own: TinyGo orders them differently from Go from String
// on (TinyGo's String is Go's Array, and so on), so a schema encoded by one
// and decoded by the other came back as a different kind, its reference
// hash changed, and a feed published by a Go node could not be filled by a
// TinyGo one ("missng schema"). The wire numbers are Go's, fixed here, so
// nothing already encoded changes; each runtime translates its own.

// wireKinds is reflect.Kind by wire number: Go's order.
var wireKinds = [...]reflect.Kind{
	reflect.Invalid,
	reflect.Bool,
	reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
	reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
	reflect.Float32, reflect.Float64,
	reflect.Complex64, reflect.Complex128,
	reflect.Array, reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
	reflect.Pointer, reflect.Slice, reflect.String, reflect.Struct, reflect.UnsafePointer,
}

// wireNumbers is the wire number of each reflect.Kind of this runtime.
var wireNumbers = func() map[reflect.Kind]uint32 {
	m := make(map[reflect.Kind]uint32, len(wireKinds))
	for n, k := range wireKinds {
		m[k] = uint32(n) //nolint:gosec // fewer than 32
	}
	return m
}()

// kindToWire is k's number on the wire.
func kindToWire(k reflect.Kind) uint32 { return wireNumbers[k] }

// kindFromWire is the reflect.Kind of wire number n; Invalid when unknown.
func kindFromWire(n uint32) reflect.Kind {
	if n < uint32(len(wireKinds)) {
		return wireKinds[n]
	}
	return reflect.Invalid
}
