package app2

import (
	"net"
	"net/rpc"

	"github.com/skycoin/skywire/pkg/app2/apputil"

	"github.com/pkg/errors"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app2/appnet"
	"github.com/skycoin/skywire/pkg/app2/idmanager"
	"github.com/skycoin/skywire/pkg/routing"
)

// Client is used by skywire apps.
type Client struct {
	log *logging.Logger
	pk  cipher.PubKey
	pid apputil.ProcID
	rpc RPCClient
	lm  *idmanager.Manager // contains listeners associated with their IDs
	cm  *idmanager.Manager // contains connections associated with their IDs
}

// NewClient creates a new `Client`. The `Client` needs to be provided with:
// - log: logger instance
// - localPK: The local public key of the parent skywire visor.
// - pid: The procID assigned for the process that Client is being used by.
// - sockFile: unix socket file to connect to the app server.
// - appKey: application key to authenticate within app server.
func NewClient(log *logging.Logger, localPK cipher.PubKey, pid apputil.ProcID, sockFile, appKey string) (*Client, error) {
	rpcCl, err := rpc.Dial("unix", sockFile)
	if err != nil {
		return nil, errors.Wrap(err, "error connecting to the app server")
	}

	return &Client{
		log: log,
		pk:  localPK,
		pid: pid,
		rpc: NewRPCClient(rpcCl, appKey),
		lm:  idmanager.New(),
		cm:  idmanager.New(),
	}, nil
}

// Dial dials the remote node using `remote`.
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
			PubKey: c.pk,
			Port:   localPort,
		},
		remote: remote,
	}

	conn.freeConnMx.Lock()
	free, err := c.cm.Add(connID, conn)
	if err != nil {
		if err := conn.Close(); err != nil {
			c.log.WithError(err).Error("error closing conn")
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
		PubKey: c.pk,
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
			c.log.WithError(err).Error("error closing listener")
		}
	}

	for _, conn := range conns {
		if err := conn.Close(); err != nil {
			c.log.WithError(err).Error("error closing conn")
		}
	}
}
