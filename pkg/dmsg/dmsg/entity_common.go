// Package dmsg pkg/dmsg/entity_common.go
package dmsg

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
)

// discoveryEndpoint represents one dmsg-discovery the entity registers
// with. AdvertisedAddr applies to servers only — clients pass empty.
// When non-empty, AdvertisedAddr overrides the addr passed to
// updateServerEntryLoop for THIS discovery only, supporting the
// "advertise LAN to local-disc, advertise public IP to internet-disc"
// pattern. PK, when set, identifies the discovery's own dmsg-server
// PK so the entity can recognize an inbound session as originating
// from that discovery and trigger an immediate registration push.
type discoveryEndpoint struct {
	Client         disc.APIClient
	AdvertisedAddr string
	PK             cipher.PubKey
}

// EntityCommon contains the common fields and methods for server and client entities.
type EntityCommon struct {
	// atomic requires 64-bit alignment for struct field access
	lastUpdate int64 // Timestamp (in unix seconds) of last update.

	pk cipher.PubKey
	sk cipher.SecKey
	// dc is the primary discovery — kept as a separate field for
	// backward compatibility with constructors and zero-value behavior.
	// Equivalent to discoveries[0].Client when discoveries is non-empty.
	dc disc.APIClient
	// discoveries holds the primary plus any additional dmsg-discoveries
	// the entity registers with. The primary is always discoveries[0]
	// (mirror of c.dc) when discoveries is non-empty. Extra discoveries
	// are appended via addDiscovery; ordering is preserved so callers
	// can control which is "first" for read fallback.
	discoveries   []*discoveryEndpoint
	discoveriesMx sync.RWMutex

	sessions   map[cipher.PubKey]*SessionCommon
	sessionsMx *sync.Mutex

	updateInterval time.Duration // Minimum duration between discovery entry updates.

	log  logrus.FieldLogger
	mlog *logging.MasterLogger

	setSessionCallback func(ctx context.Context) error
	delSessionCallback func(ctx context.Context) error

	// peerSessionsFunc returns peer server sessions for mesh forwarding.
	// Only set on Server entities; nil for clients.
	peerSessionsFunc func() []*SessionCommon

	// lastPushedSrvPKs is the set of delegated server PKs most recently
	// pushed to the dmsg discovery. It is used by updateClientEntry to
	// short-circuit redundant GET/PUT round-trips when nothing has
	// actually changed — the previous code issued a GET on every single
	// session add/remove even when the resulting delegated-servers list
	// was identical, which on production generated sustained hundreds of
	// MB/s of discovery traffic during reconnect storms. Protected by
	// pushedSrvPKsMx.
	lastPushedSrvPKs []cipher.PubKey
	pushedSrvPKsMx   sync.Mutex

	// dhtBootstrap indicates this entity runs a DHT full node.
	// Set by the DMSG server when enable_dht is true. Propagated
	// to the discovery entry so visors know which servers to use
	// for DHT bootstrap.
	dhtBootstrap bool

	// entryNudge signals that a session was added or removed and the
	// discovery entry should be updated. The update loop debounces
	// rapid signals (e.g., connecting to 6 servers at startup) into
	// a single batched update, matching the transport manager's
	// re-registration debounce pattern.
	entryNudge chan struct{}
}

func (c *EntityCommon) init(pk cipher.PubKey, sk cipher.SecKey, dc disc.APIClient, log logrus.FieldLogger, updateInterval time.Duration) {
	if updateInterval == 0 {
		updateInterval = DefaultUpdateInterval
	}
	c.pk = pk
	c.sk = sk
	c.dc = dc
	c.discoveries = []*discoveryEndpoint{{Client: dc}}
	c.sessions = make(map[cipher.PubKey]*SessionCommon)
	c.sessionsMx = new(sync.Mutex)
	c.updateInterval = updateInterval
	c.entryNudge = make(chan struct{}, 1)
	c.log = log
}

