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

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
)

// EntityCommon contains the common fields and methods for server and client entities.
type EntityCommon struct {
	// atomic requires 64-bit alignment for struct field access
	lastUpdate int64 // Timestamp (in unix seconds) of last update.

	pk cipher.PubKey
	sk cipher.SecKey
	dc disc.APIClient

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
	c.sessions = make(map[cipher.PubKey]*SessionCommon)
	c.sessionsMx = new(sync.Mutex)
	c.updateInterval = updateInterval
	c.entryNudge = make(chan struct{}, 1)
	c.log = log
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

// updateServerEntry updates the dmsg server's entry within dmsg discovery.
// If 'addr' is an empty string, the Entry.addr field will not be updated in discovery.
// Caller must hold c.sessionsMx.
func (c *EntityCommon) updateServerEntry(ctx context.Context, addr string, maxSessions int, authPassphrase string) (err error) {
	if addr == "" {
		return errors.New("updateServerEntry cannot accept empty 'addr' input")
	}

	// Record last update on success.
	defer func() {
		if err == nil {
			c.recordUpdate()
		}
	}()

	availableSessions := maxSessions - len(c.sessions)
	if availableSessions < 0 {
		availableSessions = 0
	}

	entry, err := c.dc.Entry(ctx, c.pk)
	if err != nil {
		entry = disc.NewServerEntry(c.pk, 0, addr, availableSessions)
		entry.Server.DHTBootstrap = c.dhtBootstrap
		if err := entry.Sign(c.sk); err != nil {
			return err
		}
		return c.dc.PostEntry(ctx, entry)
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

	return c.dc.PutEntry(ctx, c.sk, entry)
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
	// Record last update on success.
	defer func() {
		if err == nil {
			c.recordUpdate()
		}
	}()

	c.sessionsMx.Lock()
	srvPKs := make([]cipher.PubKey, 0, len(c.sessions))
	for pk := range c.sessions {
		srvPKs = append(srvPKs, pk)
	}
	c.sessionsMx.Unlock()

	_, err = c.dc.Entry(ctx, c.pk)
	if err != nil {
		entry := disc.NewClientEntry(c.pk, 0, srvPKs)
		entry.ClientType = clientType
		entry.Protocol = protocol
		if err := entry.Sign(c.sk); err != nil {
			return err
		}
		return c.dc.PostEntry(ctx, entry)
	}
	return nil
}

func (c *EntityCommon) updateClientEntry(ctx context.Context, done chan struct{}, clientType string) (err error) {
	if isClosed(done) {
		return nil
	}

	srvPKs := make([]cipher.PubKey, 0, len(c.sessions))
	for pk := range c.sessions {
		srvPKs = append(srvPKs, pk)
	}

	// Record last update on success AND cache the set of delegated
	// server PKs we just pushed, so the next call can short-circuit.
	defer func() {
		if err == nil {
			c.recordUpdate()
			c.pushedSrvPKsMx.Lock()
			// Make a defensive copy — the srvPKs slice is a local
			// snapshot but the caller may hold a reference in log
			// output, so copying is cheap and keeps ownership clean.
			pushed := make([]cipher.PubKey, len(srvPKs))
			copy(pushed, srvPKs)
			c.lastPushedSrvPKs = pushed
			c.pushedSrvPKsMx.Unlock()
		}
	}()

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

	entry, err := c.dc.Entry(ctx, c.pk)
	if err != nil {
		entry = disc.NewClientEntry(c.pk, 0, srvPKs)
		entry.ClientType = clientType
		if err := entry.Sign(c.sk); err != nil {
			return err
		}
		return c.dc.PostEntry(ctx, entry)
	}

	// The entry might be a server entry (e.g., debug client running on a dmsg server).
	// In that case, entry.Client is nil and we need to create a new client entry.
	if entry.Client == nil {
		entry = disc.NewClientEntry(c.pk, 0, srvPKs)
		entry.ClientType = clientType
		if err := entry.Sign(c.sk); err != nil {
			return err
		}
		return c.dc.PostEntry(ctx, entry)
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
	return c.dc.PutEntry(ctx, c.sk, entry)
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
	entry, err := c.dc.Entry(ctx, pk)
	if err != nil {
		c.log.WithField("entry", entry).WithError(err).Warn("Entry not found, so return empty as protocol.\n")
		return ""
	}

	c.log.WithField("entry", entry).Debug("Entry's protocol fetch.\n")
	return entry.Protocol
}

func (c *EntityCommon) delEntry(ctx context.Context) (err error) {

	entry, err := c.dc.Entry(ctx, c.pk)
	if err != nil {
		return err
	}

	defer func() {
		if err == nil {
			c.log.Debug("Entry Deleted successfully.")
		}
	}()

	c.log.WithField("entry", entry).Debug("Deleting entry.\n")
	return c.dc.DelEntry(ctx, entry)
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
