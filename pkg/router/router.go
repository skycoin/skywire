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
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

//go:generate mockery --name Router --case underscore --inpackage

const (
	// DefaultRouteKeepAlive is the default expiration interval for routes
	DefaultRouteKeepAlive = 2 * time.Minute
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
	ExcludeDMSG         bool          // Exclude DMSG transports (for mux — DMSG is a relay, not suitable for multiplexing)
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
	GetLastRouteCalcTime() time.Duration
	ActiveRouteStatuses() []RouteStatus
	AddMuxRouteByTransport(desc routing.RouteDescriptor, tpID uuid.UUID) error
	RemoveMuxRouteByTransport(desc routing.RouteDescriptor, tpID uuid.UUID) error

	// Routing table related methods
	RoutesCount() int
	Rules() []routing.Rule
	Rule(routing.RouteID) (routing.Rule, error)
	SaveRule(routing.Rule) error
	DelRules([]routing.RouteID)

	// MeasureTransportLatency measures the latency of a specific transport by
	// creating a temporary direct route over that transport, sending a ping,
	// and measuring the round-trip time. Returns latency in milliseconds.
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

// New constructs a new Router.
func New(dmsgC *dmsg.Client, config *Config, routeSetupHooks []RouteSetupHook) (Router, error) {
	config.SetDefaults()

	sl, err := dmsgC.Listen(skyenv.DmsgAwaitSetupPort)
	if err != nil {
		return nil, err
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
