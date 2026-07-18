// Package transport pkg/transport/errors.go c2-net-transport
package transport

import (
	"errors"
)

// ErrWrongNetwork is returned if connection's network differs from
// the one transport is awaiting.
var ErrWrongNetwork = errors.New("wrong network")
