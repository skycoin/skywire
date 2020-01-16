package dmsg

import (
	"fmt"
	"sync"
)

// Errors for dmsg discovery (1xx).
var (
	ErrDiscEntryNotFound       = NewError(100, "discovery entry is not found", nil)
	ErrDiscEntryIsNotServer    = NewError(101, "discovery entry is not of server", nil)
	ErrDiscEntryIsNotClient    = NewError(102, "discovery entry is not of client", nil)
	ErrDiscEntryHasNoDelegated = NewError(103, "discovery client entry has no delegated servers", nil)
)

// Entity Errors (2xx).
var (
	ErrEntityClosed               = NewError(200, "local entity closed", nil)
	ErrSessionClosed              = NewError(201, "local session closed", nil)
	ErrCannotConnectToDelegated   = NewError(202, "cannot connect to delegated server", nil)
	ErrSessionHandshakeExtraBytes = NewError(203, "extra bytes received during session handshake", nil)
)

// Errors for dial request/response (3xx).
var (
	ErrReqInvalidSig       = NewError(300, "request has invalid signature", nil)
	ErrReqInvalidTimestamp = NewError(301, "request timestamp should be higher than last", nil)
	ErrReqInvalidSrcPK     = NewError(302, "request has invalid source public key", nil)
	ErrReqInvalidDstPK     = NewError(303, "request has invalid destination public key", nil)
	ErrReqInvalidSrcPort   = NewError(304, "request has invalid source port", nil)
	ErrReqInvalidDstPort   = NewError(305, "request has invalid destination port", nil)
	ErrReqNoListener       = NewError(306, "request has no associated listener", nil)
	ErrReqNoSession        = NewError(307, "request has no associated session on the dmsg server", nil)

	ErrDialRespInvalidSig  = NewError(350, "response has invalid signature", nil)
	ErrDialRespInvalidHash = NewError(351, "response has invalid hash of associated request", nil)
	ErrDialRespNotAccepted = NewError(352, "response rejected associated request without reason", nil)

	ErrSignedObjectInvalid = NewError(370, "signed object is invalid", nil)
)

// Listener errors (4xx).
var (
	ErrPortOccupied    = NewError(400, "port already occupied", nil)
	ErrAcceptChanMaxed = NewError(401, "listener accept chan maxed", nil)
)

// NetworkErrorOptions provides 'timeout' and 'temporary' options for NetworkError.
type NetworkErrorOptions struct {
	Timeout   bool
	Temporary bool
}

// NetworkError implements 'net.Error'.
type NetworkError struct {
	Err  error
	Opts NetworkErrorOptions
}

// Error implements error
func (err NetworkError) Error() string { return err.Err.Error() }

// Timeout implements net.Error
func (err NetworkError) Timeout() bool { return err.Opts.Timeout }

// Temporary implements net.Error
func (err NetworkError) Temporary() bool { return err.Opts.Temporary }

var (
	errFmt  = "code %d - %s"
	errMap  = make(map[uint16]error)
	codeMap = make(map[error]uint16)
	errMx   sync.RWMutex
)

// NewError creates a new dmsg error.
// - code '0' represents a miscellaneous error and is not saved in 'errMap'.
// - netOpts is only needed if it needs to implement 'net.Error'.
func NewError(code uint16, msg string, netOpts *NetworkErrorOptions) error {
	// No need to check errMap if code 0.
	if code != 0 {
		errMx.Lock()
		defer errMx.Unlock()
		if _, ok := errMap[code]; ok {
			panic(fmt.Errorf("error of code %d already exists", code))
		}
	}
	err := fmt.Errorf(errFmt, code, msg)
	if netOpts != nil {
		err = &NetworkError{Err: err, Opts: *netOpts}
	}
	// Don't save error if code is '0'.
	if code != 0 {
		errMap[code] = err
		codeMap[err] = code
	}
	return err
}

// ErrorFromCode returns a saved error (if exists) from given error code.
func ErrorFromCode(code uint16) (bool, error) {
	errMx.RLock()
	err, ok := errMap[code]
	errMx.RUnlock()
	return ok, err
}

// CodeFromError returns code from a given error.
func CodeFromError(err error) uint16 {
	errMx.RLock()
	code, ok := codeMap[err]
	errMx.RUnlock()
	if !ok {
		return 0
	}
	return code
}
