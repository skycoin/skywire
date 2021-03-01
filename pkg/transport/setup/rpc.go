package setup

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/pkg/transport"
)

// TransportGateway that exposes methods to be used via RPC
type TransportGateway struct {
	tm *transport.Manager
}

// TransportRequest to perform an action over RPC
type TransportRequest struct {
	RemotePK cipher.PubKey
	Type     string
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
	// todo: pass transport type "skycoin"
	mt, err := gw.tm.SaveTransport(ctx, req.RemotePK, req.Type)
	if err != nil {
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

// RemoveTransports removes all transports that match given request
func (gw *TransportGateway) RemoveTransports(req TransportRequest, res *BoolResponse) error {
	trIDs := gw.tm.FindTransports(req.RemotePK, req.Type)
	if len(trIDs) == 0 {
		return ErrNotFound
	}
	for _, trID := range trIDs {
		gw.tm.DeleteTransport(trID)
	}
	res.Result = true
	return nil
}
