package statutil

import (
	"fmt"
	"strings"
)

// A Volume represents size of an
// object or many objects in bytes.
// The Volume based on uint32
type Volume uint32

var units = [...]string{
	"", "k", "M", "G", "T", "P", "E", "Z", "Y",
}

// String implements fmt.String interface
// and returns human-readable string
// represents the Volume. Prefixes for
// the Volume means 1024 (instad of 1000).
// E.g. 1kB is 1024B
func (v Volume) String() (s string) {

	fv := float64(v)

	var i int
	for ; fv >= 1024.0; i++ {
		fv /= 1024.0
	}

	s = fmt.Sprintf("%.2f", fv)
	s = strings.TrimRight(s, "0") // remove trailing zeroes (x.10, x.00)
	s = strings.TrimRight(s, ".") // remove trailing dot (x.)
	s += units[i] + "B"           //

	return
}

// An Amount represents amount of
// items. The Amount based on uint32
type Amount uint32

// String implements fmt.String interface
// and returns human-readable string
// represents the Amount. Prefixes for
// the Amount means 1000. E.g. 1k is
// 1000 items
func (a Amount) String() (s string) {

	fa := float64(a)

	var i int
	for ; fa >= 1000.0; i++ {
		fa /= 1000.0
	}

	s = fmt.Sprintf("%.2f", fa)
	s = strings.TrimRight(s, "0") // remove trailing zeroes (x.10, x.00)
	s = strings.TrimRight(s, ".") // remove trailing dot (x.)
	s += units[i]                 //

	return
}
