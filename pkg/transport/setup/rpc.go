package setup

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/transport"
)

// TransportGateway that exposes methods to be used via RPC
type TransportGateway struct {
	tm  *transport.Manager
	log *logging.Logger
}

// TransportRequest to perform an action over RPC
type TransportRequest struct {
	RemotePK cipher.PubKey
	Type     string
}

// UUIDRequest contains id in UUID format
type UUIDRequest struct {
	ID uuid.UUID
}

// TransportResponse specifies an existing transport to remote node
type TransportResponse struct {
	ID     uuid.UUID
	Local  cipher.PubKey
	Remote cipher.PubKey
	Type   string
	IsUp   bool
}

// BoolResponse is a simple boolean wrapped in structure for RPC responses
type BoolResponse struct {
	Result bool
}

// AddTransport specified by request
func (gw *TransportGateway) AddTransport(req TransportRequest, res *TransportResponse) error {
	ctx := context.Background()
	gw.log.WithField("PK", req.RemotePK).WithField("type", req.Type).Info("Adding transport")
	// todo: pass transport type "skycoin"
	mt, err := gw.tm.SaveTransport(ctx, req.RemotePK, req.Type)
	if err != nil {
		gw.log.WithError(err).Error("Cannot save transport")
		return err
	}
	res.ID = mt.Entry.ID
	res.Local = gw.tm.Local()
	res.Remote = mt.Remote()
	res.Type = mt.Type()
	res.IsUp = mt.IsUp()
	return nil
}

// ErrNotFound is returned when requested transport is not found
var ErrNotFound = errors.New("transport not found")

// RemoveTransport removes all transports that match given request
func (gw *TransportGateway) RemoveTransport(req UUIDRequest, res *BoolResponse) error {
	gw.log.WithField("ID", req.ID).Info("Removing transport")
	gw.tm.DeleteTransport(req.ID)
	res.Result = true
	return nil
}
