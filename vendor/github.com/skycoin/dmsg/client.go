package dmsg

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/disc"
	"github.com/skycoin/dmsg/netutil"
)

// TODO(evanlinjin): We should implement exponential backoff at some point.
const serveWait = time.Second

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
		sc.OnSessionDial = func(network, addr string) (err error) { return nil }
	}
	if sc.OnSessionDisconnect == nil {
		sc.OnSessionDisconnect = func(network, addr string, err error) {}
	}
}

// Config configures a dmsg client entity.
type Config struct {
	MinSessions int
	Callbacks   *ClientCallbacks
}

// PrintWarnings prints warnings with config.
func (c Config) PrintWarnings(log logrus.FieldLogger) {
	log = log.WithField("location", "dmsg.Config")
	if c.MinSessions < 1 {
		log.Warn("Field 'MinSessions' has value < 1 : This will disallow establishment of dmsg streams.")
	}
}

// DefaultConfig returns the default configuration for a dmsg client entity.
func DefaultConfig() *Config {
	return &Config{
		MinSessions: DefaultMinSessions,
	}
}

// Client represents a dmsg client entity.
type Client struct {
	ready     chan struct{}
	readyOnce sync.Once

	EntityCommon
	conf   *Config
	porter *netutil.Porter

	errCh chan error
	done  chan struct{}
	once  sync.Once
	sesMx sync.Mutex
}

// NewClient creates a dmsg client entity.
func NewClient(pk cipher.PubKey, sk cipher.SecKey, dc disc.APIClient, conf *Config) *Client {
	c := new(Client)
	c.ready = make(chan struct{})

	// Init common fields.
	c.EntityCommon.init(pk, sk, dc, logging.MustGetLogger("dmsg_client"))
	c.EntityCommon.setSessionCallback = func(ctx context.Context, sessionCount int) error {
		err := c.EntityCommon.updateClientEntry(ctx, c.done)
		if err == nil {
			// Client is 'ready' once we have successfully updated the discovery entry
			// with at least one delegated server.
			c.readyOnce.Do(func() { close(c.ready) })
		}
		return err
	}
	c.EntityCommon.delSessionCallback = func(ctx context.Context, sessionCount int) error {
		return c.EntityCommon.updateClientEntry(ctx, c.done)
	}

	// Init config.
	if conf == nil {
		conf = DefaultConfig()
	}
	if conf.Callbacks == nil {
		conf.Callbacks = new(ClientCallbacks)
	}
	conf.Callbacks.ensure()
	c.conf = conf
	c.conf.PrintWarnings(c.log)

	c.porter = netutil.NewPorter(netutil.PorterMinEphemeral)
	c.errCh = make(chan error, 10)
	c.done = make(chan struct{})

	return c
}

