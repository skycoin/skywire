package dmsg

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/disc"
	"github.com/SkycoinProject/dmsg/noise"
)

var log = logging.MustGetLogger("dmsg")

const (
	clientReconnectInterval = 3 * time.Second
)

var (
	// ErrNoSrv indicate that remote client does not have DelegatedServers in entry.
	ErrNoSrv = errors.New("remote has no DelegatedServers")
	// ErrClientClosed indicates that client is closed and not accepting new connections.
	ErrClientClosed = errors.New("client closed")
	// ErrClientAcceptMaxed indicates that the client cannot take in more accepts.
	ErrClientAcceptMaxed = errors.New("client accepts buffer maxed")
)

// ClientOption represents an optional argument for Client.
type ClientOption func(c *Client) error

// SetLogger sets the internal logger for Client.
func SetLogger(log *logging.Logger) ClientOption {
	return func(c *Client) error {
		if log == nil {
			return errors.New("nil logger set")
		}
		c.log = log
		return nil
	}
}

// Client implements stream.Factory
type Client struct {
	log *logging.Logger

	pk cipher.PubKey
	sk cipher.SecKey
	dc disc.APIClient

	conns map[cipher.PubKey]*ClientConn // conns with messaging servers. Key: pk of server
	mx    sync.RWMutex

	pm *PortManager

	done chan struct{}
	once sync.Once
}

// NewClient creates a new Client.
func NewClient(pk cipher.PubKey, sk cipher.SecKey, dc disc.APIClient, opts ...ClientOption) *Client {
	c := &Client{
		log:   logging.MustGetLogger("dmsg_client"),
		pk:    pk,
		sk:    sk,
		dc:    dc,
		conns: make(map[cipher.PubKey]*ClientConn),
		pm:    newPortManager(pk),
		done:  make(chan struct{}),
	}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			panic(err)
		}
	}
	return c
}

func (c *Client) updateDiscEntry(ctx context.Context) error {
	srvPKs := make([]cipher.PubKey, 0, len(c.conns))
	for pk := range c.conns {
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
	c.log.Infoln("updatingEntry:", entry)
	return c.dc.UpdateEntry(ctx, c.sk, entry)
}

func (c *Client) setConn(ctx context.Context, conn *ClientConn) {
	c.mx.Lock()
	c.conns[conn.srvPK] = conn
	if err := c.updateDiscEntry(ctx); err != nil {
		c.log.WithError(err).Warn("updateEntry: failed")
	}
	c.mx.Unlock()
}

func (c *Client) delConn(ctx context.Context, pk cipher.PubKey) {
	c.mx.Lock()
	delete(c.conns, pk)
	if err := c.updateDiscEntry(ctx); err != nil {
		c.log.WithError(err).Warn("updateEntry: failed")
	}
	c.mx.Unlock()
}

func (c *Client) getConn(pk cipher.PubKey) (*ClientConn, bool) {
	c.mx.RLock()
	l, ok := c.conns[pk]
	c.mx.RUnlock()
	return l, ok
}

func (c *Client) connCount() int {
	c.mx.RLock()
	n := len(c.conns)
	c.mx.RUnlock()
	return n
}

// InitiateServerConnections initiates connections with dms_servers.
func (c *Client) InitiateServerConnections(ctx context.Context, min int) error {
	if min == 0 {
		return nil
	}
	entries, err := c.findServerEntries(ctx)
	if err != nil {
		return err
	}
	c.log.Info("found dmsg_server entries:", entries)
	if err := c.findOrConnectToServers(ctx, entries, min); err != nil {
		return err
	}
	return nil
}

func (c *Client) findServerEntries(ctx context.Context) ([]*disc.Entry, error) {
	for {
		entries, err := c.dc.AvailableServers(ctx)
		if err != nil || len(entries) == 0 {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("dmsg_servers are not available: %s", err)
			default:
				retry := time.Second
				c.log.WithError(err).Warnf("no dmsg_servers found: trying again in %v...", retry)
				time.Sleep(retry)
				continue
			}
		}
		return entries, nil
	}
}

