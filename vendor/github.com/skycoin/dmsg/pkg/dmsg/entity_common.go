package dmsg

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"

	"github.com/skycoin/dmsg/pkg/disc"
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
	defer c.sessionsMx.Unlock()

	if _, ok := c.sessions[dSes.RemotePK()]; ok {
		return false
	}
	c.sessions[dSes.RemotePK()] = dSes

	if c.setSessionCallback != nil {
		if err := c.setSessionCallback(ctx); err != nil {
			c.log.
				WithField("func", "EntityCommon.setSession").
				WithError(err).
				Warn("Callback returned non-nil error.")
		}
	}
	return true
}

func (c *EntityCommon) delSession(ctx context.Context, pk cipher.PubKey, serverEndSession bool) {
	c.sessionsMx.Lock()
	defer c.sessionsMx.Unlock()
	delete(c.sessions, pk)
	if serverEndSession {
		return
	}
	if c.delSessionCallback != nil {
		if err := c.delSessionCallback(ctx); err != nil {
			c.log.
				WithField("func", "EntityCommon.delSession").
				WithError(err).
				Warn("Callback returned non-nil error.")
		}
	}
}

// updateServerEntry updates the dmsg server's entry within dmsg discovery.
// If 'addr' is an empty string, the Entry.addr field will not be updated in discovery.
func (c *EntityCommon) updateServerEntry(ctx context.Context, addr string, maxSessions int) (err error) {
	if addr == "" {
		panic("updateServerEntry cannot accept empty 'addr' input") // this should never happen
	}

	// Record last update on success.
	defer func() {
		if err == nil {
			c.recordUpdate()
		}
	}()

	availableSessions := maxSessions - len(c.sessions)

	entry, err := c.dc.Entry(ctx, c.pk)
	if err != nil {
		entry = disc.NewServerEntry(c.pk, 0, addr, availableSessions)
		if err := entry.Sign(c.sk); err != nil {
			return err
		}
		return c.dc.PostEntry(ctx, entry)
	}

	if entry.Server == nil {
		return errors.New("entry in discovery is not of a dmsg server")
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
	log.Debug("Updating entry.")

	return c.dc.PutEntry(ctx, c.sk, entry)
}

func (c *EntityCommon) updateServerEntryLoop(ctx context.Context, addr string, maxSessions int) {
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
			err := c.updateServerEntry(ctx, addr, maxSessions)
			c.sessionsMx.Unlock()

			if err != nil {
				c.log.WithError(err).Warn("Failed to update discovery entry.")
			}

			// Ensure we trigger another update within given 'updateInterval'.
			t.Reset(c.updateInterval)
		}
	}
}

func (c *EntityCommon) updateClientEntry(ctx context.Context, done chan struct{}) (err error) {
	if isClosed(done) {
		return nil
	}

	// Record last update on success.
	defer func() {
		if err == nil {
			c.recordUpdate()
		}
	}()

	srvPKs := make([]cipher.PubKey, 0, len(c.sessions))
	for pk := range c.sessions {
		srvPKs = append(srvPKs, pk)
	}

	entry, err := c.dc.Entry(ctx, c.pk)
	if err != nil {
		entry = disc.NewClientEntry(c.pk, 0, srvPKs)
		if err := entry.Sign(c.sk); err != nil {
			return err
		}
		return c.dc.PostEntry(ctx, entry)
	}

	entry.Client.DelegatedServers = srvPKs
	c.log.WithField("entry", entry).Debug("Updating entry.")
	return c.dc.PutEntry(ctx, c.sk, entry)
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

	c.log.WithField("entry", entry).Debug("Deleting entry.")
	return c.dc.DelEntry(ctx, entry)
}

func getServerEntry(ctx context.Context, dc disc.APIClient, srvPK cipher.PubKey) (*disc.Entry, error) {
	entry, err := dc.Entry(ctx, srvPK)
	if err != nil {
		return nil, ErrDiscEntryNotFound
	}
	if entry.Server == nil {
		return nil, ErrDiscEntryIsNotServer
	}
	return entry, nil
}

func getClientEntry(ctx context.Context, dc disc.APIClient, clientPK cipher.PubKey) (*disc.Entry, error) {
	entry, err := dc.Entry(ctx, clientPK)
	if err != nil {
		return nil, ErrDiscEntryNotFound
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

func (c *EntityCommon) recordUpdate() {
	atomic.StoreInt64(&c.lastUpdate, time.Now().UnixNano())
}
