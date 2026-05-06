// Package visor pkg/visor/rpc.go
package visor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/rpc"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

const (
	// RPCPrefix is the prefix used with all RPC calls.
	RPCPrefix = "app-visor"
	// HealthTimeout defines timeout for /health endpoint calls done from hypervisor.
	HealthTimeout = skyenv.HealthTimeout
	// InnerHealthTimeout defines timeout for /health endpoint calls done from visor.
	InnerHealthTimeout = skyenv.InnerHealthTimeout
)

var (
	// ErrNotImplemented occurs when a method is not implemented.
	ErrNotImplemented = errors.New("not implemented")

	// ErrNotFound is returned when a requested resource is not found.
	ErrNotFound = errors.New("not found")
)

type RPC struct {
	visor API
	log   logrus.FieldLogger
}

func newRPCServer(v *Visor, remoteName string) (*rpc.Server, error) {
	rpcS := rpc.NewServer()
	rpcG := &RPC{
		visor: v,
		log:   v.MasterLogger().PackageLogger("visor_rpc:" + remoteName),
	}

	if err := rpcS.RegisterName(RPCPrefix, rpcG); err != nil {
		return nil, fmt.Errorf("failed to create visor RPC server: %w", err)
	}

	return rpcS, nil
}

type AppLogsRequest struct {
	// TimeStamp should be time.RFC3339Nano formatted
	TimeStamp time.Time `json:"time_stamp"`
	// AppName should match the app name in visor config
	AppName string `json:"app_name"`
}
type TransportSummary struct {
	ID      uuid.UUID           `json:"id"`
	Local   cipher.PubKey       `json:"local_pk"`
	Remote  cipher.PubKey       `json:"remote_pk"`
	Type    types.Type          `json:"type"`
	Log     *transport.LogEntry `json:"log,omitempty"`
	IsSetup bool                `json:"is_setup"`
	Label   transport.Label     `json:"label"`
	// LatencyMS is the smoothed average inter-visor RTT for this
	// transport in milliseconds, measured by transport-level
	// ping/pong (or RSN-fallback for old peers). Zero means no
	// measurement yet. Populated from tp.GetLatency().
	LatencyMS float64 `json:"latency_ms,omitempty"`
}
type TransportLogEntry struct {
	TpID      uuid.UUID `json:"tp_id"`
	RecvBytes uint64    `json:"recv_bytes"`
	SentBytes uint64    `json:"sent_bytes"`
	Timestamp int64     `json:"timestamp"`
}

func newTransportSummary(tm *transport.Manager, tp *transport.ManagedTransport, includeLogs, isSetup bool) *TransportSummary {
	summary := &TransportSummary{
		ID:        tp.Entry.ID,
		Local:     tm.Local(),
		Remote:    tp.Remote(),
		Type:      tp.Type(),
		IsSetup:   isSetup,
		Label:     tp.Entry.Label,
		LatencyMS: tp.GetLatency(),
	}
	if includeLogs {
		summary.Log = tp.LogEntry
	}
	return summary
}

type SetAppStatusIn struct {
	AppName string
	Status  string
}
type SetAppErrorIn struct {
	AppName string
	Err     string
}
type StartAppIn struct {
	AppName      string
	LauncherMode string // "internal", "external", or "" for default
}
type SetAppAddIn struct {
	AppName    string
	BinaryName string
}

// AppNameIn carries a single app name for RPCs whose only argument
// is the target's name (DeleteApp, etc.).
type AppNameIn struct {
	AppName string
}
type StartVPNClientIn struct {
	PK           cipher.PubKey
	LauncherMode string // "internal", "external", or "" for default
}
type SetAutoStartIn struct {
	AppName   string
	AutoStart bool
}

// SetAppWhitelistIn carries a comma-separated list of public keys
// allowed to connect to skysocks / vpn-server. Empty = open to all
// authenticated peers. Replaces the prior SetAppPasswordIn — those
// apps no longer accept a passcode flag.
type SetAppWhitelistIn struct {
	AppName   string
	Whitelist string
}
type SetAppNetworkInterfaceIn struct {
	AppName string
	NetIfc  string
}
type SetAppPKIn struct {
	AppName string
	PK      cipher.PubKey
}
type SetAppBoolIn struct {
	AppName string
	Val     bool
}
type SetAppStringIn struct {
	AppName string
	Val     string
}
type SetAppMapIn struct {
	AppName string
	Val     map[string]any
}

// SetAppEnvIn carries a single KEY=value mutation for an app's
// environment. Empty Value deletes the entry.
type SetAppEnvIn struct {
	AppName string
	Key     string
	Value   string
}

// SetAppEnvBatchIn carries a multi-key environment mutation. Map
// values are strings (env vars are KEY=string by definition);
// empty values delete.
type SetAppEnvBatchIn struct {
	AppName string
	Env     map[string]string
}

