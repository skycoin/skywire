// Package app pkg/app/client.go c2-vis-appsvc
package app

import (
	"errors"
	"io"
	"net"
	"os"

	rpc "github.com/0magnet/gobrpc"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/idmanager"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/routing"
)

// Client is used by skywire apps.
type Client struct {
	log     logrus.FieldLogger
	conf    appcommon.ProcConfig
	rpcC    appserver.RPCIngressClient
	lm      *idmanager.Manager // contains listeners associated with their IDs
	cm      *idmanager.Manager // contains connections associated with their IDs
	closers []io.Closer        // additional things to close on close
}

// NewClient creates a new Client, panicking on any error.
func NewClient(eventSubs *appevent.Subscriber) *Client {
	log := logrus.New()
	log.SetOutput(os.Stderr)
	// Use same formatter as visor for consistent log output
	log.SetFormatter(&logging.TextFormatter{
		FullTimestamp:      true,
		AlwaysQuoteStrings: true,
		QuoteEmptyFields:   true,
		ForceFormatting:    true,
		DisableColors:      false,
		ForceColors:        true,
		TimestampFormat:    "2006-01-02T15:04:05.0000Z07:00",
	})

	conf, err := appcommon.ProcConfigFromEnv()
	if err != nil {
		log.WithError(err).Fatal("Failed to obtain proc config.")
	}
	// Add app name to logger for identification (uses _module key for bracket display)
	appLog := log.WithField("_module", conf.AppName)
	client, err := NewClientFromConfig(appLog, conf, eventSubs)
	if err != nil {
		log.WithError(err).Panic("Failed to create app client.")
	}
	return client
}

// NewClientFromConfig creates a new client from a given proc config.
func NewClientFromConfig(log logrus.FieldLogger, conf appcommon.ProcConfig, subs *appevent.Subscriber) (*Client, error) {
	conn, closers, err := appevent.DoReqHandshake(conf, subs)
	if err != nil {
		return nil, err
	}

	return &Client{
		log:     log,
		conf:    conf,
		rpcC:    appserver.NewRPCIngressClient(rpc.NewClient(conn), conf.ProcKey),
		lm:      idmanager.New(),
		cm:      idmanager.New(),
		closers: closers,
	}, nil
}

// Config returns the underlying proc config.
func (c *Client) Config() appcommon.ProcConfig {
	return c.conf
}

// Log returns the client's logger for apps to use for logging.
func (c *Client) Log() logrus.FieldLogger {
	return c.log
}

// SetDetailedStatus sets detailed app status within the visor.
func (c *Client) SetDetailedStatus(status string) error {
	return c.rpcC.SetDetailedStatus(status)
}

// SetOTP publishes the app's current one-time code within the visor, where it
// surfaces on the (auth-gated) hypervisor app list. Apps that gate their own
// web UI use this so an operator can read the code out-of-band instead of the
// app needing a password of its own.
func (c *Client) SetOTP(otp string) error {
	return c.rpcC.SetOTP(otp)
}

// SetConnectionDuration sets the detailed app connection duration within the visor.
func (c *Client) SetConnectionDuration(dur int64) error {
	return c.rpcC.SetConnectionDuration(dur)
}

// SetError sets app error within the visor.
func (c *Client) SetError(appErr string) error {
	return c.rpcC.SetError(appErr)
}

// SetAppPort sets app port within the visor.
func (c *Client) SetAppPort(appPort routing.Port) error {
	return c.rpcC.SetAppPort(appPort)
}

// NoteMuxEvent records a TUNNEL event this app decided — a standby tunnel
// promoted into the active set, an active one parked back into standby, a dead
// one retired — on the route group the app dialed from localPort, and re-labels
// that group's tunnel role ("active" / "standby"; "" leaves it alone).
//
// The app is the only end that knows which of its tunnels carries streams, and
// the router is the only end that knows the route behind one and owns the event
// ring `visor state --select diag` reads. localPort — the port handed back by
// the dial — is the one name both ends share.
func (c *Client) NoteMuxEvent(localPort routing.Port, event, reason, role string) error {
	return c.rpcC.NoteMuxEvent(appserver.NoteMuxEventReq{
		LocalPort: localPort,
		Event:     event,
		Reason:    reason,
		Role:      role,
	})
}

