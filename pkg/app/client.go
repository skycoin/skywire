// Package app pkg/app/client.go c2-vis-appsvc
package app

import (
	"errors"
	"io"
	"net"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/idmanager"
	rpc "github.com/skycoin/skywire/pkg/gobrpc"
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
	return c.dial(remote, 0, 0, 0, 0, 0, 0, false, false)
}

// DialWithOptions dials remote with per-call dial options.
// Honored options:
//   - muxRoutes: when > 1 and the remote's transport family routes
//     through SkywireNetworker, the router establishes N parallel
//     mux routes.
//   - minHops: when >= 2, the router rejects the direct path and
//     finds routes through that many intermediates.
//   - fwdMinHops / revMinHops: per-direction MinHops overrides for
//     bandwidth-asymmetric path quality (e.g. direct upstream +
//     multi-hop downstream).
//   - fwdMux / revMux: per-direction MuxRoutes overrides for
//     asymmetric route counts. Setting fwdMux=1 + revMux=N yields
//     1 forward leg + N reverse legs — the canonical download-heavy
//     workload shape where only the bulk-receive direction is
//     aggregated.
//
// All <= 1 is semantically equivalent to Dial. Apps requesting these
// dial shapes (e.g. skynet-client with --routes / --min-hops /
// --forward-min-hops / --reverse-min-hops / --forward-mux /
// --reverse-mux flags) call this instead of Dial; non-mux callers
// keep using Dial unchanged.
//   - direct: when true, forces a direct-transport-only dial — the router
//     creates a direct transport to the remote if none exists and dials
//     1-hop over it, bypassing the route-finder (the `--direct` skynet flag).
//   - diversify: when true, opts this dial into visor-side multi-tunnel
//     transport diversification (docs/mux_aggregation_rfc.md step 3). The
//     visor excludes the first-hop transports/intermediates already claimed
//     by this visor's live route groups to the same dst, so a new tunnel to
//     an exit leaves over a DIFFERENT first-hop transport and the tunnels'
//     throughputs sum. Set by skysocks-client for tunnel 2..N; harmless on a
//     lone dial (no sibling route group → no exclusions → identical to Dial).
func (c *Client) DialWithOptions(remote appnet.Addr, muxRoutes, minHops, fwdMinHops, revMinHops, fwdMux, revMux int, direct, diversify bool) (net.Conn, error) {
	return c.dial(remote, muxRoutes, minHops, fwdMinHops, revMinHops, fwdMux, revMux, direct, diversify)
}

// dial is the common body for Dial + DialWithOptions. When all opts
// are <= 1 it invokes the original rpcC.Dial path (preserving the
// existing wire shape for non-mux callers); otherwise sends the
// DialWithOptions request so the server-side knows to take the
// SkywireNetworker per-call-opts path.
func (c *Client) dial(remote appnet.Addr, muxRoutes, minHops, fwdMinHops, revMinHops, fwdMux, revMux int, direct, diversify bool) (net.Conn, error) {
	var (
		connID    uint16
		localPort routing.Port
		err       error
	)
	if direct || diversify || muxRoutes > 1 || minHops > 1 || fwdMinHops > 1 || revMinHops > 1 || fwdMux > 1 || revMux > 1 {
		connID, localPort, err = c.rpcC.DialWithOptions(remote, muxRoutes, minHops, fwdMinHops, revMinHops, fwdMux, revMux, direct, diversify)
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
