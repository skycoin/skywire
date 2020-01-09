package dmsg

import (
	"fmt"
	"sync"
)

// Entity Errors (10-19).
var (
	ErrEntityClosed               = NewError(10, "local entity closed", nil)
	ErrSessionClosed              = NewError(11, "local session closed", nil)
	ErrCannotConnectToDelegated   = NewError(12, "cannot connect to delegated server", nil)
	ErrSessionHandshakeExtraBytes = NewError(13, "extra bytes received during session handshake", nil)
)

// Errors for dmsg discovery (30-39).
var (
	ErrDiscEntryNotFound       = NewError(30, "discovery entry is not found", nil)
	ErrDiscEntryIsNotServer    = NewError(31, "discovery entry is not of server", nil)
	ErrDiscEntryIsNotClient    = NewError(32, "discovery entry is not of client", nil)
	ErrDiscEntryHasNoDelegated = NewError(33, "discovery client entry has no delegated servers", nil)
)

// Errors for dial request/response (50-69).
var (
	ErrReqInvalidSig       = NewError(50, "request has invalid signature", nil)
	ErrReqInvalidTimestamp = NewError(51, "request timestamp should be higher than last", nil)
	ErrReqInvalidSrcPK     = NewError(52, "request has invalid source public key", nil)
	ErrReqInvalidDstPK     = NewError(53, "request has invalid destination public key", nil)
	ErrReqInvalidSrcPort   = NewError(54, "request has invalid source port", nil)
	ErrReqInvalidDstPort   = NewError(55, "request has invalid destination port", nil)
	ErrReqNoListener       = NewError(56, "request has no associated listener", nil)
	ErrReqNoSession        = NewError(57, "request has no associated session on the dmsg server", nil)

	ErrDialRespInvalidSig  = NewError(60, "response has invalid signature", nil)
	ErrDialRespInvalidHash = NewError(61, "response has invalid hash of associated request", nil)
	ErrDialRespNotAccepted = NewError(62, "response rejected associated request without reason", nil)
)

// Listener errors (80-89).
var (
	ErrPortOccupied    = NewError(80, "port already occupied", nil)
	ErrAcceptChanMaxed = NewError(81, "listener accept chan maxed", nil)
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
	errMap  = make(map[uint8]error)
	codeMap = make(map[error]uint8)
	errMx   sync.RWMutex
)

// NewError creates a new dmsg error.
// - code '0' represents a miscellaneous error and is not saved in 'errMap'.
// - netOpts is only needed if it needs to implement 'net.Error'.
func NewError(code uint8, msg string, netOpts *NetworkErrorOptions) error {
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
func ErrorFromCode(code uint8) (bool, error) {
	errMx.RLock()
	err, ok := errMap[code]
	errMx.RUnlock()
	return ok, err
}

// CodeFromError returns code from a given error.
func CodeFromError(err error) uint8 {
	errMx.RLock()
	code, ok := codeMap[err]
	errMx.RUnlock()
	if !ok {
		return 0
	}
	return code
}
