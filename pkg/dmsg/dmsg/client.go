// Package dmsg pkg/dmsg/dmsg/client.go c1-net-dmsg
package dmsg

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
)

// entryCacheEntry holds a cached discovery entry with a timestamp.
type entryCacheEntry struct {
	entry     *disc.Entry
	fetchedAt time.Time
}

// entryCacheTTL bounds how long a resolved discovery entry is reused
// before a fresh lookup. It MUST stay comfortably larger than the
// callers that re-resolve the same peers on a fixed cadence — most
// notably the dmsg-tracker (skyenv.DmsgTrackerUpdateInterval, 30s),
// which re-establishes trackers for the same handful of peers every
// cycle. When this TTL equalled that interval, every tracker cycle
// landed just after expiry → cache miss → a burst of concurrent
// dmsg-discovery dials for the same ~10 peers → contention →
// canceled/timed-out dials → "cannot connect to delegated server"
// (202) → HTTP fallback. Sizing the TTL to span many tracker cycles
// collapses those repeat lookups into one disc hit per peer per TTL,
// which is what lets the DMSG-primary path stay on dmsg instead of
// falling back to HTTP. Peer entries are stable (they change only on
// a dmsg-server reconnect), and a stale entry self-heals: DialStream's
// mesh phase reaches the peer via any shared server regardless of the
// cached delegated-server list.
const entryCacheTTL = 5 * time.Minute

// SessionDialCallback is triggered BEFORE a session is dialed to.
// If a non-nil error is returned, the session dial is instantly terminated.
type SessionDialCallback func(network, addr string) (err error)

// SessionDisconnectCallback triggers after a session is closed.
type SessionDisconnectCallback func(network, addr string, err error)

// ClientCallbacks contains callbacks which a Client uses.
type ClientCallbacks struct {
	OnSessionDial       SessionDialCallback
	OnSessionDisconnect SessionDisconnectCallback
}

func (sc *ClientCallbacks) ensure() {
	if sc.OnSessionDial == nil {
		sc.OnSessionDial = func(network, addr string) (err error) { return nil } //nolint
	}
	if sc.OnSessionDisconnect == nil {
		sc.OnSessionDisconnect = func(network, addr string, err error) {} //nolint
	}
}

// Config configures a dmsg client entity.
type Config struct {
	MinSessions          int
	UpdateInterval       time.Duration // Duration between discovery entry updates.
	Callbacks            *ClientCallbacks
	ClientType           string
	ConnectedServersType string
	Protocol             string

	// Carriers is the ordered preference of dmsg CARRIERS — how this client
	// reaches a dmsg server — by name: "tcp", "ws", "wt", "quic". The first
	// listed carrier the server advertises is dialed (falling back to TCP on dial
	// failure). The carrier is transparent above the session: every dmsg stream is
	// the same Noise+yamux regardless, so this only affects how bytes reach the
	// rendezvous server. Empty (the native default) = QUIC when the server
	// advertises it, else TCP — raw sockets are strictly better for native
	// clients. It exists for restrictive networks (e.g. only 443/HTTPS egress →
	// ["wt"] or ["ws"]) and is set to ["ws"] in the js/wasm build, which has no
	// raw socket. Unknown names are ignored.
	Carriers []string
}

// Carrier names for Config.Carriers.
const (
	CarrierTCP  = "tcp"
	CarrierWS   = "ws"
	CarrierWT   = "wt"
	CarrierQUIC = "quic"
)

// Ensure ensures all config values are set.
func (c *Config) Ensure() {
	if c.Callbacks == nil {
		c.Callbacks = new(ClientCallbacks)
	}
	c.Callbacks.ensure()
	// A client that leaves UpdateInterval unset gets the CLIENT default, not
	// EntityCommon.init's zero-value fallback. That fallback is
	// DefaultUpdateInterval (1 min), which is the SERVER's cadence — a server
	// republishes often because its AvailableSessions changes with every
	// client connect/disconnect. A client's entry only carries its delegated
	// servers, which on a settled client never change, so it is meant to
	// refresh five times more slowly (DefaultConfig, below).
	//
	// Almost nobody constructs a client through DefaultConfig: eleven of the
	// fifteen call sites in this repo build a Config literal with just
	// MinSessions and were therefore republishing every 60s instead of every
	// 5m. That is a signed GET+PUT to dmsg-discovery over a fresh dmsg stream
	// per tick — the periodic update is never short-circuited, since the
	// SamePubKeys guard in updateClientEntry only suppresses nudge-driven
	// updates and the due timer always makes `due` true. Defaulting here
	// rather than in each caller is what keeps the next literal from
	// reintroducing it.
	if c.UpdateInterval == 0 {
		c.UpdateInterval = DefaultUpdateInterval * 5
	}
}

// DefaultConfig returns the default configuration for a dmsg client entity.
func DefaultConfig() *Config {
	conf := &Config{
		MinSessions:    DefaultMinSessions,
		UpdateInterval: DefaultUpdateInterval * 5,
	}
	return conf
}