// addDiscovery appends an additional dmsg-discovery to register with.
// advertisedAddr, when non-empty, overrides the entity's default addr
// for this discovery (servers only — clients should pass empty). pk,
// when non-zero, identifies the discovery's own dmsg-server PK so the
// entity can detect inbound sessions from this discovery.
//
// Safe to call after init/Serve has started — subsequent
// updateServerEntryLoop iterations will pick up the new endpoint.
func (c *EntityCommon) addDiscovery(client disc.APIClient, advertisedAddr string, pk cipher.PubKey) {
	if client == nil {
		return
	}
	c.discoveriesMx.Lock()
	c.discoveries = append(c.discoveries, &discoveryEndpoint{
		Client:         client,
		AdvertisedAddr: advertisedAddr,
		PK:             pk,
	})
	c.discoveriesMx.Unlock()
}

// setDiscoveryClients replaces the disc.APIClient on each existing
// endpoint, in-place. The lengths must match, otherwise the call is a
// no-op. Used to upgrade plain HTTP clients to dmsgfirst-wrapped
// clients once an outbound dmsg.Client is ready, without reordering
// or losing per-discovery advertised addresses / PKs.
func (c *EntityCommon) setDiscoveryClients(clients []disc.APIClient) {
	c.discoveriesMx.Lock()
	defer c.discoveriesMx.Unlock()
	if len(clients) != len(c.discoveries) {
		return
	}
	for i, cl := range clients {
		if cl != nil {
			c.discoveries[i].Client = cl
		}
	}
	if len(c.discoveries) > 0 {
		c.dc = c.discoveries[0].Client
	}
}

// snapshotDiscoveries returns a copy of the discovery list, safe for
// iteration outside the lock. The returned slice may be empty when
// init has not yet been called.
func (c *EntityCommon) snapshotDiscoveries() []*discoveryEndpoint {
	c.discoveriesMx.RLock()
	defer c.discoveriesMx.RUnlock()
	if len(c.discoveries) == 0 {
		return nil
	}
	out := make([]*discoveryEndpoint, len(c.discoveries))
	copy(out, c.discoveries)
	return out
}

// LocalPK returns the local public key of the entity.
func (c *EntityCommon) LocalPK() cipher.PubKey { return c.pk }

// LocalSK returns the local secret key of the entity.
func (c *EntityCommon) LocalSK() cipher.SecKey { return c.sk }

// Logger obtains the logger.
func (c *EntityCommon) Logger() logrus.FieldLogger { return c.log }

// SetLogger sets the internal logger.
// This should be called before we serve.
func (c *EntityCommon) SetLogger(log logrus.FieldLogger) { c.log = log }

// SetDHTBootstrap marks this entity as a DHT bootstrap node.
// When set, the server's discovery entry includes dht_bootstrap=true
// so visors know to use this server for DHT bootstrapping.
func (c *EntityCommon) SetDHTBootstrap(enabled bool) { c.dhtBootstrap = enabled }

// MasterLogger obtains the master logger.
func (c *EntityCommon) MasterLogger() *logging.MasterLogger { return c.mlog }

// SetMasterLogger sets the internal master logger.
// This should be called before we serve.
func (c *EntityCommon) SetMasterLogger(mlog *logging.MasterLogger) { c.mlog = mlog }

func (c *EntityCommon) session(pk cipher.PubKey) (*SessionCommon, bool) {
	c.sessionsMx.Lock()
	dSes, ok := c.sessions[pk]
	c.sessionsMx.Unlock()
	return dSes, ok
}

// serverSession obtains a session as a server.
func (c *EntityCommon) serverSession(pk cipher.PubKey) (ServerSession, bool) {
	ses, ok := c.session(pk)
	return ServerSession{SessionCommon: ses}, ok
}

// peerServerSessions returns all peer server sessions for mesh forwarding.
func (c *EntityCommon) peerServerSessions() []ServerSession {
	if c.peerSessionsFunc == nil {
		return nil
	}
	raw := c.peerSessionsFunc()
	sessions := make([]ServerSession, len(raw))
	for i, ses := range raw {
		sessions[i] = ServerSession{SessionCommon: ses}
	}
	return sessions
}

// clientSession obtains a session as a client.
func (c *EntityCommon) clientSession(porter *netutil.Porter, pk cipher.PubKey) (ClientSession, bool) {
	ses, ok := c.session(pk)
	return ClientSession{SessionCommon: ses, porter: porter}, ok
}

