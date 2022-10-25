// Package vpn internal/vpn/handshake_status.go
package vpn

import "errors"

// HandshakeStatus is a status of Client/Server handshake.
type HandshakeStatus int

const (
	// HandshakeStatusOK is returned on successful handshake.
	HandshakeStatusOK HandshakeStatus = iota
	// HandshakeStatusBadRequest is returned if Client hello message was malformed.
	HandshakeStatusBadRequest
	// HandshakeNoFreeIPs is returned if no free IPs left to assign to TUNs.
	HandshakeNoFreeIPs
	// HandshakeStatusInternalError is returned in all other cases when some server error occurred.
	HandshakeStatusInternalError
	// HandshakeStatusForbidden is returned if client had sent the wrong passcode.
	HandshakeStatusForbidden
)

func (hs HandshakeStatus) String() string {
	switch hs {
	case HandshakeStatusOK:
		return "OK"
	case HandshakeStatusBadRequest:
		return "Request was malformed"
	case HandshakeNoFreeIPs:
		return "No free IPs left to serve"
	case HandshakeStatusInternalError:
		return "Internal server error"
	case HandshakeStatusForbidden:
		return "Forbidden"
	default:
		return "Unknown code"
	}
}

func (hs HandshakeStatus) getError() error {
	switch hs {
	case HandshakeStatusOK:
		return nil
	case HandshakeStatusBadRequest:
		return errHandshakeStatusBadRequest
	case HandshakeNoFreeIPs:
		return errHandshakeNoFreeIPs
	case HandshakeStatusInternalError:
		return errHandshakeStatusInternalError
	case HandshakeStatusForbidden:
		return errHandshakeStatusForbidden
	default:
		return errors.New("Unknown error code")
	}
}
