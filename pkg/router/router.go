// Package router implements package router for skywire visor.
package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
)

//go:generate mockery --name Router --case underscore --inpackage

const (
	// DefaultRouteKeepAlive is the default expiration interval for routes.
	// Routes are refreshed on every data transfer. This timeout only
	// fires when no data has been sent for this long. Previously 24h,
	// reduced to 10 minutes so idle routes (e.g. from the resolving
	// proxy's one-shot HTTP requests) are cleaned up promptly instead
	// of consuming routing table entries for a full day.
	DefaultRouteKeepAlive = 10 * time.Minute
	// DefaultRulesGCInterval is the default duration for garbage collection of routing rules.
	DefaultRulesGCInterval = 10 * time.Second
	acceptSize             = 1024

	handshakeAwaitTimeout = 30 * time.Second

	maxHops       = 1000
	retryDuration = 2 * time.Second
	retryInterval = 500 * time.Millisecond
)

var (
	// ErrUnknownPacketType is returned when packet type is unknown.
	ErrUnknownPacketType = errors.New("unknown packet type")

	// ErrRemoteEmptyPK occurs when the specified remote public key is empty.
	ErrRemoteEmptyPK = errors.New("empty remote public key")

	// ErrNoTransportFound is returned when not even one transport is found.
	ErrNoTransportFound = errors.New("no transport found")
)

// RouteSetupHook is an alias for a function that takes remote public key
// and a reference to transport manager in order to setup i.e:
// 1. If the remote is either available stcpr or sudph, establish the transport to the remote and then continue with the route creation process.
// 2. If neither of these direct transports is available, check if automatic transports are currently active. If they are continue with route creation.
// 3. If none of the first two checks was successful, establish a dmsg transport and then continue with route creation.
type RouteSetupHook func(cipher.PubKey, *transport.Manager) error

// Config configures Router.
type Config struct {
	Logger           *logging.Logger
	MasterLogger     *logging.MasterLogger
	PubKey           cipher.PubKey
	SecKey           cipher.SecKey
	TransportManager *transport.Manager
	RouteFinder      rfclient.Client
	RouteGroupDialer RouteGroupDialer
	SetupNodes       []cipher.PubKey
	RulesGCInterval  time.Duration
	MinHops          uint16
	MaxHops          uint16
	// AwaitSetupListener, if non-nil, is used instead of opening a fresh
	// listener on DmsgAwaitSetupPort. This lets callers reserve the port
	// earlier in init (before the transport manager is ready) so peers
	// dialing it during the router-init window don't hit "request has no
	// associated listener" warnings.
	AwaitSetupListener *dmsg.Listener
	// AppLookup, if set, resolves a routing.Port to the originating
	// app name. Used to tag rg-scoped log entries with app_name=<n>
	// so 'cli proxy start --verbose' can scope to a session. nil
	// means no lookup is performed and entries are emitted untagged
	// (the existing behavior).
	AppLookup func(routing.Port) (string, bool)
}

// SetDefaults sets default values for certain empty values.
func (c *Config) SetDefaults() {
	if c.Logger == nil {
		c.Logger = logging.MustGetLogger("router")
	}

	if c.RouteGroupDialer == nil {
		c.RouteGroupDialer = NewSetupNodeDialer()
	}

	if c.RulesGCInterval <= 0 {
		c.RulesGCInterval = DefaultRulesGCInterval
	}

	if c.MaxHops == 0 {
		c.MaxHops = maxHops
	}
}

