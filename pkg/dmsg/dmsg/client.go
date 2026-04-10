// Package dmsg pkg/dmsg/client.go
package dmsg

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"math/rand"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
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

	errCh chan error
	done  chan struct{}
	once  sync.Once
	wg    sync.WaitGroup // tracks background goroutines for clean shutdown
	sesMx sync.Mutex
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
		c.sessionsMx.Lock()
		err := c.EntityCommon.updateClientEntry(ctx, c.done, c.conf.ClientType)
		c.sessionsMx.Unlock()
		if err != nil {
			return err
		}
		// Client is 'ready' once we have successfully updated the discovery entry
		// with at least one delegated server.
		c.readyOnce.Do(func() { close(c.ready) })
		return nil
	}

	// Init callback: on delete session.
	c.EntityCommon.delSessionCallback = func(ctx context.Context) error {
		c.sessionsMx.Lock()
		err := c.EntityCommon.updateClientEntry(ctx, c.done, c.conf.ClientType)
		c.sessionsMx.Unlock()
		return err
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
		if ctx.Value("dmsgServer") != nil {
			entries, err = ce.discoverServers(cancellabelCtx, true)
			if err != nil {
				ce.log.WithError(err).Warn("Failed to discover dmsg servers.")
				if err == context.Canceled || err == context.DeadlineExceeded {
					return
				}
				ce.serveWait()
				continue
			}

			for ind, entry := range entries {
				if dmsgServer, ok := ctx.Value("dmsgServer").(string); ok && entry.Static.Hex() == dmsgServer {
					entries = entries[ind : ind+1]
					break
				}
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
					ce.serveWait()
				}
				ce.log.WithField("remote_pk", entry.Static).WithError(err).
					Warn("Failed to establish session.")
			} else {
				// Reset backoff on successful session establishment.
				ce.bo = ce.initBO
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

// setCachedEntry stores a discovery entry in the cache.
func (ce *Client) setCachedEntry(pk cipher.PubKey, entry *disc.Entry) {
	ce.entryCacheMx.Lock()
	ce.entryCache[pk] = entryCacheEntry{entry: entry, fetchedAt: time.Now()}
	ce.entryCacheMx.Unlock()
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

// pingSessionsLoop periodically pings all sessions to measure latency.
// The interval is 1 hour — this is for server selection, not keepalive
// (yamux handles its own keepalives). 30s was excessive and generated
// noisy DEBUG logs (N_clients × N_servers pings every 30s).
func (ce *Client) pingSessionsLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	// Do an initial ping immediately.
	ce.pingSessions()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ce.done:
			return
		case <-ticker.C:
			ce.pingSessions()
		}
	}
}

func (ce *Client) pingSessions() {
	sessions := ce.allClientSessions(ce.porter)
	for _, ses := range sessions {
		rtt, err := ses.Ping()
		if err != nil {
			ce.log.WithError(err).WithField("server", ses.RemotePK()).
				Trace("Ping failed, keeping previous latency measurement")
			continue
		}
		ses.SetLastPing(rtt)
		ce.log.WithField("server", ses.RemotePK()).WithField("rtt", rtt).
			Trace("Session ping measured")
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
