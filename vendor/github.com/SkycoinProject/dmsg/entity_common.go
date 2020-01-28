package dmsg

import (
	"context"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/disc"
	"github.com/SkycoinProject/dmsg/netutil"
)

// EntityCommon contains the common fields and methods for server and client entities.
type EntityCommon struct {
	pk cipher.PubKey
	sk cipher.SecKey
	dc disc.APIClient

	sessions   map[cipher.PubKey]*SessionCommon
	sessionsMx *sync.Mutex

	log logrus.FieldLogger

	setSessionCallback func(ctx context.Context) error
	delSessionCallback func(ctx context.Context) error
}

func (c *EntityCommon) init(pk cipher.PubKey, sk cipher.SecKey, dc disc.APIClient, log logrus.FieldLogger) {
	c.pk = pk
	c.sk = sk
	c.dc = dc
	c.sessions = make(map[cipher.PubKey]*SessionCommon)
	c.sessionsMx = new(sync.Mutex)
	c.log = log
}

// LocalPK returns the local public key of the entity.
func (c *EntityCommon) LocalPK() cipher.PubKey { return c.pk }

// SetLogger sets the internal logger.
// This should be called before we serve.
func (c *EntityCommon) SetLogger(log logrus.FieldLogger) { c.log = log }

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

	if _, ok := c.sessions[dSes.RemotePK()]; ok {
		c.sessionsMx.Unlock()
		return false
	}

	c.sessions[dSes.RemotePK()] = dSes
	if c.setSessionCallback != nil {
		if err := c.setSessionCallback(ctx); err != nil {
			c.log.
				WithError(err).
				Warn("setSession() callback returned non-nil error.")
		}
	}
	c.sessionsMx.Unlock()
	return true
}

func (c *EntityCommon) delSession(ctx context.Context, pk cipher.PubKey) {
	c.sessionsMx.Lock()
	delete(c.sessions, pk)
	if c.delSessionCallback != nil {
		if err := c.delSessionCallback(ctx); err != nil {
			c.log.
				WithError(err).
				Warn("delSession() callback returned non-nil error.")
		}
	}
	c.sessionsMx.Unlock()
}

// updateServerEntry updates the dmsg server's entry within dmsg discovery.
func (c *EntityCommon) updateServerEntry(ctx context.Context, addr string) error {
	entry, err := c.dc.Entry(ctx, c.pk)
	if err != nil {
		entry = disc.NewServerEntry(c.pk, 0, addr, 10)
		if err := entry.Sign(c.sk); err != nil {
			return err
		}
		return c.dc.SetEntry(ctx, entry)
	}
	entry.Server.Address = addr
	return c.dc.UpdateEntry(ctx, c.sk, entry)
}

func (c *EntityCommon) updateClientEntry(ctx context.Context, done chan struct{}) error {
	if isClosed(done) {
		return nil
	}

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
		return c.dc.SetEntry(ctx, entry)
	}
	entry.Client.DelegatedServers = srvPKs
	c.log.WithField("entry", entry).Info("Updating entry.")
	return c.dc.UpdateEntry(ctx, c.sk, entry)
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
