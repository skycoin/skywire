// Package dmsg pkg/dmsg/client.go
package dmsg

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/rand"
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

const entryCacheTTL = 30 * time.Second

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
}

// Ensure ensures all config values are set.
func (c *Config) Ensure() {
	if c.Callbacks == nil {
		c.Callbacks = new(ClientCallbacks)
	}
	c.Callbacks.ensure()
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
	c.addDiscovery(client, "", discPK)
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
		// The first session must update discovery immediately so the
		// client becomes "ready" (other modules wait on c.ready).
		// Subsequent sessions use the debounced nudge path.
		select {
		case <-c.ready:
			// Already ready — nudge the update loop.
			c.nudgeEntryUpdate()
			return nil
		default:
			// First session — update immediately. updateClientEntry
			// takes sessionsMx itself for its snapshot; holding it
			// here would self-deadlock via the dmsgfirst path.
			if err := c.EntityCommon.updateClientEntry(ctx, c.done, c.conf.ClientType); err != nil {
				return err
			}
			c.readyOnce.Do(func() { close(c.ready) })
			return nil
		}
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

	needInitialPost := true

	for {
		if isClosed(ce.done) {
			return
		}
		var entries []*disc.Entry
		var err error
		ce.log.Debug("Discovering dmsg servers...")
		pinned := ctx.Value("dmsgServer") != nil
		if pinned {
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
			entries, err = ce.discoverServers(cancellabelCtx, false)
			if err != nil {
				ce.log.WithError(err).Warn("Failed to discover dmsg servers.")
				if err == context.Canceled || err == context.DeadlineExceeded {
					return
				}
				ce.serveWait()
				continue
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
		// randomize dmsg servers list using crypto/rand seed for true randomization
		// This ensures each client connects to servers in a different order,
		// preventing load imbalance when multiple clients start simultaneously
		var seed int64
		if err := binary.Read(crand.Reader, binary.BigEndian, &seed); err != nil {
			seed = time.Now().UnixNano() // fallback to time-based seed
		}
		rng := rand.New(rand.NewSource(seed)) //nolint:gosec // G404: seed is from crypto/rand, math/rand is fine for shuffling
		rng.Shuffle(len(entries), func(i, j int) {
			entries[i], entries[j] = entries[j], entries[i]
		})

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
			}
		}

		// Only start the update entry loop once we have at least one session established.
		updateEntryLoopOnce.Do(func() { go ce.updateClientEntryLoop(cancellabelCtx, ce.done, ce.conf.ClientType) })
		pingLoopOnce.Do(func() { go ce.pingSessionsLoop(cancellabelCtx) })
		porterReapLoopOnce.Do(func() { go ce.porterReapLoop(cancellabelCtx) })
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