// Client represents a dmsg client entity.
type Client struct {
	ready     chan struct{}
	readyOnce sync.Once

	EntityCommon
	conf   *Config
	porter *netutil.Porter

	initBO time.Duration // initial backoff duration (constant)
	bo     time.Duration // current backoff duration
	maxBO  time.Duration // maximum backoff duration
	factor float64       // multiplier for the backoff duration that is applied on every retry

	// routeCache maps destination client PK → server PK that last successfully
	// relayed to that destination. Evicted on failure.
	routeCache   map[cipher.PubKey]cipher.PubKey
	routeCacheMx sync.RWMutex

	// entryCache caches discovery entry lookups with TTL to avoid
	// re-querying HTTP discovery on every request.
	entryCache   map[cipher.PubKey]entryCacheEntry
	entryCacheMx sync.RWMutex

	// carrierFailAt records, per dmsg-server PK, the time of the last carrier
	// convergence re-dial that landed on a less-preferred carrier or failed
	// (see ConvergeCarriers). A server whose preferred endpoint is unreachable
	// (e.g. a UDP-blocked WT/QUIC H3 listener) would otherwise be re-dialed
	// every converge tick; backing off keeps the session on its working carrier
	// instead of thrashing. Lazily initialized.
	carrierFailAt map[cipher.PubKey]time.Time
	// carrierLastErr records, per server PK, the reason the last convergence
	// attempt didn't reach the preferred carrier (surfaced by SessionCarriers /
	// the `dmsg converge` CLI for diagnostics). Guarded by carrierFailMx.
	carrierLastErr map[cipher.PubKey]string
	carrierFailMx  sync.Mutex

	// carrier convergence controls: convMx guards a runtime override of the
	// ordered carrier preference (nil = Config.Carriers) and a live-only
	// discovery client used to read a server's CURRENT WT/QUIC endpoints
	// (AddressWT/CertHashWT/AddressUDP). The default disc client (ce.dc) is a
	// static-seed-first fallback that shadows those rotating fields, so
	// convergence must resolve through a live lookup — set via SetLiveDiscovery.
	convMx           sync.RWMutex
	carriersOverride []string
	liveDisc         disc.APIClient

	// DHTLookup, when set, is called by DialStream before the HTTP
	// discovery lookup. If it returns a valid entry, the HTTP discovery
	// is skipped entirely. This lets the visor resolve DMSG client
	// entries from the local DHT store (instant, no network) for PKs
	// that have published their entries to the DHT.
	DHTLookup func(pk cipher.PubKey) (*disc.Entry, error)

	// Lookup counters for diagnostics.
	LookupCacheHits  atomic.Int64 // resolved from entry cache
	LookupDHTHits    atomic.Int64 // resolved from DHT (DHTLookup callback)
	LookupHTTPHits   atomic.Int64 // resolved from HTTP discovery
	LookupHTTPMisses atomic.Int64 // HTTP discovery returned not found

	// pinnedFailures counts consecutive failed Serve-loop passes
	// while in pinned mode (--dmsg-server / ctx.Value("dmsgServer")
	// set). The visor polls PinnedFailureCount() during startup to
	// decide when to abort. Resets to zero on successful session.
	pinnedFailures atomic.Int64

	errCh chan error
	done  chan struct{}
	once  sync.Once
	wg    sync.WaitGroup // tracks background goroutines for clean shutdown
	sesMx sync.Mutex

	// closed gates new session-serving goroutines once Close has begun.
	// Protected by EntityCommon.sessionsMx — set true at the start of
	// Close's critical section and checked in dialSession before
	// wg.Add(1). Without this gate, a dialSession running concurrently
	// with Close can race wg.Add against wg.Wait (the race detector
	// flags this in TestEnv): Close.wg.Wait returns with the counter
	// at zero just as dialSession is about to wg.Add(1), leaving a
	// serve-goroutine un-waited-on. Holding sessionsMx around both
	// the closed-set in Close and the closed-check + Add in
	// dialSession provides the happens-before edge.
	closed bool
}

// AddDiscovery registers an additional dmsg-discovery this client
// should publish its entry to. discPK, when set, identifies the
// discovery's own dmsg-server PK so the client can detect inbound
// sessions originated by this discovery (currently unused on the
// client side; reserved for symmetry with Server).
//
// Safe to call before or after Serve. Subsequent updateClientEntry
// iterations pick up the new endpoint automatically.
func (c *Client) AddDiscovery(client disc.APIClient, discPK cipher.PubKey) {
	c.addDiscovery(client, "", "", discPK)
}

// SetDiscoveryClients replaces the underlying disc.APIClient on each
// configured discovery endpoint, preserving PKs. The lengths must
// match the current endpoint count, otherwise the call is a no-op.
// Used to upgrade plain HTTP clients to dmsgfirst-wrapped clients
// once an outbound dmsg.Client is available.
func (c *Client) SetDiscoveryClients(clients []disc.APIClient) {
	c.setDiscoveryClients(clients)
}

// NewClient creates a dmsg client entity.
func NewClient(pk cipher.PubKey, sk cipher.SecKey, dc disc.APIClient, conf *Config) *Client {
	log := logging.MustGetLogger("dmsg_client")

	// Init config.
	if conf == nil {
		conf = DefaultConfig()
	}
	conf.Ensure()

	c := &Client{
		ready:      make(chan struct{}),
		porter:     netutil.NewPorter(netutil.PorterMinEphemeral),
		routeCache: make(map[cipher.PubKey]cipher.PubKey),
		entryCache: make(map[cipher.PubKey]entryCacheEntry),
		errCh:      make(chan error, 10),
		done:       make(chan struct{}),
		conf:       conf,
		initBO:     time.Second * 5,
		bo:         time.Second * 5,
		maxBO:      time.Minute,
		factor:     netutil.DefaultFactor,
	}

	// Init common fields.
	c.EntityCommon.init(pk, sk, dc, log, conf.UpdateInterval)

	// Init callback: on set session.
	c.EntityCommon.setSessionCallback = func(ctx context.Context) error {
		// The first session must update discovery before declaring
		// Ready so other modules can immediately dial through us; see
		// the wave of dmsghttp transport tests that fail with
		// "client entry in discovery has no delegated servers" if
		// Ready fires pre-publish. Subsequent sessions use the
		// debounced nudge path.
		select {
		case <-c.ready:
			// Already ready — nudge the update loop.
			c.nudgeEntryUpdate()
			return nil
		default:
		}
		// First-session path runs the publish on its OWN goroutine,
		// then waits up to entryUpdateAttemptTimeout for it to finish.
		// The publish bounds itself with the same per-attempt timeout
		// the entry loop uses (#3168). If it completes within the
		// window we close c.ready on its result; if the wait expires
		// we close c.ready anyway so the rest of the visor doesn't
		// deadlock on us, and the publish goroutine continues to
		// completion in the background (its own ctx will fire and
		// unblock the dmsg-HTTP stream via the transport's ctx
		// watcher). The updateClientEntryLoop handles further retries
		// with exponential backoff.
		//
		// Originally this called updateClientEntry SYNCHRONOUSLY here
		// with the unbounded session-dial ctx and closed c.ready only
		// on success. That blocked the session-add path forever if
		// the publish stalled — a dmsg-only deployment service
		// (#3157, #3168) whose only established session couldn't
		// reach the discovery PK saw the dmsg-HTTP transport get a
		// stream via DialStream phase 3, then req.Write/ReadResponse
		// hung on the stream's own deadlines (write: none, read:
		// StreamIdleTimeout = 2 min). The cascade:
		//   - first setSessionCallback blocks → dialSession never
		//     returns → Serve loop's EnsureSession chain freezes;
		//   - subsequent session callbacks (ready never closed) re-
		//     enter this default branch and block on
		//     httpClient.updateMux which the first call holds;
		//   - updateClientEntryLoop's runUpdate (with the #3168 30 s
		//     attempt timeout) ALSO blocks on updateMux because mutex
		//     acquisition does not honor context — so the loop's
		//     retry never fires either.
		// The result was a wedged service that logged "Updating
		// entry" once at startup and then went silent for hours.
		publishCtx, publishCancel := context.WithTimeout(ctx, entryUpdateAttemptTimeout)
		publishDone := make(chan error, 1)
		go func() {
			defer publishCancel()
			publishDone <- c.EntityCommon.updateClientEntry(publishCtx, c.done, c.conf.ClientType)
		}()
		select {
		case err := <-publishDone:
			if err != nil {
				c.log.WithError(err).Warn("Initial client entry update failed; loop will retry.")
			}
		case <-time.After(entryUpdateAttemptTimeout):
			c.log.Warn("Initial client entry update did not complete within bound; closing Ready and letting the loop retry.")
		case <-ctx.Done():
			return ctx.Err()
		}
		c.readyOnce.Do(func() { close(c.ready) })
		return nil
	}

	// Init callback: on delete session.
	c.EntityCommon.delSessionCallback = func(ctx context.Context) error {
		c.nudgeEntryUpdate()
		return nil
	}

	return c
}

