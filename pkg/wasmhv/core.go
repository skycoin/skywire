// Package wasmhv is a minimal, wasm-compilable hypervisor "core": it accepts
// visor dials over dmsg (port 46, like a normal hypervisor), runs the gob RPC
// client against each dialed-in visor, and serves the hypervisor's Angular /api
// surface. It deliberately does NOT import pkg/visor (which doesn't compile to
// js/wasm — godbus/bbolt/router/...); instead it declares MINIMAL MIRROR TYPES
// of the visor API responses. gob decodes by field name and ignores the rest,
// so we only mirror the fields the UI reads. This is what lets a browser tab be
// a real standalone hypervisor that visors dial into.
package wasmhv

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// jsonMarshal is json.Marshal (named so Overview.MarshalJSON reads clearly).
var jsonMarshal = json.Marshal

// rpcService is the net/rpc service name the visor registers (visor.RPCPrefix).
const rpcService = "app-visor"

// DmsgHypervisorPort is the dmsg port visors dial their hypervisor on
// (mirror of skyenv.DmsgHypervisorPort, kept local to avoid a non-wasm import).
const DmsgHypervisorPort = 46

// --- minimal mirror types (gob field NAMES + json tags match pkg/visor.*) ---

// About mirrors visor.About.
type About struct {
	PubKey        cipher.PubKey   `json:"public_key"`
	Build         *buildinfo.Info `json:"build"`
	DmsgConnected bool            `json:"dmsg_connected"`
	DmsgSessions  int             `json:"dmsg_sessions"`
}

// HealthInfo mirrors visor.HealthInfo.
type HealthInfo struct {
	ServicesHealth         string `json:"services_health"`
	UptimeTrackerHealth    string `json:"uptime_tracker_health,omitempty"`
	AutoconnectHealth      string `json:"autoconnect_health,omitempty"`
	TransportabilityHealth string `json:"transportability_health,omitempty"`
}

// Overview mirrors the scalar fields of visor.Overview. Complex nested fields
// (Apps, Transports) are intentionally ABSENT from the struct: their element
// types ([]*appserver.AppState etc.) don't compile to wasm, and — critically —
// declaring them with a mismatched type (e.g. []struct{}) makes gob FAIL the
// whole decode. Omitting them lets gob skip those fields and decode the scalars.
// MarshalJSON re-adds empty apps/transports arrays so the UI templates that
// index them don't choke on a missing key.
type Overview struct {
	PubKey              cipher.PubKey   `json:"local_pk"`
	BuildInfo           *buildinfo.Info `json:"build_info"`
	AppProtoVersion     string          `json:"app_protocol_version"`
	RoutesCount         int             `json:"routes_count"`
	LocalIP             string          `json:"local_ip"`
	PublicIP            string          `json:"public_ip"`
	IsSymmetricNAT      bool            `json:"is_symmetic_nat"`
	NATType             string          `json:"nat_type,omitempty"`
	Hypervisors         []cipher.PubKey `json:"hypervisors"`
	ConnectedHypervisor []cipher.PubKey `json:"connected_hypervisor"`
	Hostname            string          `json:"hostname,omitempty"`

	// transports holds THIS (self) visor's live transports so MarshalJSON can
	// emit them in the overview's "transports" array (the node-table count). It
	// is unexported on purpose: gob skips it, so gob-mirrored REMOTE overviews
	// leave it nil (their transports arrive over the per-visor /transports RPC
	// instead) and only the self overview populates it via SetSelfTransports.
	transports []*TransportSummary
}

// SetSelfTransports attaches the self visor's live transports for MarshalJSON.
// Call on the OWN overview only; remote (gob-mirrored) overviews leave it nil.
func (o *Overview) SetSelfTransports(ts []*TransportSummary) { o.transports = ts }

// MarshalJSON emits the Overview fields plus apps + transports arrays. Apps stay
// empty (not surfaced in the wasm core yet); transports carry the self visor's
// live list when set (see SetSelfTransports), else an empty array so UI
// templates that index it don't choke.
func (o Overview) MarshalJSON() ([]byte, error) {
	type alias Overview
	tps := o.transports
	if tps == nil {
		tps = []*TransportSummary{}
	}
	return jsonMarshal(struct {
		alias
		Apps       []struct{}          `json:"apps"`
		Transports []*TransportSummary `json:"transports"`
	}{alias(o), []struct{}{}, tps})
}