// DialOptions describes dial options.
type DialOptions struct {
	MinForwardRts       int
	MaxForwardRts       int
	MinConsumeRts       int
	MaxConsumeRts       int
	Retries             int
	UseExistingTpOnly   bool          // If true, only use routes through existing transports, don't create new ones
	TransportID         uuid.UUID     // If set, use this specific transport (skips route calculation for direct transports)
	ForwardHops         []routing.Hop // If set, use these hops for forward path (skips route calculation)
	ReverseHops         []routing.Hop // If set, use these hops for reverse path (skips route calculation)
	MuxRoutes           int           // Number of parallel routes to establish (0 or 1 = single route, >1 = mux)
	ExcludeTransportIDs []uuid.UUID   // Transport IDs to exclude from route calculation (for mux)
	// MinHops, when > 0, is the per-call minimum hop count constraint.
	// Overrides Config.MinHops for this dial only — needed by callers
	// that want a non-direct path even when the visor's global
	// min_hops is 1 (e.g. `cli visor ping mux-bw --min-hops 2`, which
	// must force every dial through an intermediate to test the
	// "mux via intermediates > direct" bandwidth hypothesis without
	// requiring the operator to bump visor-global config).
	//
	// Setting MinHops >= 2 also suppresses the direct-transport
	// fast path inside DialRoutes (the existing
	// "if r.isTpdExist(rPK) { MinHops = 1 }" downgrade is skipped),
	// since the caller has explicitly asked NOT to use direct.
	//
	// 0 = inherit Config.MinHops (legacy / single-route callers).
	MinHops int
	// ExcludeIntermediatePKs lists intermediate-visor PKs that route-
	// finder paths must NOT contain. Source + destination are never
	// excluded (they're endpoints, not intermediates). The mux loop
	// populates this with the intermediates of routes already
	// established so that subsequent routes are constructed through
	// different intermediate paths — disjoint in the intermediate-PK
	// set sense. Applied in both the HTTP route-finder path (post-
	// filter response) and the local route-calc fallback (filter
	// candidates).
	//
	// Disjoint-intermediate routing is AUTOMATIC for the mux loop
	// (MuxRoutes > 1). Callers that want overlapping intermediates
	// must dial with explicit ForwardHops/ReverseHops (which bypass
	// the route-finder entirely). This field is also exposed so
	// callers outside the mux loop can pre-populate exclusions if
	// they have constraints to express.
	ExcludeIntermediatePKs []cipher.PubKey
	ExcludeDMSG            bool          // Exclude DMSG transports (for mux — DMSG is a relay, not suitable for multiplexing)
	KeepAlive              time.Duration // Route keepalive (0 = DefaultRouteKeepAlive). Routes idle longer expire.
	// AppName is the originating app's name. Set by appnet's
	// DialContextWithOptions when the caller threaded a context
	// carrying it (RPCIngressGateway.Dial uses appnet.WithAppName).
	// router-side: scopedLog prefers this over AppLookup-by-port,
	// since DialRoutes is invoked with an ephemeral source port that
	// isn't the app's registered SetAppPort value.
	AppName string

	// ForwardMinHops / ReverseMinHops are per-direction overrides for
	// MinHops. When > 0 they take precedence over the symmetric
	// MinHops field for THAT direction only. Use case: bandwidth-
	// asymmetric workloads (HTTP GET = tiny upstream, bulk downstream)
	// where the user wants e.g. direct stcpr for the forward leg but
	// multi-hop for the reverse-direction bulk payload.
	//
	// 0 (the default) means "inherit MinHops" — back-compat for
	// callers that only care about symmetric routes.
	//
	// Resolution at use site (see resolveDirMinHops): if the per-
	// direction override is > 0 it wins; else MinHops; else
	// Config.MinHops; else the global default.
	ForwardMinHops int
	ReverseMinHops int

}

// DefaultDialOptions returns default dial options.
// Used by default if nil is passed as options.
func DefaultDialOptions() *DialOptions {
	return &DialOptions{
		MinForwardRts: 1,
		MaxForwardRts: 1,
		MinConsumeRts: 1,
		MaxConsumeRts: 1,
		Retries:       3,
	}
}

// EffectiveMinHops returns the per-direction min-hops constraint:
// forward=true → ForwardMinHops if > 0 else MinHops; forward=false →
// ReverseMinHops if > 0 else MinHops. Callers (the route-finder query
// site + the local-BFS fallback) read this once per direction to
// support bandwidth-asymmetric workloads where forward (small uplink)
// can use direct/short paths while reverse (bulk downlink) is forced
// onto multi-hop.
//
// Returns 0 when opts is nil or both per-direction + symmetric fields
// are 0 — preserves the caller's "inherit Config.MinHops" semantic.
func (o *DialOptions) EffectiveMinHops(forward bool) int {
	if o == nil {
		return 0
	}
	if forward {
		if o.ForwardMinHops > 0 {
			return o.ForwardMinHops
		}
	} else {
		if o.ReverseMinHops > 0 {
			return o.ReverseMinHops
		}
	}
	return o.MinHops
}

