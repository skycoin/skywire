// Package visor pkg/visor/embedded_tps.go
package visor

import (
	"context"
	"fmt"
	"net/rpc"
	"time"

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

// Serve starts the dmsg RPC listener for accepting transport setup requests from remote visors.
func (tps *embeddedTPS) Serve(ctx context.Context) error {
	port := skyenv.DmsgTransportSetupServicePort
	tps.log.WithField("dmsg_port", port).Info("Starting embedded TPS dmsg listener")

	lis, err := tps.dmsgC.Listen(port)
	if err != nil {
		return fmt.Errorf("failed to listen on dmsg port %d: %w", port, err)
	}

	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			tps.log.WithError(err).Warn("Dmsg listener closed with non-nil error")
		}
	}()

	tps.log.WithField("dmsg_port", port).Info("Accepting dmsg streams for TPS requests")
	for {
		conn, err := lis.AcceptStream()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			tps.log.WithError(err).Error("Failed to accept dmsg stream")
			return err
		}

		remotePK := conn.RawRemoteAddr().PK
		tps.log.WithField("remote", remotePK).Debug("Accepted TPS request connection")

		gw := &TPSRPCGateway{
			tps:      tps,
			remotePK: remotePK,
		}

		rpcS := rpc.NewServer()
		// Register as "SetupRPCGateway" to match what health checks expect
		if err := rpcS.RegisterName("SetupRPCGateway", gw); err != nil {
			tps.log.WithError(err).Error("Failed to register TPS RPC gateway")
			conn.Close() //nolint:errcheck,gosec
			continue
		}

		go rpcS.ServeConn(conn)
	}
}

// TPSRPCGateway handles RPC requests from remote visors wanting transport setup.
type TPSRPCGateway struct {
	tps      *embeddedTPS
	remotePK cipher.PubKey
}

// HealthCheck tests if the TPS is responsive.
func (g *TPSRPCGateway) HealthCheck(_ *TPSHealthCheckArgs, reply *TPSHealthCheckReply) error {
	g.tps.log.WithField("remote", g.remotePK).Debug("Health check request")
	reply.Status = "OK"
	return nil
}

// AddTransport handles transport setup requests from remote visors.
func (g *TPSRPCGateway) AddTransport(req *TPSSetupRequest, reply *TPSSetupResponse) error {
	g.tps.log.WithField("remote", g.remotePK).
		WithField("target", req.TargetPK).
		WithField("to", req.RemotePK).
		Info("AddTransport request via dmsg")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := g.tps.AddTransport(ctx, req.TargetPK, req.RemotePK, types.Type(req.Type))
	if err != nil {
		return err
	}

	reply.ID = resp.ID
	reply.Local = resp.Local
	reply.Remote = resp.Remote
	reply.Type = string(resp.Type)
	return nil
}

// GetTransports retrieves transport list from a target visor.
func (g *TPSRPCGateway) GetTransports(req *TPSGetTransportsRequest, reply *TPSGetTransportsResponse) error {
	g.tps.log.WithField("remote", g.remotePK).
		WithField("target", req.TargetPK).
		Info("GetTransports request via dmsg")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	transports, err := g.tps.GetTransports(ctx, req.TargetPK)
	if err != nil {
		return err
	}

	reply.Transports = make([]TPSSetupResponse, len(transports))
	for i, tp := range transports {
		reply.Transports[i] = TPSSetupResponse{
			ID:     tp.ID,
			Local:  tp.Local,
			Remote: tp.Remote,
			Type:   string(tp.Type),
		}
	}
	return nil
}

// TPSRemoveTransportRequest is input for RemoveTransport RPC.
type TPSRemoveTransportRequest struct {
	TargetPK cipher.PubKey
	ID       uuid.UUID
}

// RemoveTransport is blocked for remote callers - only allowed via local RPC.
func (g *TPSRPCGateway) RemoveTransport(_ *TPSRemoveTransportRequest, _ *struct{}) error {
	g.tps.log.WithField("remote", g.remotePK).Warn("RemoveTransport blocked - not allowed via dmsg")
	return fmt.Errorf("transport removal not allowed via remote interface")
}
