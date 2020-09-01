package app

import (
	"errors"
	"net"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/idmanager"
)

// Listener is a listener for app server connections.
// Implements `net.Listener`.
type Listener struct {
	log       logrus.FieldLogger
	id        uint16
	rpc       appserver.RPCIngressClient
	addr      appnet.Addr
	cm        *idmanager.Manager // contains conns associated with their IDs
	freeLis   func() bool
	freeLisMx sync.RWMutex
}

// Accept accepts a connection from listener.
func (l *Listener) Accept() (net.Conn, error) {
	l.log.Infoln("Calling app RPC Accept")

	connID, remote, err := l.rpc.Accept(l.id)
	if err != nil {
		return nil, err
	}

	l.log.Infoln("Accepted conn from app RPC")

	conn := &Conn{
		id:     connID,
		rpc:    l.rpc,
		local:  l.addr,
		remote: remote,
	}

	// lock is needed, since the conn is already added to the manager,
	// but has no `freeConn`. It shouldn't really happen under usual
	// circumstances, but the data race is possible. If we try to close
	// the conn without `freeConn` while the next few lines are running,
	// the panic may raise without this lock
	conn.freeConnMx.Lock()

	free, err := l.cm.Add(connID, conn)
	if err != nil {
		conn.freeConnMx.Unlock()

		if err := conn.Close(); err != nil {
			l.log.WithError(err).Error("error closing listener")
		}

		return nil, err
	}

	conn.freeConn = free
	conn.freeConnMx.Unlock()

	return conn, nil
}

// Close closes listener.
func (l *Listener) Close() error {
	l.freeLisMx.RLock()
	defer l.freeLisMx.RUnlock()

	if l.freeLis != nil {
		if freed := l.freeLis(); !freed {
			return errors.New("listener is already closed")
		}

		var conns []net.Conn

		l.cm.DoRange(func(_ uint16, v interface{}) bool {
			conn, err := idmanager.AssertConn(v)
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

		return l.rpc.CloseListener(l.id)
	}

	return nil
}

// Addr returns address listener listens on.
func (l *Listener) Addr() net.Addr {
	return l.addr
}
