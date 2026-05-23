// Package app pkg/app/client.go
package app

import (
	"errors"
	"io"
	"net"
	"net/rpc"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/idmanager"
	"github.com/skycoin/skywire/pkg/logging"
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

// Dial dials the remote visor using `remote`.
func (c *Client) Dial(remote appnet.Addr) (net.Conn, error) {
	return c.dial(remote, 0, 0, 0, 0, 0, 0)
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
func (c *Client) DialWithOptions(remote appnet.Addr, muxRoutes, minHops, fwdMinHops, revMinHops, fwdMux, revMux int) (net.Conn, error) {
	return c.dial(remote, muxRoutes, minHops, fwdMinHops, revMinHops, fwdMux, revMux)
}

// dial is the common body for Dial + DialWithOptions. When all opts
// are <= 1 it invokes the original rpcC.Dial path (preserving the
// existing wire shape for non-mux callers); otherwise sends the
// DialWithOptions request so the server-side knows to take the
// SkywireNetworker per-call-opts path.
func (c *Client) dial(remote appnet.Addr, muxRoutes, minHops, fwdMinHops, revMinHops, fwdMux, revMux int) (net.Conn, error) {
	var (
		connID    uint16
		localPort routing.Port
		err       error
	)
	if muxRoutes > 1 || minHops > 1 || fwdMinHops > 1 || revMinHops > 1 || fwdMux > 1 || revMux > 1 {
		connID, localPort, err = c.rpcC.DialWithOptions(remote, muxRoutes, minHops, fwdMinHops, revMinHops, fwdMux, revMux)
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
