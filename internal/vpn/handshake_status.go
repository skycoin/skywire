package vpn

type HandshakeStatus int

const (
	HandshakeStatusOK HandshakeStatus = iota
	HandshakeStatusBadRequest
	HandshakeNoFreeIPs
	HandshakeStatusInternalError
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
	default:
		return "Unknown code"
	}
}
