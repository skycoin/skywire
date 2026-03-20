// Package api pkg/transport-setup/api/rpc_gateway.go
package api

import (
	"context"
	"errors"
	"fmt"
	"net/rpc"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/setup"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// ErrRemoteRemovalNotAllowed is returned when a remote caller tries to remove a transport.
var ErrRemoteRemovalNotAllowed = errors.New("transport removal not allowed via remote interface")

// SetupRPCGateway handles RPC requests from remote visors over dmsg.
type SetupRPCGateway struct {
	api      *API
	remotePK cipher.PubKey
	log      *logging.Logger
}

// HealthCheckArgs is an empty struct for the health check call.
type HealthCheckArgs struct{}

// HealthCheckReply is returned by the HealthCheck RPC method.
type HealthCheckReply struct {
	Status string
}

// HealthCheck tests if the setup node is responsive.
func (g *SetupRPCGateway) HealthCheck(_ *HealthCheckArgs, reply *HealthCheckReply) error {
	g.log.WithField("remote", g.remotePK).Debug("Health check request")
	reply.Status = "OK"
	return nil
}

// TransportSetupRequest is the input for AddTransport RPC.
type TransportSetupRequest struct {
	TargetPK cipher.PubKey // visor to add transport on
	RemotePK cipher.PubKey // other transport edge
	Type     string        // transport type
}

// TransportSetupResponse is the response for AddTransport RPC.
type TransportSetupResponse struct {
	ID     string
	Local  cipher.PubKey
	Remote cipher.PubKey
	Type   string
}

// AddTransport requests a transport be created on the target visor.
func (g *SetupRPCGateway) AddTransport(req *TransportSetupRequest, reply *TransportSetupResponse) error {
	g.log.WithField("remote", g.remotePK).
		WithField("target", req.TargetPK).
		WithField("to", req.RemotePK).
		Info("AddTransport request via dmsg")

	if req.TargetPK == req.RemotePK {
		return fmt.Errorf("source and destination keys are the same")
	}

	// Call the visor via dmsg
	result := &setup.TransportResponse{}
	rpcReq := setup.TransportRequest{RemotePK: req.RemotePK, Type: types.Type(req.Type)}

	if err := g.api.callVisorRPC(context.Background(), req.TargetPK, "TransportGateway.AddTransport", rpcReq, result); err != nil {
		return err
	}

	reply.ID = result.ID.String()
	reply.Local = result.Local
	reply.Remote = result.Remote
	reply.Type = string(result.Type)
	return nil
}

// GetTransportsRequest is the input for GetTransports RPC.
type GetTransportsRequest struct {
	TargetPK cipher.PubKey
}

// GetTransportsResponse is the response for GetTransports RPC.
type GetTransportsResponse struct {
	Transports []TransportSetupResponse
}

// GetTransports retrieves the list of transports from a target visor.
func (g *SetupRPCGateway) GetTransports(req *GetTransportsRequest, reply *GetTransportsResponse) error {
	g.log.WithField("remote", g.remotePK).
		WithField("target", req.TargetPK).
		Info("GetTransports request via dmsg")

	result := &[]setup.TransportResponse{}
	rpcReq := struct{}{}

	if err := g.api.callVisorRPC(context.Background(), req.TargetPK, "TransportGateway.GetTransports", rpcReq, result); err != nil {
		return err
	}

	reply.Transports = make([]TransportSetupResponse, len(*result))
	for i, tp := range *result {
		reply.Transports[i] = TransportSetupResponse{
			ID:     tp.ID.String(),
			Local:  tp.Local,
			Remote: tp.Remote,
			Type:   string(tp.Type),
		}
	}
	return nil
}

// RemoveTransportRequest is the input for RemoveTransport RPC.
type RemoveTransportRequest struct {
	TargetPK cipher.PubKey
	ID       string
}

// RemoveTransport is blocked for remote callers - transport removal only via local HTTP.
func (g *SetupRPCGateway) RemoveTransport(_ *RemoveTransportRequest, _ *struct{}) error {
	g.log.WithField("remote", g.remotePK).Warn("RemoveTransport blocked - not allowed via dmsg")
	return ErrRemoteRemovalNotAllowed
}

// ServeDmsg starts the dmsg RPC listener for the transport setup service.
func (api *API) ServeDmsg(ctx context.Context) error {
	log := api.logger

	// Ensure dmsg client is connected
	go api.dmsgC.Serve(ctx)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-api.dmsgC.Ready():
	}

	port := skyenv.DmsgTransportSetupServicePort
	log.WithField("dmsg_port", port).Info("Starting dmsg listener for transport setup requests")

	lis, err := api.dmsgC.Listen(port)
	if err != nil {
		return fmt.Errorf("failed to listen on dmsg port %d: %w", port, err)
	}

	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			log.WithError(err).Warn("Dmsg listener closed with non-nil error")
		}
	}()

	log.WithField("dmsg_port", port).Info("Accepting dmsg streams")
	for {
		conn, err := lis.AcceptStream()
		if err != nil {
			if errors.Is(err, dmsg.ErrEntityClosed) || ctx.Err() != nil {
				log.Debug("Dmsg client stopped serving")
				return nil
			}
			log.WithError(err).Warn("Failed to accept dmsg stream, continuing...")
			continue
		}

		remotePK := conn.RawRemoteAddr().PK
		log.WithField("remote", remotePK).Debug("Accepted dmsg connection")

		gw := &SetupRPCGateway{
			api:      api,
			remotePK: remotePK,
			log:      log,
		}

		rpcS := rpc.NewServer()
		if err := rpcS.Register(gw); err != nil {
			log.WithError(err).Error("Failed to register RPC gateway")
			conn.Close() //nolint:errcheck,gosec
			continue
		}

		go rpcS.ServeConn(conn)
	}
}
