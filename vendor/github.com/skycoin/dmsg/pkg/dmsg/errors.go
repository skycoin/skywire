// Package dmsg pkg/dmsg/errors.go
package dmsg

import (
	"fmt"
	"sync"
)

// Errors for dmsg discovery (1xx).
var (
	ErrDiscEntryNotFound       = registerErr(Error{code: 100, msg: "entry is not found in discovery"})
	ErrDiscEntryIsNotServer    = registerErr(Error{code: 101, msg: "entry is not of server in discovery"})
	ErrDiscEntryIsNotClient    = registerErr(Error{code: 102, msg: "entry is not of client in discovery"})
	ErrDiscEntryHasNoDelegated = registerErr(Error{code: 103, msg: "client entry in discovery has no delegated servers"})
)

// Entity Errors (2xx).
var (
	ErrEntityClosed               = registerErr(Error{code: 200, msg: "local entity closed"})
	ErrSessionClosed              = registerErr(Error{code: 201, msg: "local session closed"})
	ErrCannotConnectToDelegated   = registerErr(Error{code: 202, msg: "cannot connect to delegated server"})
	ErrSessionHandshakeExtraBytes = registerErr(Error{code: 203, msg: "extra bytes received during session handshake"})
)

// Errors for dial request/response (3xx).
var (
	ErrReqInvalidSig       = registerErr(Error{code: 300, msg: "request has invalid signature"})
	ErrReqInvalidTimestamp = registerErr(Error{code: 301, msg: "request timestamp should be higher than last"})
	ErrReqInvalidSrcPK     = registerErr(Error{code: 302, msg: "request has invalid source public key"})
	ErrReqInvalidDstPK     = registerErr(Error{code: 303, msg: "request has invalid destination public key"})
	ErrReqInvalidSrcPort   = registerErr(Error{code: 304, msg: "request has invalid source port"})
	ErrReqInvalidDstPort   = registerErr(Error{code: 305, msg: "request has invalid destination port"})
	ErrReqNoListener       = registerErr(Error{code: 306, msg: "request has no associated listener", temp: true})
	ErrReqNoNextSession    = registerErr(Error{code: 307, msg: "request cannot be forwarded because the next session is non-existent"})

	ErrDialRespInvalidSig  = registerErr(Error{code: 350, msg: "response has invalid signature"})
	ErrDialRespInvalidHash = registerErr(Error{code: 351, msg: "response has invalid hash of associated request"})
	ErrDialRespNotAccepted = registerErr(Error{code: 352, msg: "response rejected associated request without reason"})

	ErrSignedObjectInvalid = registerErr(Error{code: 370, msg: "signed object is invalid"})
)

// Listener errors (4xx).
var (
	ErrPortOccupied    = registerErr(Error{code: 400, msg: "port already occupied"})
	ErrAcceptChanMaxed = registerErr(Error{code: 401, msg: "listener accept chan maxed", temp: true})
)

// ErrorFromCode returns a saved error (if exists) from given error code.
func ErrorFromCode(code errorCode) (bool, error) {
	errMx.RLock()
	err, ok := errMap[code]
	errMx.RUnlock()
	return ok, err
}

type errorCode uint16

var (
	errMap = make(map[errorCode]error)
	errMx  sync.RWMutex
)

func registerErr(e Error) Error {
	e.nxt = nil

	errMx.Lock()
	if _, ok := errMap[e.code]; ok {
		panic(fmt.Errorf("error of code %d already exists", e.code))
	}
	errMap[e.code] = e
	errMx.Unlock()

	return e
}

// Error represents a dmsg-related error.
type Error struct {
	code    errorCode
	msg     string
	timeout bool
	temp    bool
	nxt     error
}

// Error implements error
func (e Error) Error() string {
	return fmt.Sprintf("dmsg error %d - %s", e.code, e.errorString())
}

func (e Error) errorString() string {
	msg := e.msg
	if e.nxt != nil {
		if nxt, ok := e.nxt.(Error); ok {
			msg += ": " + nxt.errorString()
		} else {
			msg += ": " + e.nxt.Error()
		}
	}
	return msg
}

// Timeout implements net.Error
func (e Error) Timeout() bool {
	return e.timeout
}

// Temporary implements net.Error
func (e Error) Temporary() bool {
	return e.temp
}

// Wrap wraps an error and returns the new error.
func (e Error) Wrap(err error) Error {
	e.nxt = err
	return e
}
