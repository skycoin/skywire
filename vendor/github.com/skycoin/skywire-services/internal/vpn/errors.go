package vpn

import (
	"errors"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// VPNErr is used to preserve the type of the errors we return in vpn
type VPNErr struct { //nolint
	Err string
}

func (e VPNErr) Error() string {
	return e.Err
}

var (
	errHandshakeStatusForbidden     = errors.New("password didn't match")
	errHandshakeStatusInternalError = errors.New("internal server error")
	errHandshakeNoFreeIPs           = errors.New("no free IPs left to serve")
	errHandshakeStatusBadRequest    = errors.New("request was malformed")
	errTimeout                      = errors.New("internal error: Timeout")
	// ErrNotPermitted is a ioctl error
	ErrNotPermitted    = errors.New("ioctl: operation not permitted")
	errVPNServerClosed = errors.New("vpn-server closed")

	errNoTransportFound = appserver.RPCErr{
		Err: router.ErrNoTransportFound.Error(),
	}
	errTransportNotFound = appserver.RPCErr{
		Err: rfclient.ErrTransportNotFound.Error(),
	}
	// ErrSetupNode is setupclient.ErrSetupNode error wrapped in appserver.RPCErr
	ErrSetupNode = appserver.RPCErr{
		Err: router.ErrSetupNode.Error(),
	}
	// ErrServerOffline is sent by server visor when the vpn-server is offline
	ErrServerOffline = appserver.RPCErr{
		Err: appnet.ErrServiceOffline(skyenv.VPNServerPort).Error(),
	}
)