func (c *EntityCommon) allClientSessions(porter *netutil.Porter) []ClientSession {
	c.sessionsMx.Lock()
	sessions := make([]ClientSession, 0, len(c.sessions))
	for _, ses := range c.sessions {
		sessions = append(sessions, ClientSession{SessionCommon: ses, porter: porter})
	}
	c.sessionsMx.Unlock()
	return sessions
}

// SessionCount returns the number of sessions.
func (c *EntityCommon) SessionCount() int {
	c.sessionsMx.Lock()
	n := len(c.sessions)
	c.sessionsMx.Unlock()
	return n
}

func (c *EntityCommon) setSession(ctx context.Context, dSes *SessionCommon) bool {
	c.sessionsMx.Lock()
	if _, ok := c.sessions[dSes.RemotePK()]; ok {
		c.sessionsMx.Unlock()
		return false
	}
	c.sessions[dSes.RemotePK()] = dSes
	cb := c.setSessionCallback
	c.sessionsMx.Unlock()

	if cb != nil {
		if err := cb(ctx); err != nil {
			c.log.
				WithField("func", "EntityCommon.setSession").
				WithError(err).
				Warn("Callback returned non-nil error.\n")
		}
	}
	return true
}

func (c *EntityCommon) delSession(ctx context.Context, pk cipher.PubKey) {
	c.sessionsMx.Lock()
	delete(c.sessions, pk)
	cb := c.delSessionCallback
	c.sessionsMx.Unlock()

	if cb != nil {
		if err := cb(ctx); err != nil {
			c.log.
				WithField("func", "EntityCommon.delSession").
				WithError(err).
				Warn("Callback returned non-nil error.\n")
		}
	}
}

// updateServerEntry updates the dmsg server's entry within all
// configured dmsg-discoveries. Each endpoint is updated independently
// — a per-discovery advertised address (when set) overrides the
// default addr argument for that endpoint only.
//
// If 'addr' is an empty string AND no endpoint provides its own
// advertised address, no update is performed for that endpoint
// (preserving the legacy "empty addr = skip update" semantics).
//
// Caller must hold c.sessionsMx.
func (c *EntityCommon) updateServerEntry(ctx context.Context, addr string, maxSessions int, authPassphrase string) (err error) {
	endpoints := c.snapshotDiscoveries()
	if len(endpoints) == 0 {
		return errors.New("updateServerEntry: no discoveries configured")
	}

	availableSessions := maxSessions - len(c.sessions)
	if availableSessions < 0 {
		availableSessions = 0
	}

	// Aggregate errors across endpoints. We return the first non-nil
	// error so the caller's existing "log on error, retry on next
	// tick" behavior keeps working — a transient failure on one
	// discovery doesn't mask a successful update to another.
	var firstErr error
	anyOK := false
	for _, ep := range endpoints {
		epAddr := addr
		if ep.AdvertisedAddr != "" {
			epAddr = ep.AdvertisedAddr
		}
		if epAddr == "" {
			continue
		}
		if updateErr := c.updateServerEntryOnEndpoint(ctx, ep, epAddr, availableSessions, authPassphrase); updateErr != nil {
			if firstErr == nil {
				firstErr = updateErr
			}
			c.log.WithError(updateErr).WithField("discovery_pk", ep.PK).Debug("server entry update failed for discovery; will retry next tick")
			continue
		}
		anyOK = true
	}
	if anyOK {
		c.recordUpdate()
	}
	return firstErr
}

