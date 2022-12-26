// Package vpn internal/vpn/errors.go
package vpn

import (
	"errors"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	errCouldFindDefaultNetworkGateway = errors.New("could not find default network gateway")
	errHandshakeStatusForbidden       = errors.New("password didn't match")
	errHandshakeStatusInternalError   = errors.New("internal server error")
	errHandshakeNoFreeIPs             = errors.New("no free IPs left to serve")
	errHandshakeStatusBadRequest      = errors.New("request was malformed")
	errTimeout                        = errors.New("internal error: Timeout")
	errNotPermitted                   = errors.New("ioctl: operation not permitted")
	errVPNServerClosed                = errors.New("vpn-server closed")
	errPermissionDenied               = errors.New("permission denied")

	errNoTransportFound = appserver.RPCErr{
		Err: router.ErrNoTransportFound.Error(),
	}
	errTransportNotFound = appserver.RPCErr{
		Err: rfclient.ErrTransportNotFound.Error(),
	}
	errErrSetupNode = appserver.RPCErr{
		Err: setup.ErrSetupNode.Error(),
	}
	errErrServerOffline = appserver.RPCErr{
		Err: appnet.ErrServiceOffline(visorconfig.VPNServerPort).Error(),
	}
)