// AnyMinHopsConstraint reports whether the caller has set any min-hops
// constraint (symmetric or per-direction). Used at the direct-tp
// downgrade site in DialRoutes: if either direction wants multi-hop,
// the "transport-exists → drop MinHops to 1" optimization should be
// suppressed (mirroring the existing opts.MinHops > 1 check).
func (o *DialOptions) AnyMinHopsConstraint() bool {
	if o == nil {
		return false
	}
	return o.MinHops > 1 || o.ForwardMinHops > 1 || o.ReverseMinHops > 1
}

// Router is responsible for creating and keeping track of routes.
// Internally, it uses the routing table, route finder client and setup client.
type Router interface {
	io.Closer

	// DialRoutes dials to a given visor of 'rPK'.
	// 'lPort'/'rPort' specifies the local/remote ports respectively.
	// A nil 'opts' input results in a value of '1' for all DialOptions fields.
	// A single call to DialRoutes should perform the following:
	// - Find routes via RouteFinder (in one call).
	// - Setup routes via SetupNode (in one call).
	// - Save to routing.Table and internal RouteGroup map.
	// - Return RouteGroup if successful.
	DialRoutes(ctx context.Context, rPK cipher.PubKey, lPort, rPort routing.Port, opts *DialOptions) (net.Conn, error)
	PingRoute(ctx context.Context, rPK cipher.PubKey, lPort, rPort routing.Port, opts *DialOptions) (net.Conn, error)

	// AcceptRoutes should block until we receive an AddRules packet from SetupNode
	// that contains ConsumeRule(s) or ForwardRule(s).
	// Then the following should happen:
	// - Save to routing.Table and internal RouteGroup map.
	// - Return the RoutingGroup.
	AcceptRoutes(context.Context) (net.Conn, error)
	SaveRoutingRules(rules ...routing.Rule) error
	ReserveKeys(n int) ([]routing.RouteID, error)
	IntroduceRules(rules routing.EdgeRules) error
	Serve(context.Context) error
	SetupIsTrusted(cipher.PubKey) bool
	SetMinHop(uint16)
	SetExistingTPOnly(bool)
	SetForceLocalRoutes(bool)
	SetMuxRoutes(int)
	SetMuxMode(WeightMode)
	GetExistingTPOnly() bool
	GetForceLocalRoutes() bool
	GetMuxRoutes() int
	GetLastRouteCalcTime() time.Duration
	ActiveRouteStatuses() []RouteStatus
	AddMuxRouteByHops(desc routing.RouteDescriptor, fwd, rev []routing.Hop) error
	RemoveMuxRouteByTransport(desc routing.RouteDescriptor, tpID uuid.UUID) error

	// RouteGroupHops returns the stored forward route hops for the route group
	// matching the given descriptor (or its inversion). Returns nil if no
	// matching route group is found.
	RouteGroupHops(desc routing.RouteDescriptor) []RouteHopInfo

	// RouteGroupMuxInfo returns a snapshot of per-leg mux state for
	// the rg matching desc (or its inversion). Returns false when no
	// matching active rg is found. Used by 'cli proxy mux-info' to
	// surface per-leg bandwidth + latency for diagnostic purposes.
	RouteGroupMuxInfo(desc routing.RouteDescriptor) (MuxInfo, bool)

	// RouteGroupMuxInfoForApp returns mux snapshots for every active
	// rg whose source port maps (via AppLookup) to the named app.
	// One app may have multiple rg's at once (e.g. each SOCKS5
	// client connection to skysocks-client gets its own rg). Returns
	// an empty slice if no rg's are active for the app.
	RouteGroupMuxInfoForApp(appName string) []MuxInfo

	// Routing table related methods
	RoutesCount() int
	Rules() []routing.Rule
	Rule(routing.RouteID) (routing.Rule, error)
	SaveRule(routing.Rule) error
	DelRules([]routing.RouteID)

	// MeasureTransportLatency measures latency via RSN route setup.
	// Used as fallback when the remote visor doesn't support transport-level ping.
	MeasureTransportLatency(ctx context.Context, remote cipher.PubKey, tpID uuid.UUID) (float64, error)
}