// updateServerEntryOnEndpoint runs the read-modify-write registration
// cycle against a single discovery endpoint.
func (c *EntityCommon) updateServerEntryOnEndpoint(ctx context.Context, ep *discoveryEndpoint, addr string, availableSessions int, authPassphrase string) error {
	entry, err := ep.Client.Entry(ctx, c.pk)
	if err != nil {
		entry = disc.NewServerEntry(c.pk, 0, addr, availableSessions)
		entry.Server.DHTBootstrap = c.dhtBootstrap
		if err := entry.Sign(c.sk); err != nil {
			return err
		}
		return ep.Client.PostEntry(ctx, entry)
	}

	if entry.Server == nil {
		return errors.New("entry in discovery is not of a dmsg server")
	}

	if authPassphrase != "" {
		entry.Server.ServerType = authPassphrase
	}

	sessionsDelta := entry.Server.AvailableSessions != availableSessions
	addrDelta := entry.Server.Address != addr

	// No update needed if entry has no delta AND update is not due.
	if _, due := c.updateIsDue(); !sessionsDelta && !addrDelta && !due {
		return nil
	}

	log := c.log
	if sessionsDelta {
		entry.Server.AvailableSessions = availableSessions
		log = log.WithField("available_sessions", entry.Server.AvailableSessions)
	}
	if addrDelta {
		entry.Server.Address = addr
		log = log.WithField("addr", entry.Server.Address)
	}
	// Propagate DHT bootstrap status to discovery entry.
	entry.Server.DHTBootstrap = c.dhtBootstrap
	log.Debug("Updating entry.\n")

	return ep.Client.PutEntry(ctx, c.sk, entry)
}

func (c *EntityCommon) updateServerEntryLoop(ctx context.Context, addr string, maxSessions int, authPassphrase string) {
	t := time.NewTimer(c.updateInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-t.C:
			if lastUpdate, due := c.updateIsDue(); !due {
				t.Reset(c.updateInterval - time.Since(lastUpdate))
				continue
			}

			c.sessionsMx.Lock()
			err := c.updateServerEntry(ctx, addr, maxSessions, authPassphrase)
			c.sessionsMx.Unlock()

			if err != nil {
				c.log.WithError(err).Warn("Failed to update discovery entry.")
			}

			t.Reset(c.updateInterval)

		case <-c.entryNudge:
			// Client connected/disconnected — debounce to batch rapid changes.
			select {
			case <-time.After(entryUpdateDebounce):
			case <-ctx.Done():
				return
			}
			// Drain additional nudges.
			for {
				select {
				case <-c.entryNudge:
				default:
					goto serverUpdate
				}
			}
		serverUpdate:
			c.sessionsMx.Lock()
			err := c.updateServerEntry(ctx, addr, maxSessions, authPassphrase)
			c.sessionsMx.Unlock()
			if err != nil {
				c.log.WithError(err).Warn("Failed to update discovery entry (nudge).")
			}
			t.Reset(c.updateInterval)
		}
	}
}

func (c *EntityCommon) initilizeClientEntry(ctx context.Context, clientType string, protocol string) (err error) {
	endpoints := c.snapshotDiscoveries()
	if len(endpoints) == 0 {
		return errors.New("initilizeClientEntry: no discoveries configured")
	}

	c.sessionsMx.Lock()
	srvPKs := make([]cipher.PubKey, 0, len(c.sessions))
	for pk := range c.sessions {
		srvPKs = append(srvPKs, pk)
	}
	c.sessionsMx.Unlock()

	var firstErr error
	anyOK := false
	for _, ep := range endpoints {
		if _, lookupErr := ep.Client.Entry(ctx, c.pk); lookupErr == nil {
			anyOK = true
			continue
		}
		entry := disc.NewClientEntry(c.pk, 0, srvPKs)
		entry.ClientType = clientType
		entry.Protocol = protocol
		if signErr := entry.Sign(c.sk); signErr != nil {
			if firstErr == nil {
				firstErr = signErr
			}
			continue
		}
		if postErr := ep.Client.PostEntry(ctx, entry); postErr != nil {
			if firstErr == nil {
				firstErr = postErr
			}
			continue
		}
		anyOK = true
	}
	if anyOK {
		c.recordUpdate()
	}
	return firstErr
}