// Serve serves the client.
// It blocks until the client is closed.
func (ce *Client) Serve() {
	defer func() {
		ce.log.Info("Stopped serving client!")
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func(ctx context.Context) {
		select {
		case <-ctx.Done():
		case <-ce.done:
			cancel()
		}
	}(ctx)

	for {
		if isClosed(ce.done) {
			return
		}

		ce.log.Info("Discovering dmsg servers...")
		entries, err := ce.discoverServers(ctx)
		if err != nil {
			ce.log.WithError(err).Warn("Failed to discover dmsg servers.")
			time.Sleep(time.Second) // TODO(evanlinjin): Implement exponential back off.
			continue
		}
		if len(entries) == 0 {
			ce.log.Warnf("No entries found. Retrying after %s...", serveWait.String())
			time.Sleep(serveWait)
		}

		for _, entry := range entries {
			if isClosed(ce.done) {
				return
			}

			// If we have enough sessions, we wait for error or done signal.
			if ce.SessionCount() >= ce.conf.MinSessions {
				select {
				case <-ce.done:
					return
				case err := <-ce.errCh:
					ce.log.WithError(err).Info("Session stopped.")
					if isClosed(ce.done) {
						return
					}
				}
			}

			if err := ce.ensureSession(ctx, entry); err != nil {
				ce.log.WithField("remote_pk", entry.Static).WithError(err).Warn("Failed to establish session.")
				time.Sleep(serveWait)
			}
		}
	}
}

// Ready returns a chan which blocks until the client has at least one delegated server and has an entry in the
// dmsg discovery.
func (ce *Client) Ready() <-chan struct{} {
	return ce.ready
}

func (ce *Client) discoverServers(ctx context.Context) (entries []*disc.Entry, err error) {
	err = netutil.NewDefaultRetrier(ce.log.WithField("func", "discoverServers")).Do(ctx, func() error {
		entries, err = ce.dc.AvailableServers(ctx)
		return err
	})
	return entries, err
}

// Close closes the dmsg client entity.
// TODO(evanlinjin): Have waitgroup.
func (ce *Client) Close() error {
	if ce == nil {
		return nil
	}

	ce.once.Do(func() {
		close(ce.done)

		ce.sesMx.Lock()
		close(ce.errCh)
		ce.sesMx.Unlock()

		ce.sessionsMx.Lock()
		for _, dSes := range ce.sessions {
			ce.log.
				WithError(dSes.Close()).
				Info("Session closed.")
		}
		ce.sessions = make(map[cipher.PubKey]*SessionCommon)
		ce.log.Info("All sessions closed.")
		ce.sessionsMx.Unlock()

		ce.porter.CloseAll(ce.log)
	})

	return nil
}

// Listen listens on a given dmsg port.
func (ce *Client) Listen(port uint16) (*Listener, error) {
	lis := newListener(Addr{PK: ce.pk, Port: port})
	ok, doneFn := ce.porter.Reserve(port, lis)
	if !ok {
		lis.close()
		return nil, ErrPortOccupied
	}
	lis.addCloseCallback(doneFn)
	return lis, nil
}

// Dial wraps DialStream to output net.Conn instead of *Stream.
func (ce *Client) Dial(ctx context.Context, addr Addr) (net.Conn, error) {
	return ce.DialStream(ctx, addr)
}

// DialStream dials to a remote client entity with the given address.
func (ce *Client) DialStream(ctx context.Context, addr Addr) (*Stream, error) {
	entry, err := getClientEntry(ctx, ce.dc, addr.PK)
	if err != nil {
		return nil, err
	}

	// Range client's delegated servers.
	// See if we are already connected to a delegated server.
	for _, srvPK := range entry.Client.DelegatedServers {
		if dSes, ok := ce.clientSession(ce.porter, srvPK); ok {
			return dSes.DialStream(addr)
		}
	}

	// Range client's delegated servers.
	// Attempt to connect to a delegated server.
	for _, srvPK := range entry.Client.DelegatedServers {
		dSes, err := ce.EnsureAndObtainSession(ctx, srvPK)
		if err != nil {
			continue
		}
		return dSes.DialStream(addr)
	}

	return nil, ErrCannotConnectToDelegated
}

// Session obtains an established session.
func (ce *Client) Session(pk cipher.PubKey) (ClientSession, bool) {
	return ce.clientSession(ce.porter, pk)
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

// EnsureAndObtainSession attempts to obtain a session.
// If the session does not exist, we will attempt to establish one.
// It returns an error if the session does not exist AND cannot be established.
func (ce *Client) EnsureAndObtainSession(ctx context.Context, srvPK cipher.PubKey) (ClientSession, error) {
	ce.sesMx.Lock()
	defer ce.sesMx.Unlock()

	if dSes, ok := ce.clientSession(ce.porter, srvPK); ok {
		return dSes, nil
	}

	srvEntry, err := getServerEntry(ctx, ce.dc, srvPK)
	if err != nil {
		return ClientSession{}, err
	}

	return ce.dialSession(ctx, srvEntry)
}

// ensureSession ensures the existence of a session.
// It returns an error if the session does not exist AND cannot be established.
func (ce *Client) ensureSession(ctx context.Context, entry *disc.Entry) error {
	ce.sesMx.Lock()
	defer ce.sesMx.Unlock()

	// If session with server of pk already exists, skip.
	if _, ok := ce.clientSession(ce.porter, entry.Static); ok {
		return nil
	}

	// Dial session.
	_, err := ce.dialSession(ctx, entry)
	return err
}

// It is expected that the session is created and served before the context cancels, otherwise an error will be returned.
// NOTE: This should not be called directly as it may lead to session duplicates.
// Only `ensureSession` or `EnsureAndObtainSession` should call this function.
func (ce *Client) dialSession(ctx context.Context, entry *disc.Entry) (cs ClientSession, err error) {
	ce.log.WithField("remote_pk", entry.Static).Info("Dialing session...")

	const network = "tcp"

	// Trigger dial callback.
	if err := ce.conf.Callbacks.OnSessionDial(network, entry.Server.Address); err != nil {
		return ClientSession{}, fmt.Errorf("session dial is rejected by callback: %w", err)
	}
	defer func() {
		if err != nil {
			// Trigger disconnect callback when dial fails.
			ce.conf.Callbacks.OnSessionDisconnect(network, entry.Server.Address, err)
		}
	}()

	conn, err := net.Dial(network, entry.Server.Address)
	if err != nil {
		return ClientSession{}, err
	}

	dSes, err := makeClientSession(&ce.EntityCommon, ce.porter, conn, entry.Static)
	if err != nil {
		return ClientSession{}, err
	}

	if !ce.setSession(ctx, dSes.SessionCommon) {
		_ = dSes.Close() //nolint:errcheck
		return ClientSession{}, errors.New("session already exists")
	}

	go func() {
		ce.log.WithField("remote_pk", dSes.RemotePK()).Info("Serving session.")
		err := dSes.serve()
		if !isClosed(ce.done) {
			// We should only report an error when client is not closed.
			// Also, when the client is closed, it will automatically delete all sessions.
			ce.errCh <- fmt.Errorf("failed to serve dialed session to %s: %v", dSes.RemotePK(), err)
			ce.delSession(ctx, dSes.RemotePK())
		}

		// Trigger disconnect callback.
		ce.conf.Callbacks.OnSessionDisconnect(network, entry.Server.Address, err)
	}()

	return dSes, nil
}
