package stcp

import (
	"net"
	"testing"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"
)

func TestConn(t *testing.T) {
	mp := func() (c1, c2 net.Conn, stop func(), err error) {
		c1, c2, stop = prepareConns(t)
		return
	}
	nettest.TestConn(t, mp)
}

func prepareConns(t *testing.T) (*Conn, *Conn, func()) {
	aPK, aSK := cipher.GenerateKeyPair()
	bPK, _ := cipher.GenerateKeyPair()

	aConn, bConn := net.Pipe()

	ihs := InitiatorHandshake(aSK, dmsg.Addr{PK: aPK, Port: 1}, dmsg.Addr{PK: bPK, Port: 1})

	rhs := ResponderHandshake(func(f2 Frame2) error {
		return nil
	})

	var b *Conn
	var respErr error
	done := make(chan struct{})

	go func() {
		b, respErr = newConn(bConn, time.Now().Add(HandshakeTimeout), rhs, nil, true, false)
		close(done)
	}()

	a, err := newConn(aConn, time.Now().Add(HandshakeTimeout), ihs, nil, true, false)
	require.NoError(t, err)

	<-done
	require.NoError(t, respErr)

	closeFunc := func() {
		require.NoError(t, a.Close())
		require.NoError(t, b.Close())
	}

	return a, b, closeFunc
}
