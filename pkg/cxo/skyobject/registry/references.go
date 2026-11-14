// Package registry pkg/cxo/skyobject/registry/references.go c2-net-cxo
package registry

import (
	"github.com/skycoin/skycoin/src/cipher"
)

// A RegistryRef represents reference to Registry
type RegistryRef cipher.SHA256

// Short returns short string
func (r RegistryRef) Short() string {
	return cipher.SHA256(r).Hex()[:7]
}

// String implements fmt.Stringer interface
func (r RegistryRef) String() string {
	return cipher.SHA256(r).Hex()
}

// Blank returns true if the RegistryRef is blank
func (r RegistryRef) Blank() bool {
	return r == (RegistryRef{})
}

// A SchemaRef represents reference to Schema
type SchemaRef cipher.SHA256

// Short returns short string
func (s SchemaRef) Short() string {
	return cipher.SHA256(s).Hex()[:7]
}

// String implements fmt.Stringer interface
func (s SchemaRef) String() string {
	return cipher.SHA256(s).Hex()
}

// Blank returns true if the SchemaRef is blank
func (s SchemaRef) Blank() bool {
	return s == (SchemaRef{})
}
