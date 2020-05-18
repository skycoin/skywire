package app

import (
	"fmt"
	"net"
	"net/rpc"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/idmanager"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

// Client is used by skywire apps.
type Client struct {
	log  logrus.FieldLogger
	conf appcommon.ProcConfig
	rpc  RPCClient
	lm   *idmanager.Manager // contains listeners associated with their IDs
	cm   *idmanager.Manager // contains connections associated with their IDs
}

// NewClient creates a new Client, panicking on any error.
func NewClient() *Client {
	log := logrus.New()

	conf, err := appcommon.ProcConfigFromEnv()
	if err != nil {
		log.WithError(err).Fatal("Failed to obtain proc config.")
	}
	client, err := NewClientFromConfig(log, conf)
	if err != nil {
		log.WithError(err).Panic("Failed to create app client.")
	}
	return client
}

// NewClientFromConfig creates a new client from a given proc config.
func NewClientFromConfig(log logrus.FieldLogger, conf appcommon.ProcConfig) (*Client, error) {
	conn, err := net.Dial("tcp", conf.AppSrvAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial to app server: %w", err)
	}
	if _, err := conn.Write(conf.ProcKey[:]); err != nil {
		return nil, fmt.Errorf("failed to send proc key back to app server: %w", err)
	}

	return &Client{
		log:  log,
		conf: conf,
		rpc:  NewRPCClient(rpc.NewClient(conn), conf.ProcKey),
		lm:   idmanager.New(),
		cm:   idmanager.New(),
	}, nil
}

// Config returns the underlying proc config.
func (c *Client) Config() appcommon.ProcConfig {
	return c.conf
}

// Dial dials the remote visor using `remote`.
func (c *Client) Dial(remote appnet.Addr) (net.Conn, error) {
	connID, localPort, err := c.rpc.Dial(remote)
	if err != nil {
		return nil, err
	}

	conn := &Conn{
		id:  connID,
		rpc: c.rpc,
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

		if err := conn.Close(); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
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

	lisID, err := c.rpc.Listen(local)
	if err != nil {
		return nil, err
	}

	listener := &Listener{
		log:  c.log,
		id:   lisID,
		rpc:  c.rpc,
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
	var listeners []net.Listener

	c.lm.DoRange(func(_ uint16, v interface{}) bool {
		lis, err := idmanager.AssertListener(v)
		if err != nil {
			c.log.Error(err)
			return true
		}

		listeners = append(listeners, lis)
		return true
	})

	var conns []net.Conn

	c.cm.DoRange(func(_ uint16, v interface{}) bool {
		conn, err := idmanager.AssertConn(v)
		if err != nil {
			c.log.Error(err)
			return true
		}

		conns = append(conns, conn)
		return true
	})

	for _, lis := range listeners {
		if err := lis.Close(); err != nil {
			c.log.WithError(err).Error("Error closing listener.")
		}
	}

	for _, conn := range conns {
		if err := conn.Close(); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			c.log.WithError(err).Error("Unexpected error while closing conn.")
		}
	}
}
