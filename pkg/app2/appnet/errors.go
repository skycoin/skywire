package appnet

import "errors"

var (
	// ErrPortAlreadyBound is being returned when the desired port is already bound to.
	ErrPortAlreadyBound = errors.New("port already bound")
)
