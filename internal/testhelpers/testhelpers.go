// Package testhelpers provides helpers for testing.
package testhelpers

import (
	"errors"
)

// NoErr is used with the mock interface to return from its methods.
var NoErr error

// Err is used with the mock interface to return some error from its methods.
var Err = errors.New("error")