// Type returns the client's type (should always be "dmsg").
func (*Client) Type() string {
	return Type
}

// Serve serves the client.
// It blocks until the client is closed.
func (ce *Client) Serve(ctx context.Context) {
	defer func() {
		ce.log.Debug("Stopped serving client!")
	}()

	cancellabelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	setupNodeTicker := time.NewTicker(1 * time.Minute)
	defer setupNodeTicker.Stop()

	go func(ctx context.Context) {
		select {
		case <-ctx.Done():
		case <-ce.done:
			cancel()
		}
	}(cancellabelCtx)

	updateEntryLoopOnce := new(sync.Once)
	pingLoopOnce := new(sync.Once)
	reconnectLoopOnce := new(sync.Once)
	porterReapLoopOnce := new(sync.Once)
	reapLoopOnce := new(sync.Once)

	needInitialPost := true

	for {
		if isClosed(ce.done) {
			return
		}
		var entries []*disc.Entry
		var err error
		ce.log.Debug("Discovering dmsg servers...")
		pinned := ctx.Value("dmsgServer") != nil
		pinnedAddr, _ := ctx.Value("dmsgServerAddr").(string)
		if pinned && pinnedAddr != "" {
			// pk@host:port form: skip discovery entirely. We have
			// everything dialSession needs (PK + TCP address) right
			// here, so don't burn an HTTP round-trip on dmsg-discovery
			// — that may be unreachable in the bootstrap scenarios
			// this mode exists for. A bad PK falls through to "No
			// entries found" and gets counted as a pinned failure.
			if dmsgServer, ok := ctx.Value("dmsgServer").(string); ok {
				var pk cipher.PubKey
				if err := pk.Set(dmsgServer); err != nil {
					ce.log.WithError(err).
						WithField("dmsg_server", dmsgServer).
						Warn("Pinned dmsg server PK invalid; will retry.")
					entries = nil
				} else {
					entries = []*disc.Entry{{
						Static: pk,
						Server: &disc.Server{Address: pinnedAddr},
					}}
				}
			}
		} else if pinned {
			entries, err = ce.discoverServers(cancellabelCtx, true)
			if err != nil {
				ce.log.WithError(err).Warn("Failed to discover dmsg servers.")
				if err == context.Canceled || err == context.DeadlineExceeded {
					return
				}
				ce.pinnedFailures.Add(1)
				ce.serveWait()
				continue
			}

			// Strict pinning: keep only the requested server. If it's
			// not in discovery (typo, not registered, etc.), drop to
			// empty entries so the outer loop hits the "No entries
			// found" retry path instead of silently connecting to a
			// different server.
			matched := false
			if dmsgServer, ok := ctx.Value("dmsgServer").(string); ok {
				for ind, entry := range entries {
					if entry.Static.Hex() == dmsgServer {
						entries = entries[ind : ind+1]
						matched = true
						break
					}
				}
			}
			if !matched {
				ce.log.WithField("dmsg_server", ctx.Value("dmsgServer")).
					Warn("Pinned dmsg server not in discovery; will retry.")
				entries = nil
			}
		} else if ctx.Value("setupNode") != nil {
			entries, err = ce.discoverServers(cancellabelCtx, true)
			if err != nil {
				ce.log.WithError(err).Warn("Failed to discover dmsg servers.")
				if err == context.Canceled || err == context.DeadlineExceeded {
					return
				}
				ce.serveWait()
				continue
			}
		} else {
			// MinSessions == 0 means "connect to all servers". Use AllServers,
			// not AvailableServers: a server at/near capacity stops advertising
			// availability (drops out of available_servers) but still accepts
			// sessions, and a connect-all client must keep a session to EVERY
			// server so it can always rendezvous with a peer delegated to one
			// that has stopped advertising. MinSessions > 0 keeps the
			// availability-filtered list (only connect where there's room).
			entries, err = ce.discoverServers(cancellabelCtx, ce.conf.MinSessions == 0)
			if err != nil {
				ce.log.WithError(err).Warn("Failed to discover dmsg servers.")
				if err == context.Canceled || err == context.DeadlineExceeded {
					return
				}
				ce.serveWait()
				continue
			}
		}
		if len(entries) == 0 && !pinned {
			// Fallback for dmsg-only deployments and fleet cold-starts:
			// when the disc.APIClient yields no entries (because the HTTP
			// discovery URL is empty -> no-op client, or because no
			// server has registered yet), use the server entries that
			// were pre-seeded into the entry cache from config. The
			// pinned paths above are deliberate fail-fast and must not
			// be silently routed to a different server, so the fallback
			// applies only to the unpinned default path.
			if seeded := ce.seededServerEntries(); len(seeded) > 0 {
				ce.log.WithField("count", len(seeded)).
					Debug("No entries from discovery; falling back to seeded servers from config.")
				entries = seeded
			}
		}
		if len(entries) == 0 {
			ce.log.Warnf("No entries found. Retrying after %s...", ce.bo.String())
			if pinned {
				ce.pinnedFailures.Add(1)
			}
			ce.serveWait()
			continue
		}
		// Order the servers by CAPACITY-WEIGHTED random selection. Each server
		// advertises AvailableSessions (its session capacity); we bias it earlier
		// in the list proportionally to that capacity, so clients spread across
		// servers in proportion to how much load each can take.
		//
		// A plain uniform shuffle (the previous behavior) sent every server an
		// equal share of clients regardless of capacity. With ~1100 clients and
		// server capacities varying from ~30 to ~2000, the low-capacity hosts
		// (small fd-limit VPSes) received far more clients than they could accept
		// and rejected the overflow with EOF, while high-capacity servers sat
		// underused — a chronic imbalance that made low-capacity servers (and any
		// client/service delegated only to them) intermittently unreachable.
		//
		// The per-item random key (Efraimidis-Spirakis A-Res: key = u^(1/w))
		// keeps the ordering randomized within the weighting, so simultaneously
		// starting clients still don't stampede the same server. A missing/zero
		// AvailableSessions falls back to weight 1 (uniform), matching the old
		// behavior for discoveries that don't populate the field.
		seedRng := rand.New(rand.NewSource(cryptoSeed())) //nolint:gosec // G404: seed is from crypto/rand; math/rand is fine for weighting
		type weightedEntry struct {
			entry *disc.Entry
			key   float64
		}
		weighted := make([]weightedEntry, len(entries))
		for i, entry := range entries {
			w := 1.0
			if entry.Server != nil && entry.Server.AvailableSessions > 0 {
				w = float64(entry.Server.AvailableSessions)
			}
			u := seedRng.Float64()
			if u <= 0 {
				u = math.SmallestNonzeroFloat64
			}
			weighted[i] = weightedEntry{entry: entry, key: math.Pow(u, 1.0/w)}
		}
		sort.SliceStable(weighted, func(i, j int) bool { return weighted[i].key > weighted[j].key })
		for i := range weighted {
			entries[i] = weighted[i].entry
		}

		if needInitialPost {
			// use this for put protocol type of client to disc, for dicision part of dmsg-server
			err = ce.initilizeClientEntry(cancellabelCtx, ce.conf.ClientType, ce.conf.Protocol)
			if err != nil {
				ce.log.WithError(err).Warn("Initial post entry failed")
			} else {
				ce.log.Info("Initial post entry succeeded")
				needInitialPost = false
			}
		}

		for n, entry := range entries {
			if isClosed(ce.done) {
				return
			}

			// Skip dmsg servers without user specific types: official, community, all
			if ce.conf.ConnectedServersType == "official" {
				if entry.Server.ServerType != "official" {
					continue
				}
			} else if ce.conf.ConnectedServersType == "community" {
				if entry.Server.ServerType != "community" {
					continue
				}
			}

			// If MinSessions is set to 0 then we connect to all available servers.
			// If MinSessions is not 0 AND we have enough sessions, we wait for error or done signal.
			if ce.conf.MinSessions != 0 && ce.SessionCount() >= ce.conf.MinSessions {
				select {
				case <-ce.done:
					return
				case err := <-ce.errCh:
					ce.log.WithError(err).Debug("Session stopped.")
					if isClosed(ce.done) {
						return
					}
				}
			}

			if err := ce.EnsureSession(cancellabelCtx, entry); err != nil {
				if err == context.Canceled || err == context.DeadlineExceeded {
					ce.log.WithField("remote_pk", entry.Static).WithError(err).Warn("Failed to establish session.")
					return
				}
				// we send an error if this is the last server
				if n == (len(entries) - 1) {
					if !isClosed(ce.done) {
						ce.sesMx.Lock()
						select {
						case ce.errCh <- err:
						default:
							ce.log.WithError(err).Warn("Error channel full, dropping error.")
						}
						ce.sesMx.Unlock()
					}
					// Only backoff after all servers have been tried
					ce.log.WithField("current_backoff", ce.bo.String()).
						Warn("All servers failed, backing off.")
					if pinned {
						ce.pinnedFailures.Add(1)
					}
					ce.serveWait()
				}
				ce.log.WithField("remote_pk", entry.Static).WithError(err).
					Warn("Failed to establish session.")
			} else {
				// Reset backoff on successful session establishment.
				ce.bo = ce.initBO
				if pinned {
					ce.pinnedFailures.Store(0)
				}
				// Start the background loops the moment a session
				// exists. Pre-fix they were started AFTER the inner
				// for-loop iterated every seed entry — fine when
				// EnsureSession returned quickly, but the dial path
				// runs setSessionCallback synchronously and that
				// callback's first-session publish blocks for up to
				// entryUpdateAttemptTimeout (#3172). During that 30 s
				// window Phase 3 of the publish's own DialStream
				// recursively dials extra sessions, pushing
				// SessionCount past MinSessions. When EnsureSession
				// finally returns, the next iteration of THIS for
				// loop trips the
				//   if MinSessions != 0 && SessionCount() >= MinSessions
				// guard and parks on <-errCh — for the rest of the
				// process's life if sessions are stable. The for loop
				// then never completes its iteration and the lines
				// below (updateClientEntryLoop start) never run, so
				// the entry-publish retry loop never starts and the
				// service stays unregistered until the very first
				// session dies. Idempotent via sync.Once so duplicate
				// triggers across entries are a no-op.
				updateEntryLoopOnce.Do(func() { go ce.updateClientEntryLoop(cancellabelCtx, ce.done, ce.conf.ClientType) })
				pingLoopOnce.Do(func() { go ce.pingSessionsLoop(cancellabelCtx) })
				porterReapLoopOnce.Do(func() { go ce.porterReapLoop(cancellabelCtx) })
				reapLoopOnce.Do(func() { go ce.reapIdleSessionsLoop(cancellabelCtx) })
				if ce.conf.MinSessions == 0 {
					reconnectLoopOnce.Do(func() { go ce.reconnectLoop(cancellabelCtx) })
				}
			}
		}

		// Defensive: also start loops post-iteration in case the
		// inner for-loop drained without hitting the success branch
		// (everything failed, then the loop fell through to the
		// outer-select retry path). sync.Once makes this safe.
		updateEntryLoopOnce.Do(func() { go ce.updateClientEntryLoop(cancellabelCtx, ce.done, ce.conf.ClientType) })
		pingLoopOnce.Do(func() { go ce.pingSessionsLoop(cancellabelCtx) })
		porterReapLoopOnce.Do(func() { go ce.porterReapLoop(cancellabelCtx) })
		reapLoopOnce.Do(func() { go ce.reapIdleSessionsLoop(cancellabelCtx) })
		// When MinSessions is 0 (connect to all), start a reconnect loop that
		// aggressively retries connecting to servers we failed to reach on the first pass.
		if ce.conf.MinSessions == 0 {
			reconnectLoopOnce.Do(func() { go ce.reconnectLoop(cancellabelCtx) })
		}

		// We dial all servers and wait for error or done signal.
		select {
		case <-ce.done:
			return
		case err := <-ce.errCh:
			ce.log.WithError(err).Debug("Session stopped.")
			if isClosed(ce.done) {
				return
			}
		case <-setupNodeTicker.C:
			continue
		}
	}
}

