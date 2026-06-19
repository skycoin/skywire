// Package dmsg pkg/dmsg/entity_common.go
package dmsg

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	Client           disc.APIClient
	AdvertisedAddr   string
	AdvertisedAddrV6 string
	PK               cipher.PubKey
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

	// advertisedUDPAddr is the QUIC (UDP) endpoint a server also listens on,
	// set by Server.ServeQUIC. When non-empty, the discovery entry carries
	// Server.AddressUDP + Protocol="quic" so QUIC-capable clients dial QUIC
	// (#2607 dmsg-over-QUIC). Empty for clients and TCP-only servers.
	advertisedUDPAddr string

	// noListenerHits records inbound stream requests to ports this client has
	// no listener on (dmsg error 306). Bounded; surfaced via NoListenerHits.
	noListenerHits *portHitTracker

	log  logrus.FieldLogger
	mlog *logging.MasterLogger

	setSessionCallback func(ctx context.Context) error
	delSessionCallback func(ctx context.Context) error

	// peerSessionsFunc returns peer server sessions for mesh forwarding.
	// Only set on Server entities; nil for clients.
	peerSessionsFunc func() []*SessionCommon

	// acceptPeerAnnouncements, peerAnnounceAllowedFunc and
	// promoteToPeerFunc support inbound peer announcements: a non-public
	// server (not in discovery, never dialed by anyone) announcing itself
	// as a forwardable peer over the link it dials out. Only set on
	// Server entities that opt in. promoteToPeerFunc files the announced
	// inbound session in the server's peer set so the reverse path can
	// forward client streams to it.
	acceptPeerAnnouncements bool
	peerAnnounceAllowedFunc func(remotePK cipher.PubKey) bool
	promoteToPeerFunc       func(remotePK cipher.PubKey, ses *SessionCommon)

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

	// geoLookup is set by Server.SetGeoLookup. Server-side IP-info
	// stream handler reads it; client entities leave it nil.
	geoLookup GeoLookupFunc
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
	c.noListenerHits = newPortHitTracker(defaultPortHitCap)
	c.log = log
}

// NoListenerHits returns a snapshot of inbound stream requests this client
// rejected because no listener was bound on the destination port (dmsg error
// 306), keyed by (source PK, destination port), most-recent first.
func (c *EntityCommon) NoListenerHits() []PortHit {
	return c.noListenerHits.snapshot()
}

// recordNoListenerHit notes one no-listener inbound request. Nil-safe.
func (c *EntityCommon) recordNoListenerHit(srcPK cipher.PubKey, dstPort uint16) {
	c.noListenerHits.record(srcPK, dstPort)
}

