// Package registry pkg/cxo/skyobject/registry/wire_kind_test.go c2-net-cxo
package registry

import (
	"reflect"
	"testing"
)

// Under Go the wire numbers are reflect.Kind's own, so what was encoded
// before the table existed decodes the same.
func TestWireKindsAreGos(t *testing.T) {
	for n, k := range wireKinds {
		if uint(k) != uint(n) {
			t.Errorf("wire %d = %v, Go numbers it %d", n, k, uint(k))
		}
		if got := kindFromWire(kindToWire(k)); got != k {
			t.Errorf("%v round-trips as %v", k, got)
		}
	}
	if len(wireKinds) != int(reflect.UnsafePointer)+1 {
		t.Errorf("%d wire kinds, Go has %d", len(wireKinds), int(reflect.UnsafePointer)+1)
	}
	if got := kindFromWire(uint32(len(wireKinds))); got != reflect.Invalid {
		t.Errorf("an unknown wire number decodes as %v", got)
	}
}