// Ready returns a chan which blocks until the client has at least one delegated server and has an entry in the
// dmsg discovery.
func (ce *Client) Ready() <-chan struct{} {
	return ce.ready
}

// PinnedFailureCount returns the number of consecutive failed
// Serve-loop passes while the client is pinned to a single dmsg
// server via the "dmsgServer" context value. Reset to 0 whenever a
// session is successfully established. Callers (the visor's startup
// path) use this to bound how long the visor waits for a pinned
// server before aborting startup.
func (ce *Client) PinnedFailureCount() int64 {
	return ce.pinnedFailures.Load()
}

func (ce *Client) discoverServers(ctx context.Context, all bool) (entries []*disc.Entry, err error) {
	err = netutil.NewDefaultRetrier(ce.log).Do(ctx, func() error {
		if all {
			entries, err = ce.dc.AllServers(ctx)
		} else {
			entries, err = ce.dc.AvailableServers(ctx)
		}
		return err
	})
	return entries, err
}

// cryptoSeed returns a math/rand seed sourced from crypto/rand, falling back to
// the wall clock if the CSPRNG read fails. Used to seed the per-client server
// ordering so simultaneously starting clients pick independent orders.
func cryptoSeed() int64 {
	var seed int64
	if err := binary.Read(crand.Reader, binary.BigEndian, &seed); err != nil {
		seed = time.Now().UnixNano()
	}
	return seed
}

