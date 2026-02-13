// Package visor pkg/visor/embedded_tps.go
package visor

import (
	"context"
	"fmt"
	"net/rpc"

	"github.com/google/uuid"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	ts "github.com/skycoin/skywire/pkg/transport/setup"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// embeddedTPS holds the state of an embedded Transport Setup Node.
// It uses a separate dmsg client with its own PK/SK identity to dial
// remote visors on DmsgTransportSetupPort and issue RPC commands.
type embeddedTPS struct {
	dmsgC *dmsg.Client
	pk    cipher.PubKey
	log   *logging.Logger
}

// AddTransport dials a remote visor via dmsg and asks it to create a transport
// to remotePK of the given type.
func (tps *embeddedTPS) AddTransport(ctx context.Context, targetPK, remotePK cipher.PubKey, tpType types.Type) (*ts.TransportResponse, error) {
	client, err := tps.dialRPC(ctx, targetPK)
	if err != nil {
		return nil, err
	}
	defer client.Close() //nolint:errcheck

	req := ts.TransportRequest{RemotePK: remotePK, Type: tpType}
	var res ts.TransportResponse
	if err := client.Call("TransportGateway.AddTransport", req, &res); err != nil {
		return nil, fmt.Errorf("RPC AddTransport: %w", err)
	}
	return &res, nil
}

// RemoveTransport dials a remote visor and removes a transport by ID.
func (tps *embeddedTPS) RemoveTransport(ctx context.Context, targetPK cipher.PubKey, tpID uuid.UUID) error {
	client, err := tps.dialRPC(ctx, targetPK)
	if err != nil {
		return err
	}
	defer client.Close() //nolint:errcheck

	req := ts.UUIDRequest{ID: tpID}
	var res ts.BoolResponse
	if err := client.Call("TransportGateway.RemoveTransport", req, &res); err != nil {
		return fmt.Errorf("RPC RemoveTransport: %w", err)
	}
	return nil
}

// GetTransports dials a remote visor and retrieves its transport list.
func (tps *embeddedTPS) GetTransports(ctx context.Context, targetPK cipher.PubKey) ([]ts.TransportResponse, error) {
	client, err := tps.dialRPC(ctx, targetPK)
	if err != nil {
		return nil, err
	}
	defer client.Close() //nolint:errcheck

	var res []ts.TransportResponse
	if err := client.Call("TransportGateway.GetTransports", struct{}{}, &res); err != nil {
		return nil, fmt.Errorf("RPC GetTransports: %w", err)
	}
	return res, nil
}

// dialRPC dials the target visor on DmsgTransportSetupPort and returns an RPC client.
func (tps *embeddedTPS) dialRPC(ctx context.Context, targetPK cipher.PubKey) (*rpc.Client, error) {
	tps.log.WithField("target", targetPK).Debug("Dialing remote visor via dmsg for TPS RPC")
	conn, err := tps.dmsgC.Dial(ctx, dmsg.Addr{PK: targetPK, Port: skyenv.DmsgTransportSetupPort})
	if err != nil {
		return nil, fmt.Errorf("dmsg dial %s: %w", targetPK, err)
	}
	return rpc.NewClient(conn), nil
}