// addDiscovery appends an additional dmsg-discovery to register with.
// advertisedAddr, when non-empty, overrides the entity's default addr
// for this discovery (servers only — clients should pass empty).
// advertisedAddrV6, when non-empty, is the optional IPv6 endpoint
// counterpart to advertisedAddr — stored in disc.Server.AddressV6 on
// the next updateServerEntry tick. Empty preserves single-stack v4
// advertisement (the pre-#1525 default). pk, when non-zero, identifies
// the discovery's own dmsg-server PK so the entity can detect inbound
// sessions from this discovery.
//
// Safe to call after init/Serve has started — subsequent
// updateServerEntryLoop iterations will pick up the new endpoint.
func (c *EntityCommon) addDiscovery(client disc.APIClient, advertisedAddr, advertisedAddrV6 string, pk cipher.PubKey) {
	if client == nil {
		return
	}
	c.discoveriesMx.Lock()
	c.discoveries = append(c.discoveries, &discoveryEndpoint{
		Client:           client,
		AdvertisedAddr:   advertisedAddr,
		AdvertisedAddrV6: advertisedAddrV6,
		PK:               pk,
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

// peerAnnounceAllowed reports whether an inbound peer announcement from
// remotePK should be honored (feature opted in + PK allowlisted).
func (c *EntityCommon) peerAnnounceAllowed(remotePK cipher.PubKey) bool {
	if c.peerAnnounceAllowedFunc == nil {
		return false
	}
	return c.peerAnnounceAllowedFunc(remotePK)
}

// promoteToPeer files an announced inbound session in the server's peer
// set so the reverse path can forward client streams to it. No-op when
// the feature is not configured.
func (c *EntityCommon) promoteToPeer(remotePK cipher.PubKey, ses *SessionCommon) {
	if c.promoteToPeerFunc != nil {
		c.promoteToPeerFunc(remotePK, ses)
	}
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

// setSession installs dSes as THE session for its remote PK and always
// succeeds. If a session for that PK already exists it is replaced and the
// predecessor is closed.
//
// Newest-session-wins is the correct policy because exactly one session per
// remote PK is ever valid: a well-behaved peer keeps a single session per
// remote and only re-dials after its previous link died. The common case is a
// half-dead link — the TCP died without a FIN/RST, so the old session's serve
// loop is still parked in AcceptStream and has NOT errored out, leaving the
// stale session in the map. The previous policy REJECTED the reconnecting
// session and kept the corpse, so:
//   - inbound stream bridging (server_session.go: serverSession →
//     forwardRequest) kept targeting the dead session and blocked for the
//     full HandshakeTimeout on every dial — observed as "delegated server
//     advertised but unreachable (5s)", which starved multihop route setup
//     (every hop dialed pk@136 hung 5s, exhausting the id-reservation budget
//     → "dmsg error 202 - cannot connect to delegated server"); and
//   - zombie accept-loop goroutines accumulated (21 serve goroutines observed
//     on a visor with only 8 reachable servers).
//
// The evicted predecessor's serve goroutine unwinds and calls delSession,
// which is identity-checked so it cannot remove the live replacement.
func (c *EntityCommon) setSession(ctx context.Context, dSes *SessionCommon) {
	c.sessionsMx.Lock()
	old, hadOld := c.sessions[dSes.RemotePK()]
	if hadOld && old == dSes {
		c.sessionsMx.Unlock()
		return
	}
	c.sessions[dSes.RemotePK()] = dSes
	cb := c.setSessionCallback
	c.sessionsMx.Unlock()

	if hadOld {
		// Close the stale predecessor OUTSIDE sessionsMx — Close does IO
		// and can take other locks; holding sessionsMx across it risks the
		// serveStream→session self-deadlock documented in updateServerEntry.
		_ = old.Close() //nolint:errcheck
	}

	if cb != nil {
		if err := cb(ctx); err != nil {
			c.log.
				WithField("func", "EntityCommon.setSession").
				WithError(err).
				Warn("Callback returned non-nil error.\n")
		}
	}
}

// delSession removes the session for pk, but only when the currently-stored
// session is dSes. The identity check is essential now that setSession
// replaces stale sessions on reconnect: when an evicted predecessor's serve
// goroutine unwinds it calls delSession, and an unconditional delete would
// remove the LIVE replacement that setSession just installed. A nil dSes
// deletes unconditionally for callers that hold no session reference.
func (c *EntityCommon) delSession(ctx context.Context, pk cipher.PubKey, dSes *SessionCommon) {
	c.sessionsMx.Lock()
	cur, ok := c.sessions[pk]
	if !ok || (dSes != nil && cur != dSes) {
		c.sessionsMx.Unlock()
		return
	}
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
func (c *EntityCommon) updateServerEntry(ctx context.Context, addr, addrV6 string, maxSessions int, authPassphrase string) (err error) {
	endpoints := c.snapshotDiscoveries()
	if len(endpoints) == 0 {
		return errors.New("updateServerEntry: no discoveries configured")
	}

	// Snapshot session count under sessionsMx and release before any IO.
	// Holding sessionsMx across ep.Client.Entry / PutEntry self-deadlocks
	// the server: when the disc client is dmsgfirst, the discovery roundtrip
	// dials over DMSG, and every concurrent stream forward through this
	// server (server_session.go: serveStream → entity.serverSession →
	// EntityCommon.session) wants the same mutex. Same shape as the
	// client-side fix in PR #2443.
	c.sessionsMx.Lock()
	availableSessions := maxSessions - len(c.sessions)
	c.sessionsMx.Unlock()
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
		// V6 follows the same per-endpoint-override semantics as v4:
		// a non-empty per-endpoint AdvertisedAddrV6 wins; otherwise
		// fall back to the entity-level addrV6 (empty when the
		// server isn't dual-stack-configured). Empty addrV6 → v4-only
		// entry, preserving backward compat for pre-#1525 servers.
		epAddrV6 := addrV6
		if ep.AdvertisedAddrV6 != "" {
			epAddrV6 = ep.AdvertisedAddrV6
		}
		if updateErr := c.updateServerEntryOnEndpoint(ctx, ep, epAddr, epAddrV6, availableSessions, authPassphrase); updateErr != nil {
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
// cycle against a single discovery endpoint. addrV6 is the optional
// IPv6 counterpart to addr — empty when the server is v4-only, which
// keeps the entry's AddressV6 field omitempty-elided exactly as
// pre-#1525.
func (c *EntityCommon) updateServerEntryOnEndpoint(ctx context.Context, ep *discoveryEndpoint, addr, addrV6 string, availableSessions int, authPassphrase string) error {
	entry, err := ep.Client.Entry(ctx, c.pk)
	if err != nil {
		entry = disc.NewServerEntry(c.pk, 0, addr, availableSessions)
		entry.Server.AddressV6 = addrV6
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
	addrV6Delta := entry.Server.AddressV6 != addrV6
	udpDelta := entry.Server.AddressUDP != c.advertisedUDPAddr

	// No update needed if entry has no delta AND update is not due.
	if _, due := c.updateIsDue(); !sessionsDelta && !addrDelta && !addrV6Delta && !udpDelta && !due {
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
	if addrV6Delta {
		entry.Server.AddressV6 = addrV6
		log = log.WithField("addr_v6", entry.Server.AddressV6)
	}
	// Advertise the QUIC (UDP) endpoint + Protocol "quic" when the server runs
	// a QUIC listener (#2607 dmsg-over-QUIC). QUIC-capable clients dial it;
	// others keep using Address (TCP).
	if c.advertisedUDPAddr != "" {
		entry.Server.AddressUDP = c.advertisedUDPAddr
		entry.Protocol = "quic"
		log = log.WithField("addr_udp", entry.Server.AddressUDP)
	}
	// Propagate DHT bootstrap status to discovery entry.
	entry.Server.DHTBootstrap = c.dhtBootstrap
	log.Debug("Updating entry.\n")

	return ep.Client.PutEntry(ctx, c.sk, entry)
}

func (c *EntityCommon) updateServerEntryLoop(ctx context.Context, addr, addrV6 string, maxSessions int, authPassphrase string) {
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

			err := c.updateServerEntry(ctx, addr, addrV6, maxSessions, authPassphrase)
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
			err := c.updateServerEntry(ctx, addr, addrV6, maxSessions, authPassphrase)
			if err != nil {
				c.log.WithError(err).Warn("Failed to update discovery entry (nudge).")
			}
			t.Reset(c.updateInterval)
		}
	}
}

// isEntryNotFound reports whether err is the discovery's "entry not found"
// result — i.e. the client genuinely has no entry yet, so a fresh sequence-0
// registration is correct. It checks BOTH the typed sentinel and the message
// string: the raw httpClient returns the disc.ErrKeyNotFound sentinel, but the
// dmsgfirst/fallback clients and the test mock stringify it across the
// dmsg-HTTP boundary (the same reason dmsgfirst string-matches the 202 error),
// so errors.Is alone is not reliable here. Any OTHER error is transient (the
// entry probably exists at a sequence we just couldn't read) and must NOT
// trigger a sequence-0 re-registration, which the discovery rejects 422.
func isEntryNotFound(err error) bool {
	return err != nil &&
		(errors.Is(err, disc.ErrKeyNotFound) ||
			strings.Contains(err.Error(), disc.ErrKeyNotFound.Error()))
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
		_, lookupErr := ep.Client.Entry(ctx, c.pk)
		if lookupErr == nil {
			anyOK = true
			continue
		}
		// Only a genuine "entry not found" means we should register a fresh
		// entry at sequence 0. Any other lookup error is transient (EOF /
		// timeout / dmsg session not ready during the startup burst — the
		// same race that produces the "Failed to dial server for IP" EOF):
		// the entry almost certainly EXISTS at some sequence N we just
		// couldn't read, so POSTing a sequence-0 entry would be rejected
		// "sequence field of new entry is not sequence of old entry + 1"
		// (422). Surface the transient error so the caller retries the
		// initial post once the fetch works, instead of clobbering.
		if !isEntryNotFound(lookupErr) {
			if firstErr == nil {
				firstErr = lookupErr
			}
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

	// Snapshot the connected-server PK set under sessionsMx and release
	// before any IO. Holding sessionsMx across ep.Client.Entry() / PutEntry()
	// self-deadlocks now that the disc client (dmsgfirst) dials over DMSG —
	// HTTPTransport.RoundTrip → DialStream → sortedDelegatedSessions →
	// session() also wants sessionsMx, on this same goroutine.
	c.sessionsMx.Lock()
	srvPKs := make([]cipher.PubKey, 0, len(c.sessions))
	for pk := range c.sessions {
		srvPKs = append(srvPKs, pk)
	}
	c.sessionsMx.Unlock()

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
	// Re-check shutdown immediately before the publish IO. The work above
	// (session snapshot + endpoint resolution) can take time, and Close() may
	// have fired since the top-of-function check. Without this a PutEntry can
	// land AFTER Close()'s delEntry, leaving a zombie "advertised but
	// unreachable" client entry until its TTL — the same pathology fixed on the
	// server side (#3145). Narrows the window sharply; a full join is deferred
	// because the client wg machinery intentionally avoids wg.Add on these
	// background loops to dodge an Add-after-Wait race (see Client.closed and
	// the comment at client.go:145-148).
	if isClosed(done) {
		return nil
	}
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
		// Only register a fresh sequence-0 entry when the entry is
		// genuinely absent. A transient lookup failure (EOF/timeout) does
		// NOT mean the entry is gone — POSTing sequence 0 over an existing
		// entry is rejected 422 "not old + 1". Return the error and let the
		// update loop retry on the next tick.
		if !isEntryNotFound(err) {
			return err
		}
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

// entryUpdateAttemptTimeout bounds a single updateClientEntry call so a
// stuck PUT (e.g. dmsg-HTTP stream-dial that the underlying transport never
// errors out of) doesn't hang the loop forever. Without this, a service
// whose first PUT-via-dmsg-HTTP raced its dmsg-disc peer's outbound-session
// establishment could stall here permanently — observed on a fresh dmsg-
// only deployment service post-autopull: nothing returns, nothing retries,
// the entry never registers.
const entryUpdateAttemptTimeout = 30 * time.Second

// entryUpdateMinBackoff / entryUpdateMaxBackoff bracket the exponential
// backoff applied when updateClientEntry returns a non-nil error. The loop
// otherwise rearms its timer at c.updateInterval (default 1 min, often 5
// in service configs) which would leave dmsg-only services unregistered
// for minutes after a transient publish failure.
const (
	entryUpdateMinBackoff = time.Second
	entryUpdateMaxBackoff = 30 * time.Second
)

// entryFailureBackoff returns the delay before the next attempt after
// `consecutiveFailures` consecutive failed updates. Exponential, clamped
// to entryUpdateMaxBackoff.
func entryFailureBackoff(consecutiveFailures int) time.Duration {
	if consecutiveFailures <= 0 {
		return entryUpdateMinBackoff
	}
	d := entryUpdateMinBackoff << (consecutiveFailures - 1) //nolint:gosec
	if d <= 0 || d > entryUpdateMaxBackoff {
		return entryUpdateMaxBackoff
	}
	return d
}

func (c *EntityCommon) updateClientEntryLoop(ctx context.Context, done chan struct{}, clientType string) {
	t := time.NewTimer(c.updateInterval)
	defer t.Stop()

	// runUpdate calls updateClientEntry with a per-attempt context timeout
	// and returns whether the attempt succeeded. The timeout bounds a stuck
	// underlying HTTP/dmsg-HTTP call without affecting the outer ctx the
	// service runs under.
	runUpdate := func(label string) bool {
		callCtx, cancel := context.WithTimeout(ctx, entryUpdateAttemptTimeout)
		defer cancel()
		if err := c.updateClientEntry(callCtx, done, clientType); err != nil {
			c.log.WithError(err).Warnf("Failed to update discovery entry (%s).", label)
			return false
		}
		return true
	}

	// rearm rearms the timer at either c.updateInterval on success or a
	// short exponential backoff on failure, so a transient publish failure
	// (e.g. dmsg-HTTP cold-start race) doesn't leave the service silently
	// unregistered for minutes.
	consecutiveFailures := 0
	rearm := func(ok bool) {
		if ok {
			consecutiveFailures = 0
			t.Reset(c.updateInterval)
			return
		}
		consecutiveFailures++
		t.Reset(entryFailureBackoff(consecutiveFailures))
	}

	for {
		select {
		case <-ctx.Done():
			return

		case <-t.C:
			if lastUpdate, due := c.updateIsDue(); !due {
				t.Reset(c.updateInterval - time.Since(lastUpdate))
				continue
			}

			// updateClientEntry takes sessionsMx itself for its snapshot;
			// holding it here would self-deadlock via the dmsgfirst path.
			rearm(runUpdate("tick"))

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
			// See timer-tick comment above re: sessionsMx.
			rearm(runUpdate("nudge"))
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
