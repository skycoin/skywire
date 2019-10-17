package app2

import (
	"errors"
)

var (
	// errMethodNotImplemented serves as a return value for non-implemented funcs (stubs).
	errMethodNotImplemented = errors.New("method not implemented")
)
