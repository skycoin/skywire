// Package visor pkg/visor/transport_rpc.go
//
// TransportRPC serves the visor's RPC interface over skywire transports
// using VisorRPCPacket (route ID 0) virtual streams. This allows remote
// management without DMSG or routes — only a direct transport is needed.
//
// Access is controlled by the visor's hypervisor PK whitelist: only PKs
// listed in the config's `hypervisors` field (or the dmsgpty whitelist)
// are permitted to open RPC streams.
package visor

import (
	"net/rpc"
	"sync"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/transport"
)

// TransportRPCServer serves the visor's RPC over transport virtual streams.
type TransportRPCServer struct {
	log       *logging.Logger
	rpcServer *rpc.Server
	whitelist pty.Whitelist
	mux       *transport.VStreamMux
	done      chan struct{}
	once      sync.Once
}

// NewTransportRPCServer creates a transport-level RPC server.
// rpcServer is the visor's existing RPC server (same one that serves localhost).
// whitelist is the visor's shared peer whitelist — consulted live per
// accept, so runtime transitive additions take effect without a restart.
//
// mux MUST be the same VStreamMux that's registered as the transport
// manager's VisorRPCPacket handler (via tm.SetVisorRPCHandler) AND is
// used by TransportRPCCall's outbound dial path. The transport
// manager only routes VisorRPCPacket frames to a single handler;
// dial-side muxes that aren't that handler never receive responses
// and hang. Caller (initApps in init_apps.go) is responsible for
// creating the mux once and threading it through both the server
// (here) and the Visor struct so dial paths use it too.
func NewTransportRPCServer(
	log *logging.Logger,
	rpcServer *rpc.Server,
	whitelist pty.Whitelist,
	mux *transport.VStreamMux,
) *TransportRPCServer {
	srv := &TransportRPCServer{
		log:       log,
		rpcServer: rpcServer,
		whitelist: whitelist,
		mux:       mux,
		done:      make(chan struct{}),
	}

	return srv
}

// Serve starts accepting and serving RPC connections. Blocks until Close.
func (s *TransportRPCServer) Serve() {
	s.log.Info("Transport RPC server started (accepting VisorRPCPacket streams)")
	for {
		stream, err := s.mux.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			s.log.WithError(err).Warn("Transport RPC accept failed")
			return
		}

		remotePK := stream.RemotePK()

		// Check whitelist.
		if ok, err := s.whitelist.Get(remotePK); err != nil || !ok {
			s.log.WithField("remote_pk", remotePK.String()).
				Warn("Transport RPC rejected: PK not in whitelist")
			stream.Close() //nolint:errcheck,gosec
			continue
		}

		s.log.WithField("remote_pk", remotePK.String()).
			Debug("Transport RPC connection accepted")

		go func() {
			s.rpcServer.ServeConn(stream)
			stream.Close() //nolint:errcheck,gosec
		}()
	}
}

// Close stops the server.
func (s *TransportRPCServer) Close() error {
	s.once.Do(func() {
		close(s.done)
		s.mux.Close() //nolint:errcheck,gosec
	})
	return nil
}