// Close closes the dmsg client entity and waits for background goroutines to finish.
func (ce *Client) Close() error {
	if ce == nil {
		return nil
	}
	var err error
	ce.once.Do(func() {
		close(ce.done)

		ce.sesMx.Lock()
		close(ce.errCh)
		ce.sesMx.Unlock()

		ce.sessionsMx.Lock()
		// Mark closed before iterating so any concurrent dialSession
		// blocking on sessionsMx sees the closed flag and bails out
		// without doing wg.Add(1) (see Client.closed for the race).
		ce.closed = true
		for _, dSes := range ce.sessions {
			ce.log.
				WithError(dSes.Close()).
				Debug("Session closed.")
		}
		ce.sessions = make(map[cipher.PubKey]*SessionCommon)
		ce.log.Debug("All sessions closed.")
		ce.sessionsMx.Unlock()
		ce.porter.CloseAll(ce.log)
		ce.wg.Wait()
		// Use a short timeout for discovery cleanup — if the discovery
		// server is accessed over dmsg (which we just closed), this
		// request would hang forever with context.Background().
		delCtx, delCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = ce.EntityCommon.delEntry(delCtx)
		delCancel()
	})
	return err
}

// AllSessions obtains all established sessions.
func (ce *Client) AllSessions() []ClientSession {
	return ce.allClientSessions(ce.porter)
}

// SetMinSessions updates the minimum session count at runtime.
// The reconnect loop will use the new value on its next iteration.
func (ce *Client) SetMinSessions(n int) {
	ce.sesMx.Lock()
	defer ce.sesMx.Unlock()
	ce.conf.MinSessions = n
}

// MinSessions returns the current minimum session count.
func (ce *Client) MinSessions() int {
	ce.sesMx.Lock()
	defer ce.sesMx.Unlock()
	return ce.conf.MinSessions
}

// PorterCount returns the number of reserved ephemeral ports.
func (ce *Client) PorterCount() int {
	return ce.porter.Count()
}

// ResetPorter frees all ephemeral port reservations, recovering from
// port exhaustion. Well-known ports (listeners) are preserved.
// Returns the number of ports freed.
func (ce *Client) ResetPorter() int {
	return ce.porter.ResetEphemeral()
}

// PorterDiag returns detailed diagnostic information about ephemeral
// port reservations. This helps identify what is holding ports when
// the porter approaches exhaustion.
func (ce *Client) PorterDiag() netutil.EphemeralDiagResult {
	return ce.porter.EphemeralDiag()
}

// SweepStalePorterEntries closes and frees ephemeral port entries older
// than maxAge. Returns the number of entries swept. This is a safety net
// for streams that leak through the normal cleanup paths.
func (ce *Client) SweepStalePorterEntries(maxAge time.Duration) int {
	return ce.porter.SweepStaleEphemeral(maxAge)
}

// ForceReconnect closes every active server session. The reconnect loop
// (15 s cadence) and delSession → nudgeEntryUpdate path together will
// re-dial fresh sessions and refresh the discovery entry's
// delegated_servers list.
//
// This exists as a recovery hook for the visor's self-probe: when a
// visor's own listeners become unreachable through its existing
// sessions (typically a one-sided view — server has the session, we
// don't, or vice versa), nothing in steady-state detects it because
// our side thinks the session is fine. Closing and re-dialing clears
// the staleness.
//
// Returns the number of sessions that were torn down. Safe to call
// concurrently with normal operation; in-flight streams on the closed
// sessions will fail and the caller is expected to retry on a fresh
// session.
func (ce *Client) ForceReconnect() int {
	sessions := ce.allClientSessions(ce.porter)
	for _, ses := range sessions {
		_ = ses.Close() //nolint:errcheck,gosec
	}
	// Nudge an immediate discovery entry refresh so clients dialing
	// us stop seeing the now-closed sessions in our delegated list.
	ce.nudgeEntryUpdate()
	return len(sessions)
}

// ConnectToAllServersResult summarizes the result of a ConnectToAllServers call.
type ConnectToAllServersResult struct {
	Total            int                      // total servers found in discovery
	AlreadyConnected int                      // sessions already in place before the call
	NewlyConnected   int                      // sessions established by this call
	Failed           map[cipher.PubKey]string // server PK → error text for any that could not be connected
}

