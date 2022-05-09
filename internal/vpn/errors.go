package vpn

import (
	"errors"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/setup/setupclient"
)

var (
	errCouldFindDefaultNetworkGateway = errors.New("Could not find default network gateway")
	errHandshakeStatusForbidden       = errors.New("Password didn't match")
	errHandshakeStatusInternalError   = errors.New("Internal server error")
	errHandshakeNoFreeIPs             = errors.New("No free IPs left to serve")
	errHandshakeStatusBadRequest      = errors.New("Request was malformed")
	errTimeout                        = errors.New("Internal error: Timeout")
	errNotPermited                    = errors.New("ioctl: operation not permitted")

	errNoTransportFound = appserver.RPCErr{
		Err: router.ErrNoTransportFound.Error(),
	}
	errTransportNotFound = appserver.RPCErr{
		Err: rfclient.ErrTransportNotFound.Error(),
	}
	errErrSetupNode = appserver.RPCErr{
		Err: setupclient.ErrSetupNode.Error(),
	}
)
