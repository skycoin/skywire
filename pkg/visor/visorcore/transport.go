// Package visorcore pkg/visor/visorcore/transport.go c3-vis-core
package visorcore

import (
	"context"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TransportManagerDeps is the platform-neutral input to BuildTransportManager.
// The caller builds the platform-specific pieces — the ManagerConfig (native
// wraps its HTTP discovery client with CXO; the browser edge uses a dmsg-only
// tpdclient), and the network.ClientFactory (native pre-enables its TCP/UDP
// port demux, which is not TinyGo-safe, before handing it in) — and this owns
// the NewManager → optional InitDmsgClient → optional Serve → dial-only
// InitClient sequence so the native visor and the wasm edge can't drift on it.
//
// ARClient and EB are typed `any` for the same reason transport.NewManager
// types them so: addrresolver (net/http) and appevent must stay out of the
// TinyGo graph. Native passes its *addrresolver.APIClient / *appevent.Broadcaster;
// the browser edge passes nil ARClient and its appevent broadcaster.
type TransportManagerDeps struct {
	Log      *logging.Logger
	ARClient any
	EB       any
	Config   *transport.ManagerConfig
	Factory  network.ClientFactory

	// DmsgC, when non-nil, is registered as the dmsg transport client
	// (InitDmsgClient) before Serve — the browser edge does this inline; the
	// native visor defers dmsg-client init to its own init_dmsg stage and
	// leaves this nil.
	DmsgC *dmsg.Client

	// Serve starts `go tm.Serve(serveCtx)`. The browser edge sets it; the
	// native visor keeps Serve in its own WaitGroup-wrapped goroutine (its
	// close-stack needs the wg handle for graceful shutdown) and passes false.
	Serve bool

	// DialOnlyClients are transport types registered via InitClient after Serve
	// — the browser edge's {WS, WT, WEBRTC} dial-only set. The native visor
	// registers its (STUN/config-gated) clients from its own init modules and
	// passes nil here.
	DialOnlyClients []types.Type
}

// BuildTransportManager creates the transport.Manager from deps, optionally
// registers the dmsg client, optionally starts it serving, and registers any
// dial-only transport clients. One place owns the NewManager call signature and
// the init/serve ordering, so the native visor and the wasm edge cannot drift
// on it (e.g. a changed NewManager signature, or a forgotten InitClient).
func BuildTransportManager(serveCtx context.Context, deps TransportManagerDeps) (*transport.Manager, error) {
	tm, err := transport.NewManager(deps.Log, deps.ARClient, deps.EB, deps.Config, deps.Factory)
	if err != nil {
		return nil, err
	}
	if deps.DmsgC != nil {
		tm.InitDmsgClient(serveCtx, deps.DmsgC)
	}
	if deps.Serve {
		go tm.Serve(serveCtx)
	}
	for _, t := range deps.DialOnlyClients {
		tm.InitClient(serveCtx, t, 0)
	}
	return tm, nil
}

// ICEURLs prefixes each bare STUN "host:port" with the "stun:" scheme the
// WebRTC stack expects, for network.ClientFactory.ICEURLs. Both the native
// visor and the wasm edge feed their deployment STUN servers through this so
// the WebRTC transport dials identically on either platform.
func ICEURLs(stunServers []string) []string {
	if len(stunServers) == 0 {
		return nil
	}
	out := make([]string, 0, len(stunServers))
	for _, s := range stunServers {
		out = append(out, "stun:"+s)
	}
	return out
}