// SetAppArgsIn replaces the entire Args slice on an app.
type SetAppArgsIn struct {
	AppName string
	Args    []string
}

// SetAppEnvFullIn replaces the entire Env slice on an app.
type SetAppEnvFullIn struct {
	AppName string
	Env     []string
}

// SetAppLauncherModeIn persists the launcher-mode preference.
// Mode is "" / "internal" / "external".
type SetAppLauncherModeIn struct {
	AppName string
	Mode    string
}
type TransportsIn struct {
	FilterTypes   []string
	FilterPubKeys []cipher.PubKey
	ShowLogs      bool
}
type AddTransportIn struct {
	RemotePK         cipher.PubKey
	TpType           string
	Timeout          time.Duration
	Label            string // "user" or "skycoin" (default: "skycoin")
	NoRegister       bool   // skip transport discovery registration (only valid for "user" label)
	SkipLatencyProbe bool   // deprecated: latency is now measured at the transport level
}
type SetSTCPAddrIn struct {
	PK   cipher.PubKey
	Addr string
}
type RouteGroupInfo struct {
	ConsumeRuleID routing.RouteID               `json:"consume_rule_id"`
	FwdRuleID     routing.RouteID               `json:"fwd_rule_id"`
	Desc          routing.RouteDescriptorFields `json:"desc"`
	FwdNextTpID   string                        `json:"fwd_next_tp_id,omitempty"`
	// Initiator is true when this visor dialed the remote end of the
	// route, false when the route was accepted from a remote setup-node
	// request. Distinguishes outbound proxy/VPN client connections from
	// inbound listener connections.
	Initiator bool `json:"initiator"`
	// Hops is the stored forward route path (transport IDs, edges, types)
	// for this route group, populated when the route group is active.
	Hops []RouteHopInfo `json:"hops,omitempty"`
}

// MuxRouteGroupInfo is one route-group's mux state plus per-leg
// counters. Returned by RouteGroupMuxInfo for 'cli proxy mux-info'.
type MuxRouteGroupInfo struct {
	Desc        routing.RouteDescriptorFields `json:"desc"`
	MuxEnabled  bool                          `json:"mux_enabled"`
	SACKEnabled bool                          `json:"sack_enabled"`
	Legs        []MuxLegInfo                  `json:"legs"`
}

// MuxLegInfo is one route in a mux'd group.
type MuxLegInfo struct {
	Index       int     `json:"index"`
	TransportID string  `json:"transport_id"`
	TpType      string  `json:"tp_type"`
	RemotePK    string  `json:"remote_pk"`
	LatencyMS   float64 `json:"latency_ms,omitempty"`
	SentBytes   uint64  `json:"sent_bytes"`
	SentPackets uint64  `json:"sent_packets"`
	RecvBytes   uint64  `json:"recv_bytes"`
	RecvPackets uint64  `json:"recv_packets"`
}
type FetchServiceDataIn struct {
	Service string
	Path    string
}
type MuxRouteInput struct {
	AppName string
	// Forward and Reverse are the caller-supplied hop lists for
	// AddMuxRoute. Same shape 'cli route calc' emits. Empty for
	// RemoveMuxRoute.
	Forward []routing.Hop
	Reverse []routing.Hop
	// TransportID identifies the leg to remove; unused by AddMuxRoute.
	TransportID uuid.UUID
	// SrcPort disambiguates between concurrent rg's owned by the same
	// app (one per concurrent SOCKS5 connection on skysocks-client,
	// etc.). Zero means "auto-pick if exactly one rg is active for
	// the app, error otherwise."
	SrcPort uint16
}
type FilterServersIn struct {
	Version string
	Country string
}
type DeregisterServiceIn struct {
	PKs         []cipher.PubKey
	ServiceType string
}

// SkychatPasswordChangeIn carries the old + new password for the
// hypervisor's "Skychat password" set/change flow. Mirrors the
// shape of usermanager.ChangePassword.
type SkychatPasswordChangeIn struct {
	OldPassword string
	NewPassword string
}

type ConnectIn struct {
	// Network selects the underlying transport for the reverse proxy.
	// "skynet" (default; empty string treated as skynet) or "dmsg".
	Network    string
	RemotePK   cipher.PubKey
	RemotePort int
	LocalPort  int
}
type StopAllPingsOut struct {
	Stopped int
	Errors  []string
}
type DialDmsgPingViaServerIn struct {
	PK       cipher.PubKey
	ServerPK cipher.PubKey
}
type SetEmbeddedProxyEnabledRequest struct {
	Kind   string
	Enable bool
}
type SetEmbeddedProxyUpstreamRequest struct {
	Kind string
	Addr string // upstream SOCKS5 address, "" to clear
}
type DmsgProbeRequest struct {
	PK   cipher.PubKey
	Port uint16
}
type TPSAddTransportIn struct {
	TargetPK cipher.PubKey
	RemotePK cipher.PubKey
	TpType   string
}
type TPSRemoveTransportIn struct {
	TargetPK cipher.PubKey
	TpID     uuid.UUID
}

