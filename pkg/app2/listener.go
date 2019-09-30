package app2

import (
	"net"
	"sync"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/skywire/pkg/app2/appnet"
)

// Listener is a listener for app server connections.
// Implements `net.Listener`.
type Listener struct {
	log       *logging.Logger
	id        uint16
	rpc       RPCClient
	addr      appnet.Addr
	cm        *idManager // contains conns associated with their IDs
	freeLis   func()
	freeLisMx sync.RWMutex
}

func (l *Listener) Accept() (net.Conn, error) {
	connID, remote, err := l.rpc.Accept(l.id)
	if err != nil {
		return nil, err
	}

	conn := &Conn{
		id:     connID,
		rpc:    l.rpc,
		local:  l.addr,
		remote: remote,
	}

	free, err := l.cm.add(connID, conn)
	if err != nil {
		if err := conn.Close(); err != nil {
			l.log.WithError(err).Error("error closing listener")
		}

		return nil, err
	}

	conn.freeConnMx.Lock()
	conn.freeConn = free
	conn.freeConnMx.Unlock()

	return conn, nil
}

func (l *Listener) Close() error {
	defer func() {
		l.freeLisMx.RLock()
		defer l.freeLisMx.RUnlock()
		if l.freeLis != nil {
			l.freeLis()
		}

		var conns []net.Conn
		l.cm.doRange(func(_ uint16, v interface{}) bool {
			conn, err := assertConn(v)
			if err != nil {
				l.log.Error(err)
				return true
			}

			conns = append(conns, conn)
			return true
		})

		for _, conn := range conns {
			if err := conn.Close(); err != nil {
				l.log.WithError(err).Error("error closing listener")
			}
		}
	}()

	return l.rpc.CloseListener(l.id)
}

func (l *Listener) Addr() net.Addr {
	return l.addr
}