// ProxyStatus fetches the visor-built rich read-only status snapshot for this
// app (per-leg mux telemetry, recent logs, route/transport events). An app that
// serves its own reserved status host (skysocks-client's status.skysocks)
// renders this so the page shows the same rich view as the visor-side resolving
// proxies. An empty snapshot (no error) means the visor had no data; the caller
// then falls back to its own local view.
func (c *Client) ProxyStatus() (proxystatus.Snapshot, error) {
	return c.rpcC.ProxyStatus()
}

// SetStatusOrLog sets the detailed status and logs the error if any.
// Status transitions are best-effort — the app keeps running even if
// the visor can't be notified, so callers don't have to handle the
// error path. A nil receiver is a no-op so apps that can run in
// standalone mode (skychat) don't have to guard each call site.
// Replaces the setAppStatus helper that every app previously defined
// locally.
func (c *Client) SetStatusOrLog(status appserver.AppDetailedStatus) {
	if c == nil {
		return
	}
	if err := c.SetDetailedStatus(string(status)); err != nil {
		c.Log().Errorf("Failed to set status %v: %v", status, err)
	}
}

// SetOTPOrLog publishes the app's one-time code, logging (without the code
// itself) if the visor can't be reached. Best-effort and nil-safe like
// SetStatusOrLog, so apps running standalone don't have to guard call sites.
func (c *Client) SetOTPOrLog(otp string) {
	if c == nil {
		return
	}
	if err := c.SetOTP(otp); err != nil {
		c.Log().Errorf("Failed to publish OTP: %v", err)
	}
}

// Notify publishes a user-facing notification to the visor's notification hub,
// which decides which sink can actually reach the user (an attached UI, a
// subscribed host app such as the Android service, the host-OS notification
// center, or nowhere at all on a headless visor).
//
// The app decides *whether* to notify — it alone knows what is muted or already
// on screen; the visor decides *where*. The visor stamps the App field from the
// calling proc's identity, so callers leave it empty.
func (c *Client) Notify(n appserver.NotifyReq) error {
	return c.rpcC.Notify(n)
}

// NotifyOrLog publishes a notification, logging (without the body — it is
// routinely untrusted peer text) if the visor can't be reached. Best-effort and
// nil-safe like SetStatusOrLog, so apps that can run standalone don't have to
// guard each call site.
func (c *Client) NotifyOrLog(title, body, tag string) {
	if c == nil {
		return
	}
	if err := c.Notify(appserver.NotifyReq{Title: title, Body: body, Tag: tag}); err != nil {
		c.Log().Errorf("Failed to publish notification: %v", err)
	}
}

// SetErrorOrLog records an app error in the visor and logs the
// failure to record (not the original error — that's already in
// appErr). Best-effort and nil-safe like SetStatusOrLog. Replaces
// the setAppError helper that every app previously defined locally.
func (c *Client) SetErrorOrLog(appErr error) {
	if c == nil {
		return
	}
	if err := c.SetError(appErr.Error()); err != nil {
		c.Log().Errorf("Failed to set error %v: %v", appErr, err)
	}
}

// SetAppPortOrLog sets the routing port and logs the error if any.
// Best-effort and nil-safe like SetStatusOrLog. Replaces the
// setAppPort helper that every app previously defined locally.
func (c *Client) SetAppPortOrLog(port routing.Port) {
	if c == nil {
		return
	}
	if err := c.SetAppPort(port); err != nil {
		c.Log().Errorf("Failed to set port %v: %v", port, err)
	}
}

// Dial dials the remote visor using `remote`.
func (c *Client) Dial(remote appnet.Addr) (net.Conn, error) {
	return c.dial(remote, nil)
}

// DialWithOptions dials remote with the per-call dial options opts
// carries — mux route counts (symmetric and per-direction), min-hops
// (symmetric and per-direction), the `--direct` transport-only
// shortcut, multi-tunnel diversification and its disjoint requirement,
// and the tunnel role label. See appserver.DialOptionsReq for what
// each field means to the router.
//
// An opts with nothing explicit set is semantically equivalent to
// Dial. Apps requesting these dial shapes (skynet-client's --routes /
// --min-hops / --forward-mux …, skysocks-client's tunnels) call this
// instead of Dial; non-mux callers keep using Dial unchanged.
//
// opts.Addr is filled in from remote, so a caller never has to state
// the destination twice.
func (c *Client) DialWithOptions(remote appnet.Addr, opts appserver.DialOptionsReq) (net.Conn, error) {
	return c.dial(remote, &opts)
}

