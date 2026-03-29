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
		initBO: time.Second * 5,
		bo:     time.Second * 5,
		maxBO:  time.Minute,
		factor: netutil.DefaultFactor,
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
				}
				ce.log.WithField("remote_pk", entry.Static).WithError(err).WithField("current_backoff", ce.bo.String()).
					Warn("Failed to establish session.")
				ce.serveWait()
			} else {
				// Reset backoff on successful session establishment.
				ce.bo = ce.initBO
			}
		}

		// Only start the update entry loop once we have at least one session established.
		updateEntryLoopOnce.Do(func() { go ce.updateClientEntryLoop(cancellabelCtx, ce.done, ce.conf.ClientType) })

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
