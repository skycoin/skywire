package appserver_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/routing"
)

const (
	sleepDelay = 500 * time.Millisecond
)

func TestServer_ListenAndServe(t *testing.T) {
	l := logging.MustGetLogger("app_server")

	s := appserver.New(l, appcommon.DefaultServerAddr)

	appKey := appcommon.GenerateAppKey()

	require.NoError(t, s.Register(appKey))

	visorPK, _ := cipher.GenerateKeyPair()
	clientConfig := app.ClientConfig{
		VisorPK:    visorPK,
		ServerAddr: appcommon.DefaultServerAddr,
		AppKey:     appKey,
	}

	errCh := make(chan error, 1)

	go func() {
		err := s.ListenAndServe()
		if err != nil {
			fmt.Printf("ListenAndServe error: %v\n", err)
		}
		errCh <- err
	}()

	time.Sleep(sleepDelay)

	dmsgLocal, dmsgRemote, remote := prepAddrs()

	var noErr error

	conn := &appcommon.MockConn{}
	conn.On("LocalAddr").Return(dmsgLocal)
	conn.On("RemoteAddr").Return(dmsgRemote)
	conn.On("Close").Return(noErr)

	appnet.ClearNetworkers()

	n := &appnet.MockNetworker{}

	n.On("DialContext", mock.Anything, remote).Return(conn, noErr)

	require.NoError(t, appnet.AddNetworker(appnet.TypeDmsg, n))

	cl, err := app.NewClient(logging.MustGetLogger("app_client"), clientConfig)
	require.NoError(t, err)

	gotConn, err := cl.Dial(remote)
	require.NoError(t, err)
	require.NotNil(t, gotConn)
	require.Equal(t, remote, gotConn.RemoteAddr())

	require.NoError(t, s.Close())

	err = <-errCh
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "use of closed network connection"))
}

func prepAddrs() (dmsgLocal, dmsgRemote dmsg.Addr, remote appnet.Addr) {
	localPK, _ := cipher.GenerateKeyPair()
	remotePK, _ := cipher.GenerateKeyPair()

	const (
		localPort  uint16 = 10
		remotePort uint16 = 11
	)

	dmsgLocal = dmsg.Addr{
		PK:   localPK,
		Port: localPort,
	}

	dmsgRemote = dmsg.Addr{
		PK:   remotePK,
		Port: remotePort,
	}

	remote = appnet.Addr{
		Net:    appnet.TypeDmsg,
		PubKey: remotePK,
		Port:   routing.Port(remotePort),
	}

	return
}
