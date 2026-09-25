// Package visor pkg/visor/rpc_client.go c3-vis-core
package visor

import (
	"errors"
)

var (
	// ErrAlreadyServing is returned when an operation fails due to an operation
	// that is currently running.
	ErrAlreadyServing = errors.New("already serving")

	// ErrTimeout represents a timed-out call.
	ErrTimeout = errors.New("rpc client timeout")
)

// StatusMessage defines a status of visor update.
type StatusMessage struct {
	Text    string
	IsError bool
}
