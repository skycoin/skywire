// Package wasmhv pkg/wasmhv/rpc_gateway.go c3-vis-wasm
package wasmhv

import (
	"errors"
	"net/rpc"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TransportController is the control surface the gateway needs beyond the
// read-only SelfProvider: creating and removing the tab's own transports. The
// wasm-visor implements it over its transport.Manager. May be nil (then the
// transport-control methods return an error rather than panic).
type TransportController interface {
	AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration) (*TransportSummary, error)
	RemoveTransport(id uuid.UUID) error
}

// TransportsIn mirrors visor.TransportsIn (the Transports RPC arg).
type TransportsIn struct {
	FilterTypes   []string
	FilterPubKeys []cipher.PubKey
	ShowLogs      bool
}

// AddTransportIn mirrors visor.AddTransportIn (the AddTransport RPC arg).
type AddTransportIn struct {
	RemotePK         cipher.PubKey
	TpType           string
	Timeout          time.Duration
	Label            string
	NoRegister       bool
	SkipLatencyProbe bool
}

// RPCGateway adapts the wasm-visor's self view to the visor's net/rpc surface
// (the "app-visor.*" methods `skywire cli` calls). It is registered on an
// rpc.Server whose connections arrive over the wasmrpc bridge (one yamux stream
// per cli connection), so the ordinary CLI can drive a browser tab.
//
// The reply types are the wasmhv mirrors (Summary/Overview/HealthInfo/…). gob
// matches fields by name, so the CLI decodes them straight into the real
// pkg/visor types — the same trick the HV UI core uses (gob_mirror_test.go
// round-trips both directions, so a field drift fails a test).
//
// Only the read-side "info" methods are implemented so far; transport/route
// control methods are added incrementally. Unknown methods simply aren't
// registered, so the CLI gets a clear "can't find method" rather than a wrong
// answer.
type RPCGateway struct {
	self SelfProvider
	ctl  TransportController
}

// errNoSelf is returned when the tab has no local visor wired (not booted).
var errNoSelf = errors.New("wasm-visor: not booted (no self provider)")

// errNoControl is returned when a control method is called but no controller is
// wired (read-only mode).
var errNoControl = errors.New("wasm-visor: transport control not available")

// NewRPCServer builds an rpc.Server exposing the gateway under the "app-visor"
// name (the prefix the CLI uses). self provides the read views; ctl (optional,
// may be nil) provides transport control. Feed its connections via ServeConn —
// e.g. from wasmrpc.ServeTab(conn, srv.ServeConn).
func NewRPCServer(self SelfProvider, ctl TransportController) (*rpc.Server, error) {
	srv := rpc.NewServer()
	if err := srv.RegisterName("app-visor", &RPCGateway{self: self, ctl: ctl}); err != nil {
		return nil, err
	}
	return srv, nil
}

// Transports returns the tab's transports (the CLI's `tp ls`). Filters are
// applied if given; an empty filter returns all.
func (g *RPCGateway) Transports(in *TransportsIn, out *[]*TransportSummary) error {
	if g.self == nil {
		return errNoSelf
	}
	all := g.self.SelfTransports()
	res := make([]*TransportSummary, 0, len(all))
	for _, t := range all {
		if t == nil || !matchTransport(t, in) {
			continue
		}
		res = append(res, t)
	}
	*out = res
	return nil
}

// matchTransport applies a TransportsIn filter (empty filter = match all).
func matchTransport(t *TransportSummary, in *TransportsIn) bool {
	if in == nil {
		return true
	}
	if len(in.FilterTypes) > 0 {
		ok := false
		for _, ty := range in.FilterTypes {
			if ty == t.Type {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(in.FilterPubKeys) > 0 {
		ok := false
		for _, pk := range in.FilterPubKeys {
			if pk == t.Remote {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// AddTransport creates a transport from this tab to a remote visor (the CLI's
// `tp add`). webrtc/dmsg work over the CLI (no endpoint needed); ws/wt need an
// endpoint and must use skywireVisor.dialTransport instead.
func (g *RPCGateway) AddTransport(in *AddTransportIn, out *TransportSummary) error {
	if g.ctl == nil {
		return errNoControl
	}
	ts, err := g.ctl.AddTransport(in.RemotePK, in.TpType, in.Timeout)
	if err != nil {
		return err
	}
	if ts != nil {
		*out = *ts
	}
	return nil
}

// RemoveTransport tears down a transport by ID (the CLI's `tp rm`).
func (g *RPCGateway) RemoveTransport(tid *uuid.UUID, _ *struct{}) error {
	if g.ctl == nil {
		return errNoControl
	}
	return g.ctl.RemoveTransport(*tid)
}

// IsStartupComplete reports whether the tab's visor is up.
func (g *RPCGateway) IsStartupComplete(_ *struct{}, out *bool) error {
	*out = g.self != nil
	return nil
}

// Health reports the tab visor as healthy whenever it's booted (the browser tab
// being alive is, by definition, the service being up).
func (g *RPCGateway) Health(_ *struct{}, out *HealthInfo) error {
	if g.self == nil {
		return errNoSelf
	}
	*out = HealthInfo{ServicesHealth: "healthy"}
	return nil
}

// Summary returns the tab visor's summary (PK, transports, …).
func (g *RPCGateway) Summary(_ *struct{}, out *Summary) error {
	if g.self == nil {
		return errNoSelf
	}
	*out = g.self.SelfSummary()
	return nil
}

// Overview returns the tab visor's overview (the dashboard's top-level view).
func (g *RPCGateway) Overview(_ *struct{}, out *Overview) error {
	if g.self == nil {
		return errNoSelf
	}
	*out = g.self.SelfOverview()
	return nil
}