func (c *EntityCommon) updateClientEntry(ctx context.Context, done chan struct{}, clientType string) (err error) {
	if isClosed(done) {
		return nil
	}

	endpoints := c.snapshotDiscoveries()
	if len(endpoints) == 0 {
		return errors.New("updateClientEntry: no discoveries configured")
	}

	srvPKs := make([]cipher.PubKey, 0, len(c.sessions))
	for pk := range c.sessions {
		srvPKs = append(srvPKs, pk)
	}

	// Short-circuit: if the delegated server set we're about to publish
	// is identical to what we last successfully pushed AND an update
	// isn't due on the timer, skip both the GET and the PUT. Without
	// this guard every session add/remove generates an HTTP GET to
	// dmsg-discovery regardless of whether anything actually changed,
	// which under reconnect storms sends the discovery service into
	// 100%+ CPU and saturates its ingress bandwidth.
	c.pushedSrvPKsMx.Lock()
	lastPushed := c.lastPushedSrvPKs
	c.pushedSrvPKsMx.Unlock()
	if _, due := c.updateIsDue(); !due && lastPushed != nil && cipher.SamePubKeys(srvPKs, lastPushed) {
		return nil
	}

	var firstErr error
	anyOK := false
	for _, ep := range endpoints {
		if updateErr := c.updateClientEntryOnEndpoint(ctx, ep, clientType, srvPKs); updateErr != nil {
			if firstErr == nil {
				firstErr = updateErr
			}
			c.log.WithError(updateErr).Debug("client entry update failed for discovery; will retry next tick")
			continue
		}
		anyOK = true
	}
	if anyOK {
		c.recordUpdate()
		c.pushedSrvPKsMx.Lock()
		// Defensive copy — caller may retain srvPKs in log output, so
		// copying is cheap and keeps ownership clean.
		pushed := make([]cipher.PubKey, len(srvPKs))
		copy(pushed, srvPKs)
		c.lastPushedSrvPKs = pushed
		c.pushedSrvPKsMx.Unlock()
	}
	return firstErr
}

// updateClientEntryOnEndpoint runs the read-modify-write registration
// cycle against a single discovery endpoint.
func (c *EntityCommon) updateClientEntryOnEndpoint(ctx context.Context, ep *discoveryEndpoint, clientType string, srvPKs []cipher.PubKey) error {
	entry, err := ep.Client.Entry(ctx, c.pk)
	if err != nil {
		entry = disc.NewClientEntry(c.pk, 0, srvPKs)
		entry.ClientType = clientType
		if err := entry.Sign(c.sk); err != nil {
			return err
		}
		return ep.Client.PostEntry(ctx, entry)
	}

	// The entry might be a server entry (e.g., debug client running on a dmsg server).
	// In that case, entry.Client is nil and we need to create a new client entry.
	if entry.Client == nil {
		entry = disc.NewClientEntry(c.pk, 0, srvPKs)
		entry.ClientType = clientType
		if err := entry.Sign(c.sk); err != nil {
			return err
		}
		return ep.Client.PostEntry(ctx, entry)
	}

	// Whether the client's CURRENT delegated servers is the same as what would be advertised.
	sameSrvPKs := cipher.SamePubKeys(srvPKs, entry.Client.DelegatedServers)

	// No update is needed if delegated servers has no delta, and an entry update is not due.
	if _, due := c.updateIsDue(); sameSrvPKs && !due {
		return nil
	}

	entry.ClientType = clientType
	entry.Client.DelegatedServers = srvPKs
	c.log.WithField("entry", entry).Debug("Updating entry.\n")
	return ep.Client.PutEntry(ctx, c.sk, entry)
}

// entryUpdateDebounce is how long to wait after a nudge before updating
// the discovery entry. This batches rapid session changes (e.g.,
// connecting to 6 servers at startup) into a single update.
//
// Reduced from 5 s to 1 s because the old value created a 5-second
// window in which the discovery entry still listed a dead server as a
// delegated server. Remote dials to this visor during that window
// would pick the dead server, fail with "cannot connect to delegated
// server" (dmsg error 202), and blow through the RSN's circuit
// breaker budget — see the production pathology where popular public
// visors accumulated open circuits because of this race. 1 s is still
// enough to coalesce the startup burst (typically 6 sessions
// establishing within <200 ms of each other) into a single update
// while shrinking the stale-entry window by 80 %.
const entryUpdateDebounce = 1 * time.Second

