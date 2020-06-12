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
	bPK, bSK := cipher.GenerateKeyPair()

	aConn, bConn := net.Pipe()

	ihs := InitiatorHandshake(aSK, dmsg.Addr{PK: aPK, Port: 1}, dmsg.Addr{PK: bPK, Port: 1})

	rhs := ResponderHandshake(func(f2 Frame2) error {
		return nil
	})

	var b *Conn
	var respErr error
	done := make(chan struct{})

	go func() {
		bConnConfig := connConfig{
			conn:      bConn,
			localPK:   bPK,
			localSK:   bSK,
			deadline:  time.Now().Add(HandshakeTimeout),
			hs:        rhs,
			freePort:  nil,
			encrypt:   false,
			initiator: false,
		}

		b, respErr = newConn(bConnConfig)

		close(done)
	}()

	aConnConfig := connConfig{
		conn:      aConn,
		localPK:   aPK,
		localSK:   aSK,
		deadline:  time.Now().Add(HandshakeTimeout),
		hs:        ihs,
		freePort:  nil,
		encrypt:   false,
		initiator: true,
	}

	a, err := newConn(aConnConfig)
	require.NoError(t, err)

	<-done
	require.NoError(t, respErr)

	closeFunc := func() {
		require.NoError(t, a.Close())
		require.NoError(t, b.Close())
	}

	return a, b, closeFunc
}
