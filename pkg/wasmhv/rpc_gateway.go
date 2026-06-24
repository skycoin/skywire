package wasmhv

import (
	"errors"
	"net/rpc"
)

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
}

// errNoSelf is returned when the tab has no local visor wired (not booted).
var errNoSelf = errors.New("wasm-visor: not booted (no self provider)")

// NewRPCServer builds an rpc.Server exposing the gateway under the "app-visor"
// name (the prefix the CLI uses). Feed its connections via ServeConn — e.g. from
// wasmrpc.ServeTab(conn, srv.ServeConn).
func NewRPCServer(self SelfProvider) (*rpc.Server, error) {
	srv := rpc.NewServer()
	if err := srv.RegisterName("app-visor", &RPCGateway{self: self}); err != nil {
		return nil, err
	}
	return srv, nil
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