// Router implements visor.PacketRouter. It manages routing table by
// communicating with setup nodes, forward packets according to local
// rules and manages route groups for apps.
type router struct {
	mx                 sync.Mutex
	conf               *Config
	logger             *logging.Logger
	mLogger            *logging.MasterLogger
	sl                 *dmsg.Listener
	dmsgC              *dmsg.Client
	trustedVisors      map[cipher.PubKey]struct{}
	tm                 *transport.Manager
	rt                 routing.Table
	rgsNs              map[routing.RouteDescriptor]*NoiseRouteGroup // Noise-wrapped route groups to push incoming reads from transports.
	rgsRaw             map[routing.RouteDescriptor]*RouteGroup      // Not-yet-noise-wrapped route groups. when one of these gets wrapped, it gets removed from here
	rpcSrv             *rpc.Server
	accept             chan routing.EdgeRules
	done               chan struct{}
	once               sync.Once
	routeSetupHookMu   sync.Mutex
	routeSetupHooks    []RouteSetupHook // see RouteSetupHook description
	existingTpOnly     bool             // when true, don't create new transports for routing
	existingTpOnlyMu   sync.Mutex       // protects existingTpOnly
	forceLocalRoutes   bool             // when true, skip route finder and use local route calculation
	forceLocalRoutesMu sync.Mutex       // protects forceLocalRoutes
	muxRoutes          int              // number of parallel mux routes (0 or 1 = disabled)
	muxMode            WeightMode       // default weight mode for new mux connections
	lastRouteCalcTime  time.Duration    // last route calculation time (for local routes)
	lastRouteCalcMu    sync.Mutex       // protects lastRouteCalcTime
}

// scopedLog returns a logger augmented with app_name=<n> when the
// configured AppLookup resolves lPort to an app, otherwise the
// router's bare logger. Callers use the returned logger for
// rg-scoped entries so 'cli proxy start --verbose' can filter on
// the field. Cheap on the no-lookup path (returns the bare logger
// directly).
func (r *router) scopedLog(lPort routing.Port) *logging.Logger {
	if r.conf == nil || r.conf.AppLookup == nil {
		return r.logger
	}
	name, ok := r.conf.AppLookup(lPort)
	if !ok || name == "" {
		return r.logger
	}
	return r.logger.WithAppName(name)
}

// scopedLogForOpts returns a logger augmented with app_name=<n> when
// opts carries an AppName (set via the appnet.WithAppName context
// path), falling back to the port-based scopedLog. Used in DialRoutes
// where the lPort is an ephemeral port that doesn't resolve via
// AppLookup, but the caller has already threaded the canonical name
// through DialOptions.
func (r *router) scopedLogForOpts(opts *DialOptions, lPort routing.Port) *logging.Logger {
	if opts != nil && opts.AppName != "" {
		return r.logger.WithAppName(opts.AppName)
	}
	return r.scopedLog(lPort)
}

// New constructs a new Router.
func New(dmsgC *dmsg.Client, config *Config, routeSetupHooks []RouteSetupHook) (Router, error) {
	config.SetDefaults()

	sl := config.AwaitSetupListener
	if sl == nil {
		var err error
		sl, err = dmsgC.Listen(skyenv.DmsgAwaitSetupPort)
		if err != nil {
			return nil, err
		}
	}

	trustedVisors := make(map[cipher.PubKey]struct{})
	for _, node := range config.SetupNodes {
		trustedVisors[node] = struct{}{}
	}

	if routeSetupHooks == nil {
		routeSetupHooks = []RouteSetupHook{}
	}

	r := &router{
		conf:            config,
		logger:          config.Logger,
		mLogger:         config.MasterLogger,
		tm:              config.TransportManager,
		rt:              routing.NewTable(config.Logger),
		sl:              sl,
		dmsgC:           dmsgC,
		rgsNs:           make(map[routing.RouteDescriptor]*NoiseRouteGroup),
		rgsRaw:          make(map[routing.RouteDescriptor]*RouteGroup),
		rpcSrv:          rpc.NewServer(),
		accept:          make(chan routing.EdgeRules, acceptSize),
		done:            make(chan struct{}),
		trustedVisors:   trustedVisors,
		routeSetupHooks: routeSetupHooks,
	}

	go r.rulesGCLoop()

	if err := r.rpcSrv.Register(NewRPCGateway(r, config.MasterLogger)); err != nil {
		return nil, fmt.Errorf("failed to register RPC server")
	}

	return r, nil
}

// RegisterSetupHooks takes variadic RouteSetupHook to add to router's setup functions
// currently not in use
func (r *router) RegisterSetupHooks(rshooks ...RouteSetupHook) {
	r.routeSetupHookMu.Lock()
	r.routeSetupHooks = append(r.routeSetupHooks, rshooks...)
	r.routeSetupHookMu.Unlock()
}