// ConnectToAllServers enumerates every server entry in dmsg discovery and
// ensures the client has an active session to each one. Useful as a
// one-shot "maximize connectivity now" action for visors that run
// route/transport setup nodes and need to reach arbitrary destinations
// without bouncing through phase-3 new-session dials.
//
// The client's configured MinSessions is NOT mutated — once a session
// dies, the normal reconnect behavior applies. Use SetSessionsCount
// (or restart the client with sessions_count: 0) if you want the
// "keep sessions to all servers" behavior to persist.
func (ce *Client) ConnectToAllServers(ctx context.Context) (ConnectToAllServersResult, error) {
	result := ConnectToAllServersResult{Failed: make(map[cipher.PubKey]string)}

	entries, err := ce.discoverServers(ctx, true)
	if err != nil {
		return result, fmt.Errorf("dmsg disc AllServers: %w", err)
	}
	result.Total = len(entries)

	for _, entry := range entries {
		if isClosed(ce.done) {
			return result, nil
		}
		// Respect server type filter if one is configured.
		if ce.conf.ConnectedServersType == "official" && entry.Server.ServerType != "official" {
			continue
		}
		if ce.conf.ConnectedServersType == "community" && entry.Server.ServerType != "community" {
			continue
		}
		// Skip servers we already have a session with.
		if _, ok := ce.session(entry.Static); ok {
			result.AlreadyConnected++
			continue
		}
		if err := ce.EnsureSession(ctx, entry); err != nil {
			result.Failed[entry.Static] = err.Error()
			ce.log.WithField("server_pk", entry.Static).WithError(err).
				Debug("ConnectToAllServers: failed to establish session.")
			continue
		}
		result.NewlyConnected++
		ce.log.WithField("server_pk", entry.Static).Info("ConnectToAllServers: session established.")
	}
	return result, nil
}

// ConnectedServers obtains all the servers client is connected to.
//
// Deprecated: we can now obtain the remote TCP address of a session from the ClientSession struct directly.
func (ce *Client) ConnectedServers() []string {
	sessions := ce.allClientSessions(ce.porter)
	addrs := make([]string, len(sessions))
	for i, s := range sessions {
		addrs[i] = s.RemoteTCPAddr().String()
	}
	return addrs
}

func (ce *Client) serveWait() {
	bo := ce.bo

	t := time.NewTimer(bo)
	defer t.Stop()

	newBO := time.Duration(float64(bo) * ce.factor)
	if ce.maxBO > 0 && newBO > ce.maxBO {
		newBO = ce.maxBO
	}
	ce.bo = newBO
	<-t.C
}

func hasPK(pks []cipher.PubKey, pk cipher.PubKey) bool {
	for _, oldPK := range pks {
		if oldPK == pk {
			return true
		}
	}
	return false
}

// getCachedRoute returns the server PK that last successfully reached the given destination.
func (ce *Client) getCachedRoute(dst cipher.PubKey) (cipher.PubKey, bool) {
	ce.routeCacheMx.RLock()
	srvPK, ok := ce.routeCache[dst]
	ce.routeCacheMx.RUnlock()
	return srvPK, ok
}

// setCachedRoute records a successful route to a destination via a server.
func (ce *Client) setCachedRoute(dst, srvPK cipher.PubKey) {
	ce.routeCacheMx.Lock()
	ce.routeCache[dst] = srvPK
	ce.routeCacheMx.Unlock()
}

// evictCachedRoute removes a cached route on failure.
func (ce *Client) evictCachedRoute(dst cipher.PubKey) {
	ce.routeCacheMx.Lock()
	delete(ce.routeCache, dst)
	ce.routeCacheMx.Unlock()
}

// getCachedEntry returns a cached discovery entry if it exists and hasn't expired.
func (ce *Client) getCachedEntry(pk cipher.PubKey) (*disc.Entry, bool) {
	ce.entryCacheMx.RLock()
	cached, ok := ce.entryCache[pk]
	ce.entryCacheMx.RUnlock()
	if !ok || time.Since(cached.fetchedAt) > entryCacheTTL {
		return nil, false
	}
	return cached.entry, true
}

// setCachedEntry stores a discovery entry in the cache. Refuses to
// overwrite a permanent entry — those are seeded by SeedEntryCache
// for deployment service PKs (dmsg-discovery itself, address-resolver,
// etc.). Without this guard, a fresh-fetch refresh would clobber the
// seed's far-future timestamp with time.Now(), the entry would expire
// 30 seconds later (entryCacheTTL), and the next DialStream lookup
// for one of those PKs would recurse — disc.Entry fall-through goes
// through the dmsgfirst-wrapped client whose primary path calls
// DialStream(dmsgdiscPK), which cache-misses and asks disc.Entry
// again. Stack overflow within seconds.
//
// Permanent is detected by a fetchedAt > now+50years; SeedEntryCache
// uses now+100years so the threshold gives plenty of headroom.
func (ce *Client) setCachedEntry(pk cipher.PubKey, entry *disc.Entry) {
	const permanentThreshold = 50 * 365 * 24 * time.Hour
	ce.entryCacheMx.Lock()
	defer ce.entryCacheMx.Unlock()
	if existing, ok := ce.entryCache[pk]; ok && time.Until(existing.fetchedAt) > permanentThreshold {
		return
	}
	ce.entryCache[pk] = entryCacheEntry{entry: entry, fetchedAt: time.Now()}
}

// SetDHTLookup sets a callback for DHT-based client entry resolution.
// DialStream calls this before the HTTP discovery, avoiding network
// round-trips for PKs that have published to the DHT.
func (ce *Client) SetDHTLookup(fn func(pk cipher.PubKey) (*disc.Entry, error)) {
	ce.DHTLookup = fn
}

// resolveServerEntry returns the discovery entry for the given dmsg
// server PK, consulting the local entryCache first and only falling
// through to the disc.APIClient (potentially a dmsgfirst-wrapped client
// whose primary path dials over DMSG and would land back here) on a
// cache miss. Configured bootstrap servers are pre-seeded by
// dmsgc.New, so the common dial-session path never reaches the fall-
// through and the dmsgfirst recursion never has a chance to run.
//
// The returned entry is validated to be a server entry — a client
// entry would mean someone passed a non-server PK as srvPK, which is
// a programming error in the caller and we want to surface it the
// same way the bare disc lookup did.
func (ce *Client) resolveServerEntry(ctx context.Context, srvPK cipher.PubKey) (*disc.Entry, error) {
	if entry, ok := ce.getCachedEntry(srvPK); ok && entry != nil && entry.Server != nil {
		return entry, nil
	}
	return getServerEntry(ctx, ce.dc, srvPK)
}

// SeedEntryCache injects a client entry into the discovery cache so that
// DialStream can find the destination without querying the HTTP discovery.
// This is used for deployment services that run as direct DMSG clients
// (they don't register in the public discovery). The caller should set
// entry.Client.DelegatedServers to all known DMSG server PKs so that
// DialStream can try each connected server.
//
// Seeded entries never expire (timestamp set far in the future).
func (ce *Client) SeedEntryCache(pk cipher.PubKey, entry *disc.Entry) {
	ce.entryCacheMx.Lock()
	ce.entryCache[pk] = entryCacheEntry{
		entry:     entry,
		fetchedAt: time.Now().Add(100 * 365 * 24 * time.Hour), // effectively permanent
	}
	ce.entryCacheMx.Unlock()
}

