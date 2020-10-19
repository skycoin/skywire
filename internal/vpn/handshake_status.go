package vpn

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
