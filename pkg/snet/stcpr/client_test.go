package stcpr

import (
	"net"
	"testing"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/directtransport"
)

func TestConn(t *testing.T) {
	mp := func() (c1, c2 net.Conn, stop func(), err error) {
		c1, c2, stop = prepareConns(t)
		return
	}
	nettest.TestConn(t, mp)
}

func prepareConns(t *testing.T) (*directtransport.Conn, *directtransport.Conn, func()) {
	aPK, aSK := cipher.GenerateKeyPair()
	bPK, bSK := cipher.GenerateKeyPair()

	aConn, bConn := net.Pipe()

	ihs := directtransport.InitiatorHandshake(aSK, dmsg.Addr{PK: aPK, Port: 1}, dmsg.Addr{PK: bPK, Port: 1})

	rhs := directtransport.ResponderHandshake(func(f2 directtransport.Frame2) error {
		return nil
	})

	var b *directtransport.Conn
	var respErr error
	done := make(chan struct{})

	go func() {
		bConnConfig := directtransport.ConnConfig{
			Conn:      bConn,
			LocalPK:   bPK,
			LocalSK:   bSK,
			Deadline:  time.Now().Add(directtransport.HandshakeTimeout),
			Handshake: rhs,
			FreePort:  nil,
			Encrypt:   false,
			Initiator: false,
		}

		b, respErr = directtransport.NewConn(bConnConfig)

		close(done)
	}()

	aConnConfig := directtransport.ConnConfig{
		Conn:      aConn,
		LocalPK:   aPK,
		LocalSK:   aSK,
		Deadline:  time.Now().Add(directtransport.HandshakeTimeout),
		Handshake: ihs,
		FreePort:  nil,
		Encrypt:   false,
		Initiator: true,
	}

	a, err := directtransport.NewConn(aConnConfig)
	require.NoError(t, err)

	<-done
	require.NoError(t, respErr)

	closeFunc := func() {
		require.NoError(t, a.Close())
		require.NoError(t, b.Close())
	}

	return a, b, closeFunc
}