// seededServerEntries returns all server-type discovery entries that have
// been permanently seeded into the entry cache (via SeedEntryCache, typically
// from the dmsgc config's `servers` list). Used by the main Serve loop as
// a fallback when the disc.APIClient cannot supply discovery results — e.g.
// in dmsg-only deployments where the HTTP discovery URL is empty and the
// no-op discovery client returns nil for AvailableServers/AllServers,
// or during a fleet-wide cold-start when no servers have registered yet
// but the configured embedded set is known.
//
// Permanent is detected by a fetchedAt > now+50years; SeedEntryCache uses
// now+100years so the threshold gives plenty of headroom. The returned
// slice is safe to iterate without holding the lock.
func (ce *Client) seededServerEntries() []*disc.Entry {
	const permanentThreshold = 50 * 365 * 24 * time.Hour
	ce.entryCacheMx.RLock()
	defer ce.entryCacheMx.RUnlock()
	out := make([]*disc.Entry, 0, len(ce.entryCache))
	for _, cached := range ce.entryCache {
		if cached.entry == nil || cached.entry.Server == nil {
			continue
		}
		if time.Until(cached.fetchedAt) > permanentThreshold {
			out = append(out, cached.entry)
		}
	}
	return out
}

// DiscEntry looks up a PK in dmsg-discovery and returns the entry if it
// exists as a client with at least one delegated server. Returns an error
// if the PK is not found, is a server entry, or has no delegated servers.
// This is a lightweight check that does NOT dial — useful to pre-filter
// before expensive dial attempts.
func (ce *Client) DiscEntry(ctx context.Context, pk cipher.PubKey) (*disc.Entry, error) {
	return getClientEntry(ctx, ce.dc, pk)
}

// Probe checks whether a remote dmsg client is reachable by performing a
// lightweight DialStream to the specified port and immediately closing.
// On the remote side, the stream is accepted and the noise handshake
// completes — confirming end-to-end DMSG reachability. The remote's
// application-level handler (e.g. router.serveSetup on port 136) will
// typically close the stream quickly (untrusted PK), but that's fine:
// the dial success is all we need.
//
// This is useful as a pre-flight check before expensive operations like
// route setup: if Probe fails, the destination is unreachable and there
// is no point attempting a full route setup that will time out.
func (ce *Client) Probe(ctx context.Context, pk cipher.PubKey, port uint16) bool {
	stream, err := ce.DialStream(ctx, Addr{PK: pk, Port: port})
	if err != nil {
		return false
	}
	_ = stream.Close() //nolint:errcheck
	return true
}

// ProbeViaServer is like Probe but forces the stream through a specific
// dmsg server (serverPK), so it tests reachability of pk:port VIA that
// server rather than via whichever delegated/shared server DialStream
// would otherwise pick. Useful for per-server reachability diagnosis
// ("is pk reachable through server X but not server Y?"). It ensures a
// session to serverPK first, then dials the target through that session.
func (ce *Client) ProbeViaServer(ctx context.Context, pk cipher.PubKey, port uint16, serverPK cipher.PubKey) bool {
	if _, err := ce.EnsureAndObtainSession(ctx, serverPK); err != nil {
		return false
	}
	session, ok := ce.Session(serverPK)
	if !ok {
		return false
	}
	stream, err := session.DialStream(ctx, Addr{PK: pk, Port: port})
	if err != nil {
		return false
	}
	_ = stream.Close() //nolint:errcheck
	return true
}

// reconnectLoop periodically discovers all available servers and attempts to
// connect to any that don't have an active session. This ensures services using
// MinSessions=0 (connect to all) maintain sessions to all servers, even if some
// were unavailable during initial startup.
func (ce *Client) reconnectLoop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ce.done:
			return
		case <-ticker.C:
			ce.reconnectMissing(ctx)
		}
	}
}

func (ce *Client) reconnectMissing(ctx context.Context) {
	entries, err := ce.discoverServers(ctx, false)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if isClosed(ce.done) {
			return
		}
		// Skip servers we already have sessions with
		if _, ok := ce.session(entry.Static); ok {
			continue
		}
		// Filter by server type if configured
		if ce.conf.ConnectedServersType == "official" && entry.Server.ServerType != "official" {
			continue
		}
		if ce.conf.ConnectedServersType == "community" && entry.Server.ServerType != "community" {
			continue
		}
		ce.log.WithField("remote_pk", entry.Static).Debug("Reconnecting to missing server...")
		if err := ce.EnsureSession(ctx, entry); err != nil {
			ce.log.WithField("remote_pk", entry.Static).WithError(err).
				Debug("Reconnect failed, will retry next cycle.")
		} else {
			ce.log.WithField("remote_pk", entry.Static).Info("Reconnected to server.")
		}
	}
}

// pingSessionsLoop periodically pings all sessions. Serves two jobs:
//
//  1. Latency measurement for server-selection in DialStream's
//     sortedDelegatedSessions / sortedMeshSessions.
//  2. Dead-session detection: yamux's own keepalive covers TCP-level
//     liveness but does NOT detect the case where our side of the
//     session is healthy but the dmsg server has torn down its view
//     (e.g. after a server restart or a forwarding-table hiccup).
//     An application-level ping round-trips through the session and
//     exposes this mismatch; 2 consecutive failures close the session
//     so the reconnect loop picks a fresh server and delSession's
//     nudge refreshes the discovery entry.
//
// Cadence: 60 s (down from 1 h, which was latency-only and useless
// for deadness detection). With ~6 sessions per visor the traffic is
// 6 round-trips/min — negligible.
func (ce *Client) pingSessionsLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	// Per-session consecutive-failure counter. Keyed on the session's
	// SessionCommon pointer so reconnects (which create a fresh
	// SessionCommon) reset it naturally.
	fails := make(map[*SessionCommon]int)

	// Do an initial ping immediately.
	ce.pingSessions(ctx, fails)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ce.done:
			return
		case <-ticker.C:
			ce.pingSessions(ctx, fails)
		}
	}
}

// pingDeadThreshold is the consecutive-ping-failure count at which we
// give up on a session and close it. Set to 2 so a single transient
// hiccup (packet loss, brief server blip) does not kill the session.
const pingDeadThreshold = 2

