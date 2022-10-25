// Package appserver pkg/app/appserver/errors.go
package appserver

// netErr implements `net.Error` to properly
// implement `net.Conn` on app client side.
type netErr struct {
	err       error
	timeout   bool
	temporary bool
}

func (e *netErr) Error() string {
	return e.err.Error()
}

func (e *netErr) Timeout() bool {
	return e.timeout
}

func (e *netErr) Temporary() bool {
	return e.temporary
}
