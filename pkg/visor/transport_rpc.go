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

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// TransportRPCServer serves the visor's RPC over transport virtual streams.
type TransportRPCServer struct {
	log       *logging.Logger
	rpcServer *rpc.Server
	whitelist map[cipher.PubKey]struct{}
	mux       *transport.VStreamMux
	done      chan struct{}
	once      sync.Once
}

// NewTransportRPCServer creates a transport-level RPC server.
// rpcServer is the visor's existing RPC server (same one that serves localhost).
// whitelistPKs controls who can connect — typically the hypervisor PKs.
func NewTransportRPCServer(
	log *logging.Logger,
	rpcServer *rpc.Server,
	whitelistPKs []cipher.PubKey,
	tm *transport.Manager,
) *TransportRPCServer {
	wl := make(map[cipher.PubKey]struct{}, len(whitelistPKs))
	for _, pk := range whitelistPKs {
		wl[pk] = struct{}{}
	}

	mux := transport.NewVStreamMux(tm, routing.VisorRPCPacket, log)
	tm.SetVisorRPCHandler(mux.HandlePacket)

	srv := &TransportRPCServer{
		log:       log,
		rpcServer: rpcServer,
		whitelist: wl,
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
		if _, ok := s.whitelist[remotePK]; !ok {
			s.log.WithField("remote_pk", remotePK.String()[:8]).
				Warn("Transport RPC rejected: PK not in whitelist")
			stream.Close() //nolint:errcheck,gosec
			continue
		}

		s.log.WithField("remote_pk", remotePK.String()[:8]).
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

// UpdateWhitelist replaces the whitelist at runtime.
func (s *TransportRPCServer) UpdateWhitelist(pks []cipher.PubKey) {
	wl := make(map[cipher.PubKey]struct{}, len(pks))
	for _, pk := range pks {
		wl[pk] = struct{}{}
	}
	s.whitelist = wl
}