func (r *RPC) GetTransportSetupNodes(_ *struct{}, out *[]cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetTransportSetupNodes", nil)(out, &err)

	nodes, err := r.visor.GetTransportSetupNodes()
	if err != nil {
		return err
	}
	*out = nodes
	return nil
}
func (r *RPC) GetTransportSetupNodesSorted(_ *struct{}, out *[]cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetTransportSetupNodesSorted", nil)(out, &err)

	nodes, err := r.visor.GetTransportSetupNodesSorted()
	if err != nil {
		return err
	}
	*out = nodes
	return nil
}
func (r *RPC) GetRouteSetupNodesSorted(_ *struct{}, out *[]cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetRouteSetupNodesSorted", nil)(out, &err)

	nodes, err := r.visor.GetRouteSetupNodesSorted()
	if err != nil {
		return err
	}
	*out = nodes
	return nil
}
func (r *RPC) GetTPSHealth(_ *struct{}, out *[]NodeHealth) (err error) {
	defer rpcutil.LogCall(r.log, "GetTPSHealth", nil)(out, &err)

	health, err := r.visor.GetTPSHealth()
	if err != nil {
		return err
	}
	*out = health
	return nil
}
func (r *RPC) GetRSNHealth(_ *struct{}, out *[]NodeHealth) (err error) {
	defer rpcutil.LogCall(r.log, "GetRSNHealth", nil)(out, &err)

	health, err := r.visor.GetRSNHealth()
	if err != nil {
		return err
	}
	*out = health
	return nil
}
func (r *RPC) RouteSetupStats(_ *struct{}, out *setupmetrics.StatsSnapshot) (err error) {
	defer rpcutil.LogCall(r.log, "RouteSetupStats", nil)(out, &err)
	snap, err := r.visor.RouteSetupStats()
	if err != nil {
		return err
	}
	*out = *snap
	return nil
}
func (r *RPC) ResetRouteSetupStats(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "ResetRouteSetupStats", nil)(nil, &err)
	return r.visor.ResetRouteSetupStats()
}

type TPSExternalAddTransportIn struct {
	TPSPK    cipher.PubKey
	TargetPK cipher.PubKey
	RemotePK cipher.PubKey
	TpType   string
}
type TPSExternalGetTransportsIn struct {
	TPSPK    cipher.PubKey
	TargetPK cipher.PubKey
}
type DHTGetIn struct {
	PK   string `json:"pk"`
	Salt string `json:"salt"`
}
type DHTPutIn struct {
	Value []byte `json:"value"`
	Seq   uint64 `json:"seq"`
	Salt  string `json:"salt"`
}

func (r *RPC) AddHypervisor(in *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "AddHypervisor", in)(nil, &err)
	return r.visor.AddHypervisor(*in)
}

type DHTSyncRequest struct {
	RemotePK string `json:"remote_pk"`
	Salt     string `json:"salt"`
}
type TransportRPCCallRequest struct {
	RemotePK cipher.PubKey   `json:"remote_pk"`
	Method   string          `json:"method"`
	Args     json.RawMessage `json:"args,omitempty"` // JSON-encoded RPC arguments
}
type HVAppArgs struct {
	PK      cipher.PubKey
	AppName string
}
type HVMinHopsArgs struct {
	PK   cipher.PubKey
	Hops uint16
}
type HVRewardArgs struct {
	PK   cipher.PubKey
	Addr string
}
type HVTransportArgs struct {
	PK  cipher.PubKey
	TID uuid.UUID
}
type HVRoutingRuleArgs struct {
	PK  cipher.PubKey
	Key routing.RouteID
}
type HVAddTransportArgs struct {
	PK      cipher.PubKey
	Remote  cipher.PubKey
	TpType  string
	Label   string
	Timeout time.Duration
}
type HVAutoconnectArgs struct {
	PK     cipher.PubKey
	Enable bool
}
type HVMuxArgs struct {
	PK cipher.PubKey
	N  int
}
type HVCalcRoutesArgs struct {
	PK     cipher.PubKey
	Enable bool
}
type HVDmsgSessionsArgs struct {
	PK    cipher.PubKey
	Count int
}
type HVLogsArgs struct {
	PK      cipher.PubKey
	Since   time.Time
	AppName string
}
type HVAutostartArgs struct {
	PK        cipher.PubKey
	AppName   string
	Autostart bool
}
type HVProxyArgs struct {
	PK     cipher.PubKey
	Kind   string // "dmsg" or "skynet"
	Enable bool
	Addr   string // SOCKS5 upstream
}
type HVTCPPortArgs struct {
	PK   cipher.PubKey
	Port int
}
type HVForwardedPortArgs struct {
	PK   cipher.PubKey
	Port ForwardedPort
}