// DmsgClientSummary mirrors visor/dmsgtracker.DmsgClientSummary. The CLI's
// `visor info` dereferences summary.DmsgStats.RoundTrip unconditionally, so this
// must decode to a non-nil pointer (gob matches by field name).
type DmsgClientSummary struct {
	PK        cipher.PubKey `json:"public_key"`
	ServerPK  cipher.PubKey `json:"server_public_key"`
	RoundTrip time.Duration `json:"round_trip"`
}

// DMSGServerInfo mirrors visor.DMSGServerInfo: a connected dmsg server with its
// round-trip latency (0 = not measured). The node-list UI reads the `dmsg_servers`
// array (pk + latency) to show which dmsg servers a visor is connected to.
type DMSGServerInfo struct {
	PK      cipher.PubKey `json:"pk"`
	Latency time.Duration `json:"latency"`
}

// Summary mirrors the scalar fields of visor.Summary the node table reads.
type Summary struct {
	Overview          *Overview          `json:"overview"`
	Health            *HealthInfo        `json:"health"`
	DmsgStats         *DmsgClientSummary `json:"dmsg_stats"`
	DMSGServers       []DMSGServerInfo   `json:"dmsg_servers"`
	Uptime            float64            `json:"uptime"`
	Online            bool               `json:"online"`
	MinHops           uint16             `json:"min_hops"`
	RewardAddress     string             `json:"reward_address"`
	BuildTag          string             `json:"build_tag"`
	ConfigVersion     string             `json:"config_version"`
	PublicAutoconnect bool               `json:"public_autoconnect"`
	IsPublic          bool               `json:"is_public"`
	IsHypervisor      bool               `json:"is_hypervisor,omitempty"`
}

// --- the core ---

// Core is a standalone wasm hypervisor: it accepts visor dials and tracks an
// gob RPC client per connected visor (net/rpc wire protocol, no net/rpc import).
type Core struct {
	pk    cipher.PubKey
	dmsgC *dmsg.Client

	mu     sync.RWMutex
	visors map[cipher.PubKey]*gobRPCClient
	self   SelfProvider // the tab's OWN visor (nil for a plain standalone HV)
}

// NewCore returns a Core for the given dmsg client + this hypervisor's PK.
func NewCore(pk cipher.PubKey, dmsgC *dmsg.Client) *Core {
	return &Core{pk: pk, dmsgC: dmsgC, visors: make(map[cipher.PubKey]*gobRPCClient)}
}

// Serve listens on the hypervisor dmsg port and accepts visor dials. Each
// accepted stream is wrapped in a gob RPC client (the dialing visor serves its RPC
// over it). Blocks until the listener errors or ctx is done.
func (c *Core) Serve(ctx context.Context) error {
	lis, err := c.dmsgC.Listen(DmsgHypervisorPort)
	if err != nil {
		return err
	}
	go func() { <-ctx.Done(); lis.Close() }() //nolint:errcheck,gosec
	for {
		stream, err := lis.AcceptStream()
		if err != nil {
			return err
		}
		remotePK := stream.RawRemoteAddr().PK
		client := newGobRPCClient(stream)
		c.mu.Lock()
		if old := c.visors[remotePK]; old != nil {
			old.Close() //nolint:errcheck,gosec
		}
		c.visors[remotePK] = client
		c.mu.Unlock()
	}
}

// connectedPKs returns the PKs of currently-connected visors.
func (c *Core) connectedPKs() []cipher.PubKey {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]cipher.PubKey, 0, len(c.visors))
	for pk := range c.visors {
		out = append(out, pk)
	}
	return out
}

// call invokes a visor RPC method on the connected visor, decoding into reply.
func (c *Core) call(pk cipher.PubKey, method string, args, reply interface{}) error {
	c.mu.RLock()
	client := c.visors[pk]
	c.mu.RUnlock()
	if client == nil {
		return errNotConnected
	}
	done := make(chan error, 1)
	go func() { done <- client.Call(rpcService+"."+method, args, reply) }()
	select {
	case err := <-done:
		return err
	case <-time.After(8 * time.Second):
		return errRPCTimeout
	}
}

// overviewOf fetches a visor's Overview, or a PK-only stub on error.
func (c *Core) overviewOf(pk cipher.PubKey) Overview {
	var ov Overview
	if err := c.call(pk, "Overview", &struct{}{}, &ov); err != nil {
		return Overview{PubKey: pk}
	}
	return ov
}

// summaryOf fetches a visor's Summary, or an offline stub on error.
func (c *Core) summaryOf(pk cipher.PubKey) Summary {
	var s Summary
	if err := c.call(pk, "Summary", &struct{}{}, &s); err != nil {
		return Summary{Overview: &Overview{PubKey: pk}, Online: false}
	}
	s.Online = true
	return s
}
