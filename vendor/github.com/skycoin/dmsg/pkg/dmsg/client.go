// Package dmsg pkg/dmsg/client.go
package dmsg

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"

	"github.com/skycoin/dmsg/pkg/disc"
)

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

	bo     time.Duration // initial backoff duration
	maxBO  time.Duration // maximum backoff duration
	factor float64       // multiplier for the backoff duration that is applied on every retry

	errCh chan error
	done  chan struct{}
	once  sync.Once
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
		ready:  make(chan struct{}),
		porter: netutil.NewPorter(netutil.PorterMinEphemeral),
		errCh:  make(chan error, 10),
		done:   make(chan struct{}),
		conf:   conf,
		bo:     time.Second * 5,
		maxBO:  time.Minute,
		factor: netutil.DefaultFactor,
	}

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
		ce.log.Debug("Stopped serving client!")
	}()

	cancellabelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	setupNodeTicker := time.NewTicker(1 * time.Minute)

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
				if entry.Static.Hex() == ctx.Value("dmsgServer").(string) {
					entries = entries[ind : ind+1]
				}
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
		}

		for n, entry := range entries {
			if isClosed(ce.done) {
				return
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
						ce.errCh <- err
						ce.sesMx.Unlock()
					}
				}
				ce.log.WithField("remote_pk", entry.Static).WithError(err).WithField("current_backoff", ce.bo.String()).
					Warn("Failed to establish session.")
				ce.serveWait()
			}
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
				Debug("Session closed.")
		}
		ce.sessions = make(map[cipher.PubKey]*SessionCommon)
		ce.log.Debug("All sessions closed.")
		ce.sessionsMx.Unlock()
		ce.porter.CloseAll(ce.log)
		err = ce.EntityCommon.delEntry(context.Background())
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

// EnsureSession ensures the existence of a session.
// It returns an error if the session does not exist AND cannot be established.
func (ce *Client) EnsureSession(ctx context.Context, entry *disc.Entry) error {
	ce.sesMx.Lock()
	defer ce.sesMx.Unlock()

	// If session with server of pk already exists, skip.
	if _, ok := ce.clientSession(ce.porter, entry.Static); ok {
		ce.log.WithField("remote_pk", entry.Static).Debug("Session already exists...")
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
	ce.log.WithField("remote_pk", entry.Static).Debug("Dialing session...")

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
		ce.log.WithField("remote_pk", dSes.RemotePK()).Debug("Serving session.")
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

// AllEntries returns all the entries registered in discovery
func (ce *Client) AllEntries(ctx context.Context) (entries []string, err error) {
	err = netutil.NewDefaultRetrier(ce.log).Do(ctx, func() error {
		entries, err = ce.dc.AllEntries(ctx)
		return err
	})
	return entries, err
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

func (ce *Client) serveWait() {
	bo := ce.bo

	t := time.NewTimer(bo)
	defer t.Stop()

	if newBO := time.Duration(float64(bo) * ce.factor); ce.maxBO == 0 || newBO <= ce.maxBO {
		ce.bo = newBO
		if newBO > ce.maxBO {
			ce.bo = ce.maxBO
		}
	}
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
