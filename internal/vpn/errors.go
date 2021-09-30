package vpn

import "errors"

var (
	errCouldFindDefaultNetworkGateway = errors.New("could not find default network gateway")
	errHandshakeStatusForbidden       = errors.New("password didn't match")
	errHandshakeStatusInternalError   = errors.New("Internal server error")
	errHandshakeNoFreeIPs             = errors.New("No free IPs left to serve")
	errHandshakeStatusBadRequest      = errors.New("Request was malformed")
)
