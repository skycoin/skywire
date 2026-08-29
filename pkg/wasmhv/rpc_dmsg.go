// Package wasmhv pkg/wasmhv/rpc_dmsg.go c3-vis-wasm
package wasmhv

import (
	"context"
	"net/rpc"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// ServeRPCOverDmsg serves the wasm-visor's own RPC gateway (srv, built by
// NewRPCServer under the "app-visor" prefix) over dmsg on the standard always-on
// visor-RPC port (skyenv.DmsgVisorRPCPort) — the SAME port and codec the native
// visor uses (pkg/visor init_apps.go). So `skywire cli ... visor state --via
// dmsg://<pk>` (and every other gateway method) reaches a browser leaf the way it
// reaches a native visor, instead of only over the local wasmrpc WebSocket bridge.
//
// Auth: each accepted stream's remote PK must be in `authorized`; others are
// dropped at accept. Fail-closed — an empty authorized set disables the server
// (returns nil without listening), matching the native visor, which disables its
// dmsg visor-RPC server when no PKs are authorized. Build-tag-free so the exact
// serving path is exercised by a native dmsg round-trip test.
//
// Blocks until the listener errors or ctx is done; run it in its own goroutine.
func ServeRPCOverDmsg(ctx context.Context, dmsgC *dmsg.Client, srv *rpc.Server, authorized map[cipher.PubKey]bool, log *logging.Logger) error {
	if dmsgC == nil || srv == nil || len(authorized) == 0 {
		if log != nil {
			log.Debug("dmsg visor-RPC server disabled (no dmsg client, server, or authorized PKs)")
		}
		return nil
	}
	lis, err := dmsgC.Listen(skyenv.DmsgVisorRPCPort)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		lis.Close() //nolint:errcheck,gosec
	}()
	if log != nil {
		log.Infof("dmsg visor-RPC server listening on port %d (%d authorized PK(s))", skyenv.DmsgVisorRPCPort, len(authorized))
	}
	for {
		stream, err := lis.AcceptStream()
		if err != nil {
			if strings.Contains(err.Error(), "closed") {
				return nil
			}
			return err
		}
		remote := stream.RawRemoteAddr().PK
		if !authorized[remote] {
			if log != nil {
				log.Debugf("dmsg visor-RPC: rejecting unauthorized peer %s", remote)
			}
			stream.Close() //nolint:errcheck,gosec
			continue
		}
		go func(s *dmsg.Stream) {
			defer s.Close() //nolint:errcheck,gosec
			srv.ServeConn(s)
		}(stream)
	}
}