func (c *EntityCommon) updateClientEntryLoop(ctx context.Context, done chan struct{}, clientType string) {
	t := time.NewTimer(c.updateInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-t.C:
			if lastUpdate, due := c.updateIsDue(); !due {
				t.Reset(c.updateInterval - time.Since(lastUpdate))
				continue
			}

			c.sessionsMx.Lock()
			err := c.updateClientEntry(ctx, done, clientType)
			c.sessionsMx.Unlock()

			if err != nil {
				c.log.WithError(err).Warn("Failed to update discovery entry.")
			}

			t.Reset(c.updateInterval)

		case <-c.entryNudge:
			// Session added/removed — debounce to batch rapid changes.
			select {
			case <-time.After(entryUpdateDebounce):
			case <-ctx.Done():
				return
			}
			// Drain any additional nudges that arrived during debounce.
			for {
				select {
				case <-c.entryNudge:
				default:
					goto clientUpdate
				}
			}
		clientUpdate:
			c.sessionsMx.Lock()
			err := c.updateClientEntry(ctx, done, clientType)
			c.sessionsMx.Unlock()
			if err != nil {
				c.log.WithError(err).Warn("Failed to update discovery entry (nudge).")
			}
			t.Reset(c.updateInterval)
		}
	}
}

func (c *EntityCommon) entryProtocol(ctx context.Context, pk cipher.PubKey) string {
	endpoints := c.snapshotDiscoveries()
	for _, ep := range endpoints {
		entry, err := ep.Client.Entry(ctx, pk)
		if err != nil {
			continue
		}
		c.log.WithField("entry", entry).Debug("Entry's protocol fetch.\n")
		return entry.Protocol
	}
	c.log.WithField("pk", pk).Warn("Entry not found in any discovery; returning empty protocol.\n")
	return ""
}

func (c *EntityCommon) delEntry(ctx context.Context) (err error) {
	endpoints := c.snapshotDiscoveries()
	if len(endpoints) == 0 {
		return errors.New("delEntry: no discoveries configured")
	}

	defer func() {
		if err == nil {
			c.log.Debug("Entry Deleted successfully.")
		}
	}()

	var firstErr error
	for _, ep := range endpoints {
		entry, lookupErr := ep.Client.Entry(ctx, c.pk)
		if lookupErr != nil {
			if firstErr == nil {
				firstErr = lookupErr
			}
			continue
		}
		c.log.WithField("entry", entry).Debug("Deleting entry.\n")
		if delErr := ep.Client.DelEntry(ctx, entry); delErr != nil && firstErr == nil {
			firstErr = delErr
		}
	}
	return firstErr
}

func getServerEntry(ctx context.Context, dc disc.APIClient, srvPK cipher.PubKey) (*disc.Entry, error) {
	entry, err := dc.Entry(ctx, srvPK)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDiscEntryNotFound, err)
	}
	if entry.Server == nil {
		return nil, ErrDiscEntryIsNotServer
	}
	return entry, nil
}

func getClientEntry(ctx context.Context, dc disc.APIClient, clientPK cipher.PubKey) (*disc.Entry, error) {
	entry, err := dc.Entry(ctx, clientPK)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDiscEntryNotFound, err)
	}
	if entry.Client == nil {
		return nil, ErrDiscEntryIsNotClient
	}
	if len(entry.Client.DelegatedServers) == 0 {
		return nil, ErrDiscEntryHasNoDelegated
	}
	return entry, nil
}

/*
	<<< Update interval helpers >>>
*/

func (c *EntityCommon) updateIsDue() (lastUpdate time.Time, isDue bool) {
	lastUpdate = time.Unix(0, atomic.LoadInt64(&c.lastUpdate))
	isDue = time.Since(lastUpdate) >= c.updateInterval
	return lastUpdate, isDue
}

// nudgeEntryUpdate signals the update loop that a session changed and
// the discovery entry should be refreshed. Non-blocking — if a nudge
// is already pending, the loop will pick it up.
func (c *EntityCommon) nudgeEntryUpdate() {
	select {
	case c.entryNudge <- struct{}{}:
	default:
	}
}

func (c *EntityCommon) recordUpdate() {
	atomic.StoreInt64(&c.lastUpdate, time.Now().UnixNano())
}
