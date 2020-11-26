package vpn

import (
	"errors"
)

var (
	errCouldFindDefaultNetworkGateway = errors.New("could not find default network gateway")
)

type ErrorWithStderr struct {
	Err    error
	Stderr []byte
}

func NewErrorWithStderr(err error, stderr []byte) *ErrorWithStderr {
	return &ErrorWithStderr{
		Err:    err,
		Stderr: stderr,
	}
}

func (e *ErrorWithStderr) Error() string {
	return e.Err.Error() + ": " + string(e.Stderr)
}
