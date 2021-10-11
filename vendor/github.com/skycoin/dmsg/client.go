package dmsg

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

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
	MinSessions    int
	UpdateInterval time.Duration // Duration between discovery entry updates.
	Callbacks      *ClientCallbacks
}

// Ensure ensures all config values are set.
func (c *Config) Ensure() {
	if c.MinSessions == 0 {
		c.MinSessions = DefaultMinSessions
	}
	if c.UpdateInterval == 0 {
		c.UpdateInterval = DefaultUpdateInterval
	}
	if c.Callbacks == nil {
		c.Callbacks = new(ClientCallbacks)
	}
	c.Callbacks.ensure()
}

// DefaultConfig returns the default configuration for a dmsg client entity.
func DefaultConfig() *Config {
	conf := &Config{
		MinSessions:    DefaultMinSessions,
		UpdateInterval: DefaultUpdateInterval,
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

	errCh chan error
	done  chan struct{}
	once  sync.Once
	sesMx sync.Mutex
}

// NewClient creates a dmsg client entity.
func NewClient(pk cipher.PubKey, sk cipher.SecKey, dc disc.APIClient, conf *Config) *Client {
	c := new(Client)
	c.ready = make(chan struct{})
	c.porter = netutil.NewPorter(netutil.PorterMinEphemeral)
	c.errCh = make(chan error, 10)
	c.done = make(chan struct{})

	log := logging.MustGetLogger("dmsg_client")

	// Init config.
	if conf == nil {
		conf = DefaultConfig()
	}
	conf.Ensure()
	c.conf = conf

	// Init common fields.
	c.EntityCommon.init(pk, sk, dc, log, conf.UpdateInterval)

	// Init callback: on set session.
	c.EntityCommon.setSessionCallback = func(ctx context.Context) error {
		if err := c.EntityCommon.updateClientEntry(ctx, c.done); err != nil {
			return err
		}

		// Client is 'ready' once we have successfully updated the discovery entry
		// with at least one delegated server.
		c.readyOnce.Do(func() { close(c.ready) })
		return nil
	}

	// Init callback: on delete session.
	c.EntityCommon.delSessionCallback = func(ctx context.Context) error {
		err := c.EntityCommon.updateClientEntry(ctx, c.done)
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
		ce.log.Info("Stopped serving client!")
	}()

	cancellabelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func(ctx context.Context) {
		select {
		case <-ctx.Done():
		case <-ce.done:
			cancel()
		}
	}(cancellabelCtx)

	for {
		if isClosed(ce.done) {
			return
		}

		ce.log.Info("Discovering dmsg servers...")
		entries, err := ce.discoverServers(cancellabelCtx)
		if err != nil {
			ce.log.WithError(err).Warn("Failed to discover dmsg servers.")
			if err == context.Canceled || err == context.DeadlineExceeded {
				return
			}
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

			if err := ce.ensureSession(cancellabelCtx, entry); err != nil {
				ce.log.WithField("remote_pk", entry.Static).WithError(err).Warn("Failed to establish session.")
				if err == context.Canceled || err == context.DeadlineExceeded {
					return
				}
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
				Info("Session closed.")
		}
		ce.sessions = make(map[cipher.PubKey]*SessionCommon)
		ce.log.Info("All sessions closed.")
		ce.sessionsMx.Unlock()
		ce.porter.CloseAll(ce.log)
		err = ce.EntityCommon.delClientEntry(context.Background())
	})
	return err
}

// Listen listens on a given dmsg port.
func (ce *Client) Listen(port uint16) (*Listener, error) {
	lis := newListener(ce.porter, Addr{PK: ce.pk, Port: port})
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

// AllStreams returns all the streams of the current client.
func (ce *Client) AllStreams() (out []*Stream) {
	fn := func(port uint16, pv netutil.PorterValue) (next bool) {
		if str, ok := pv.Value.(*Stream); ok {
			out = append(out, str)
			return true
		}

		for _, v := range pv.Children {
			if str, ok := v.(*Stream); ok {
				out = append(out, str)
			}
		}
		return true
	}

	ce.porter.RangePortValuesAndChildren(fn)
	return out
}

// ConnectionsSummary associates connected clients, and the servers that connect such clients.
// Key: Client PK, Value: Slice of Server PKs
type ConnectionsSummary map[cipher.PubKey][]cipher.PubKey

// ConnectionsSummary returns a summary of all connected clients, and the associated servers that connect them.
func (ce *Client) ConnectionsSummary() ConnectionsSummary {
	streams := ce.AllStreams()
	out := make(ConnectionsSummary, len(streams))

	for _, s := range streams {
		cPK := s.RawRemoteAddr().PK
		sPK := s.ServerPK()

		sPKs, ok := out[cPK]
		if ok && hasPK(sPKs, sPK) {
			continue
		}
		out[cPK] = append(sPKs, sPK)
	}

	return out
}

func hasPK(pks []cipher.PubKey, pk cipher.PubKey) bool {
	for _, oldPK := range pks {
		if oldPK == pk {
			return true
		}
	}
	return false
}