// dial is the common body for Dial + DialWithOptions. With no opts (or
// nothing explicit set in them) it invokes the original rpcC.Dial path,
// preserving the existing wire shape for non-mux callers; any explicit
// value, 1 included (the gateway treats MuxRoutes=1 as "form a route
// group"), sends the DialWithOptions request so the server-side knows to
// take the SkywireNetworker per-call-opts path.
func (c *Client) dial(remote appnet.Addr, opts *appserver.DialOptionsReq) (net.Conn, error) {
	var (
		connID    uint16
		localPort routing.Port
		err       error
	)
	if opts != nil && opts.Explicit() {
		opts.Addr = remote
		connID, localPort, err = c.rpcC.DialWithOptions(*opts)
	} else {
		connID, localPort, err = c.rpcC.Dial(remote)
	}
	if err != nil {
		return nil, err
	}
	conn := &Conn{
		id:  connID,
		rpc: c.rpcC,
		local: appnet.Addr{
			Net:    remote.Net,
			PubKey: c.conf.VisorPK,
			Port:   localPort,
		},
		remote: remote,
	}

	conn.freeConnMx.Lock()

	free, err := c.cm.Add(connID, conn)

	if err != nil {
		conn.freeConnMx.Unlock()

		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			c.log.WithError(err).Error("Received unexpected error when closing conn.")
		}

		return nil, err
	}

	conn.freeConn = free

	conn.freeConnMx.Unlock()

	return conn, nil
}

// Listen listens on the specified `port` for the incoming connections.
func (c *Client) Listen(n appnet.Type, port routing.Port) (net.Listener, error) {
	local := appnet.Addr{
		Net:    n,
		PubKey: c.conf.VisorPK,
		Port:   port,
	}

	lisID, err := c.rpcC.Listen(local)
	if err != nil {
		return nil, err
	}

	listener := &Listener{
		log:  c.log,
		id:   lisID,
		rpc:  c.rpcC,
		addr: local,
		cm:   idmanager.New(),
	}

	listener.freeLisMx.Lock()

	freeLis, err := c.lm.Add(lisID, listener)
	if err != nil {
		listener.freeLisMx.Unlock()

		if err := listener.Close(); err != nil {
			c.log.WithError(err).Error("Unexpected error while closing listener.")
		}

		return nil, err
	}

	listener.freeLis = freeLis

	listener.freeLisMx.Unlock()

	return listener, nil
}

// Close closes client/server communication entirely. It closes all open
// listeners and connections.
func (c *Client) Close() {
	var (
		listeners []net.Listener
		conns     []net.Conn
	)

	// Fill listeners and connections.
	c.lm.DoRange(func(_ uint16, v interface{}) bool {
		lis, err := idmanager.AssertListener(v)
		if err != nil {
			c.log.Error(err)
			return true
		}
		listeners = append(listeners, lis)
		return true
	})
	c.cm.DoRange(func(_ uint16, v interface{}) bool {
		conn, err := idmanager.AssertConn(v)
		if err != nil {
			c.log.Error(err)
			return true
		}
		conns = append(conns, conn)
		return true
	})

	// Close everything.
	for _, lis := range listeners {
		if err := lis.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			c.log.WithError(err).Error("Error closing listener.")
		}
	}
	for _, conn := range conns {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			c.log.WithError(err).Error("Error closing conn.")
		}
	}
	for _, v := range c.closers {
		if err := v.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			c.log.WithError(err).Error("Error closing closer.")
		}
	}
}

// AppSettings polls the visor for this app's live tuning knobs, reporting the
// version the app currently has installed so a steady-state poll costs an empty
// round-trip. Returns the full intended set and the version it carries; a knob
// absent from the set is at its compiled default.
//
// This is the only visor->app VALUE channel: the ingress gateway is ingress
// only (the app is the RPC client), so a running app is reconfigured by asking,
// not by being told. See pkg/app/appserver/app_settings.go.
func (c *Client) AppSettings(applied uint64) (map[string]int64, uint64, error) {
	resp, err := c.rpcC.AppSettings(appserver.AppSettingsReq{Applied: applied})
	if err != nil {
		return nil, applied, err
	}
	return resp.Values, resp.Version, nil
}