func (c *Client) findOrConnectToServers(ctx context.Context, entries []*disc.Entry, min int) error {
	for _, entry := range entries {
		_, err := c.findOrConnectToServer(ctx, entry.Static)
		if err != nil {
			c.log.Warnf("findOrConnectToServers: failed to find/connect to server %s: %s", entry.Static, err)
			continue
		}
		c.log.Infof("findOrConnectToServers: found/connected to server %s", entry.Static)
		if c.connCount() >= min {
			return nil
		}
	}
	return fmt.Errorf("findOrConnectToServers: all servers failed")
}

func (c *Client) findOrConnectToServer(ctx context.Context, srvPK cipher.PubKey) (*ClientConn, error) {
	if conn, ok := c.getConn(srvPK); ok {
		return conn, nil
	}

	entry, err := c.dc.Entry(ctx, srvPK)
	if err != nil {
		return nil, err
	}
	if entry.Server == nil {
		return nil, errors.New("entry is of client instead of server")
	}

	tcpConn, err := net.Dial("tcp", entry.Server.Address)
	if err != nil {
		return nil, err
	}
	ns, err := noise.New(noise.HandshakeXK, noise.Config{
		LocalPK:   c.pk,
		LocalSK:   c.sk,
		RemotePK:  srvPK,
		Initiator: true,
	})
	if err != nil {
		return nil, err
	}
	nc, err := noise.WrapConn(tcpConn, ns, StreamHandshakeTimeout)
	if err != nil {
		return nil, err
	}

	conn := NewClientConn(c.log, c.pm, nc, c.pk, srvPK)
	if err := conn.readOK(); err != nil {
		return nil, err
	}

	c.setConn(ctx, conn)

	go func() {
		err := conn.Serve(ctx)
		conn.log.WithField("reason", err).WithField("remoteServer", srvPK).Debug("connection with server closed")
		c.delConn(ctx, srvPK)

		// reconnect logic.
	retryServerConnect:
		select {
		case <-c.done:
		case <-ctx.Done():
		case <-time.After(clientReconnectInterval):
			conn.log.WithField("remoteServer", srvPK).Warn("Reconnecting")
			if _, err := c.findOrConnectToServer(ctx, srvPK); err != nil {
				conn.log.WithError(err).WithField("remoteServer", srvPK).Warn("ReconnectionFailed")
				goto retryServerConnect
			}
			conn.log.WithField("remoteServer", srvPK).Warn("ReconnectionSucceeded")
		}
	}()
	return conn, nil
}

// Listen creates a listener on a given port, adds it to port manager and returns the listener.
func (c *Client) Listen(port uint16) (*Listener, error) {
	l, ok := c.pm.NewListener(port)
	if !ok {
		return nil, errors.New("port is busy")
	}
	return l, nil
}

// Dial dials a stream to remote dms_client.
func (c *Client) Dial(ctx context.Context, remote cipher.PubKey, port uint16) (*Stream, error) {
	entry, err := c.dc.Entry(ctx, remote)
	if err != nil {
		return nil, fmt.Errorf("get entry failure: %s", err)
	}
	if entry.Client == nil {
		return nil, errors.New("entry is of server instead of client")
	}
	if len(entry.Client.DelegatedServers) == 0 {
		return nil, ErrNoSrv
	}
	for _, srvPK := range entry.Client.DelegatedServers {
		conn, err := c.findOrConnectToServer(ctx, srvPK)
		if err != nil {
			c.log.WithError(err).Warn("failed to connect to server")
			continue
		}
		return conn.DialStream(ctx, remote, port)
	}
	return nil, errors.New("failed to find dmsg_servers for given client pk")
}

// Addr returns the local dmsg_client's public key.
func (c *Client) Addr() net.Addr {
	return Addr{
		PK: c.pk,
	}
}

// Type returns the stream type.
func (c *Client) Type() string {
	return Type
}

// Close closes the dmsg_client and associated connections.
// TODO(evaninjin): proper error handling.
func (c *Client) Close() (err error) {
	if c == nil {
		return nil
	}

	c.once.Do(func() {
		close(c.done)

		c.mx.Lock()
		for _, conn := range c.conns {
			if err := conn.Close(); err != nil {
				log.WithField("reason", err).Debug("Connection closed")
			}
		}
		c.conns = make(map[cipher.PubKey]*ClientConn)
		c.mx.Unlock()

		err = c.pm.Close()
	})

	return err
}