func (ce *Client) pingSessions(_ context.Context, fails map[*SessionCommon]int) {
	sessions := ce.allClientSessions(ce.porter)
	// Track which sessions are currently alive so we can prune the
	// fails map and avoid a slow leak as sessions churn.
	alive := make(map[*SessionCommon]struct{}, len(sessions))
	for _, ses := range sessions {
		key := ses.SessionCommon
		alive[key] = struct{}{}
		rtt, err := ses.Ping()
		if err != nil {
			fails[key]++
			ce.log.WithError(err).
				WithField("server", ses.RemotePK()).
				WithField("consecutive_fails", fails[key]).
				Debug("Session ping failed")
			if fails[key] >= pingDeadThreshold {
				ce.log.WithField("server", ses.RemotePK()).
					WithField("consecutive_fails", fails[key]).
					Warn("Closing dead session (ping threshold exceeded); reconnect loop will re-dial")
				_ = ses.Close() //nolint:errcheck,gosec
				delete(fails, key)
			}
			continue
		}
		fails[key] = 0
		ses.SetLastPing(rtt)
		ce.log.WithField("server", ses.RemotePK()).WithField("rtt", rtt).
			Trace("Session ping measured")
	}
	// Prune fails entries for sessions that no longer exist (closed
	// and not yet re-added). Bounds the map at len(alive).
	for key := range fails {
		if _, ok := alive[key]; !ok {
			delete(fails, key)
		}
	}
}

// porterReapLoop periodically walks the porter and frees ephemeral port
// entries for streams whose owning ClientSession is no longer in the
// client's sessions map. This is defense-in-depth against port leaks:
// ClientSession.Close() reaps on session death, but if a session stays
// alive for hours while individual streams leak (because
// rpc.Client.input() detects EOF but doesn't call codec.Close() on the
// remote end, so some caller paths don't explicitly free the port),
// nothing cleans them up. The reaper bounds the leak at the reap
// interval regardless of root cause.
func (ce *Client) porterReapLoop(ctx context.Context) {
	const interval = 60 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ce.done:
			return
		case <-ticker.C:
			ce.reapOrphanPorts()
		}
	}
}

// reapIdleSessionsLoop periodically closes sessions that are streamless and
// beyond MinSessions, so on-demand rendezvous sessions (opened by the dial path
// to reach a peer delegated to a server this client wasn't connected to, then
// kept alive forever by pingSessionsLoop) don't accumulate — the session count
// settles toward the configured minimum instead of drifting toward "all servers".
// Disabled when MinSessions == 0 (connect-to-all), where every session is
// intentional and reconnectLoop actively re-dials.
func (ce *Client) reapIdleSessionsLoop(ctx context.Context) {
	if ce.MinSessions() <= 0 {
		return
	}
	const interval = 60 * time.Second
	// A session must read as streamless for this many consecutive checks before
	// it's reaped, so a brief gap between requests (or a transient ping stream)
	// doesn't cull an in-use session.
	const idleChecksToReap = 2
	idleStreak := make(map[cipher.PubKey]int)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ce.done:
			return
		case <-ticker.C:
			ce.reapExcessIdleSessions(idleStreak, idleChecksToReap)
		}
	}
}

// reapExcessIdleSessions snapshots the live sessions and their stream counts,
// asks pickIdleSessionsToReap which to close, and closes them. Closing a session
// unwinds its serve goroutine, which calls delSession to drop it from the map.
func (ce *Client) reapExcessIdleSessions(idleStreak map[cipher.PubKey]int, idleChecksToReap int) {
	min := ce.MinSessions()
	if min <= 0 {
		return
	}
	ce.sessionsMx.Lock()
	sessions := make(map[cipher.PubKey]*SessionCommon, len(ce.sessions))
	streams := make(map[cipher.PubKey]int, len(ce.sessions))
	for pk, ses := range ce.sessions {
		sessions[pk] = ses
		streams[pk] = ses.NumStreams()
	}
	ce.sessionsMx.Unlock()

	for _, pk := range pickIdleSessionsToReap(streams, idleStreak, min, idleChecksToReap) {
		if ses := sessions[pk]; ses != nil {
			ce.log.WithField("remote_pk", pk.String()).
				Debug("reaping idle dmsg session (streamless, over min_sessions)")
			_ = ses.Close() //nolint:errcheck
		}
	}
}

// pickIdleSessionsToReap decides which sessions to close given each session's
// open-stream count (0 = idle; a negative "unknown" count — e.g. quic — is
// treated as busy). It advances the per-session idle streak, prunes streaks for
// sessions that have vanished, and returns up to (total - min) sessions that have
// read as streamless for at least idleChecksToReap consecutive calls — so it
// never takes the count below the MinSessions floor and never reaps an in-use
// session. Pure over its inputs (it mutates idleStreak) so the policy is unit
// testable without real sessions.
func pickIdleSessionsToReap(streams, idleStreak map[cipher.PubKey]int, min, idleChecksToReap int) []cipher.PubKey {
	total := len(streams)
	var reap []cipher.PubKey
	for pk, n := range streams {
		if n == 0 {
			idleStreak[pk]++
			if idleStreak[pk] >= idleChecksToReap {
				reap = append(reap, pk)
			}
		} else {
			idleStreak[pk] = 0
		}
	}
	for pk := range idleStreak {
		if _, ok := streams[pk]; !ok {
			delete(idleStreak, pk)
		}
	}
	surplus := total - min
	if surplus <= 0 {
		return nil
	}
	if len(reap) > surplus {
		reap = reap[:surplus]
	}
	return reap
}

// reapOrphanPorts walks the porter and calls the close function for any
// ephemeral-port stream whose owning session is no longer live (not in
// the client's sessions map under the same PK). Safe to call concurrently
// with normal operation — makePortFreer uses sync.Once.
func (ce *Client) reapOrphanPorts() {
	// Snapshot the live session set.
	live := make(map[cipher.PubKey]*SessionCommon)
	ce.sessionsMx.Lock()
	for pk, sc := range ce.sessions {
		live[pk] = sc
	}
	ce.sessionsMx.Unlock()

	var toFree []*Stream
	ce.porter.RangePortValues(func(_ uint16, v interface{}) bool {
		s, ok := v.(*Stream)
		if !ok || s == nil || s.ses == nil || s.ses.SessionCommon == nil {
			return true
		}
		pk := s.ses.RemotePK()
		// If no session exists for this remote PK, or the live session
		// is a different SessionCommon pointer (session was replaced),
		// reap the stream.
		if sc, ok := live[pk]; !ok || sc != s.ses.SessionCommon {
			toFree = append(toFree, s)
		}
		return true
	})

	if len(toFree) == 0 {
		return
	}
	for _, s := range toFree {
		if s.close != nil {
			s.close()
		}
	}
	ce.log.WithField("count", len(toFree)).
		WithField("ports_reserved", ce.porter.Count()).
		Debug("Reaped orphaned ephemeral port reservations")
}
