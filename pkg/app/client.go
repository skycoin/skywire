package app

import (
	"errors"
	"fmt"
	"net"
	"net/rpc"
	"os"
	"strings"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/idmanager"
	"github.com/skycoin/skywire/pkg/routing"
)

var (
	// ErrVisorPKNotProvided is returned when the visor PK is not provided.
	ErrVisorPKNotProvided = errors.New("visor PK is not provided")
	// ErrVisorPKInvalid is returned when the visor PK is invalid.
	ErrVisorPKInvalid = errors.New("visor PK is invalid")
	// ErrServerAddrNotProvided is returned when app server address is not provided.
	ErrServerAddrNotProvided = errors.New("server address is not provided")
	// ErrAppKeyNotProvided is returned when the app key is not provided.
	ErrAppKeyNotProvided = errors.New("app key is not provided")
)

// ClientConfig is a configuration for `Client`.
type ClientConfig struct {
	VisorPK    cipher.PubKey
	ServerAddr string
	AppKey     appcommon.Key
}

// ClientConfigFromEnv creates client config from the ENV args.
func ClientConfigFromEnv() (ClientConfig, error) {
	appKey := os.Getenv(appcommon.EnvAppKey)
	if appKey == "" {
		return ClientConfig{}, ErrAppKeyNotProvided
	}

	serverAddr := os.Getenv(appcommon.EnvServerAddr)
	if serverAddr == "" {
		return ClientConfig{}, ErrServerAddrNotProvided
	}

	visorPKStr := os.Getenv(appcommon.EnvVisorPK)
	if visorPKStr == "" {
		return ClientConfig{}, ErrVisorPKNotProvided
	}

	var visorPK cipher.PubKey
	if err := visorPK.UnmarshalText([]byte(visorPKStr)); err != nil {
		return ClientConfig{}, ErrVisorPKInvalid
	}

	return ClientConfig{
		VisorPK:    visorPK,
		ServerAddr: serverAddr,
		AppKey:     appcommon.Key(appKey),
	}, nil
}

// Client is used by skywire apps.
type Client struct {
	log     *logging.Logger
	visorPK cipher.PubKey
	rpc     RPCClient
	lm      *idmanager.Manager // contains listeners associated with their IDs
	cm      *idmanager.Manager // contains connections associated with their IDs
}

// NewClient creates a new `Client`. The `Client` needs to be provided with:
// - log: logger instance.
// - config: client configuration.
func NewClient(log *logging.Logger, config ClientConfig) (*Client, error) {
	rpcCl, err := rpc.Dial("tcp", config.ServerAddr)
	if err != nil {
		return nil, fmt.Errorf("error connecting to the app server: %v", err)
	}

	return &Client{
		log:     log,
		visorPK: config.VisorPK,
		rpc:     NewRPCClient(rpcCl, config.AppKey),
		lm:      idmanager.New(),
		cm:      idmanager.New(),
	}, nil
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
			PubKey: c.visorPK,
			Port:   localPort,
		},
		remote: remote,
	}

	conn.freeConnMx.Lock()

	free, err := c.cm.Add(connID, conn)

	if err != nil {
		conn.freeConnMx.Unlock()

		if err := conn.Close(); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			c.log.WithError(err).Error("Unexpected error while closing conn.")
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
		PubKey: c.visorPK,
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
			c.log.WithError(err).Error("error closing listener")
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
