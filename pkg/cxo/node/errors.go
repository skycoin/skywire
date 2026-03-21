package node

import (
	"errors"
)

// common errors
var (
	ErrAlreadyListen           = errors.New("already listen")
	ErrTimeout                 = errors.New("timeout")
	ErrClosed                  = errors.New("closed")
	ErrNotPublic               = errors.New("not a public server")
	ErrAlreadyHaveConnection   = errors.New("already have connection")
	ErrInvalidResponse         = errors.New("invalid response")
	ErrNoConnectionsToFillFrom = errors.New("no connections to fill from")
	ErrMaxHeadsLimit           = errors.New("max heads limit")
	ErrUnsubscribe             = errors.New("unsubscribe")
	ErrBlankFeed               = errors.New("blank feed")
	ErrConnLimit               = errors.New("max nubmber of connections exceeded")
	ErrPendConnLimit           = errors.New("max number of pending connections exceeded")
)
