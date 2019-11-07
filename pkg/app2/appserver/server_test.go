package appserver_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app2"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app2/appcommon"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app2/appnet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app2/appserver"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

func TestServer_ListenAndServe(t *testing.T) {
	l := logging.MustGetLogger("app_server")
	sockFile := "app.sock"
	appKey := appcommon.GenerateAppKey()

	s, err := appserver.New(l, sockFile, appKey)
	require.NoError(t, err)

	visorPK, _ := cipher.GenerateKeyPair()
	clientConfig := app2.ClientConfig{
		VisorPK:  visorPK,
		SockFile: sockFile,
		AppKey:   appKey,
	}

	errCh := make(chan error)
	go func() {
		err := s.ListenAndServe()
		if err != nil {
			fmt.Printf("ListenAndServe error: %v\n", err)
		}
		errCh <- err
	}()

	time.Sleep(500 * time.Millisecond)

	dmsgLocal, dmsgRemote, remote := prepAddrs()

	var noErr error

	conn := &appcommon.MockConn{}
	conn.On("LocalAddr").Return(dmsgLocal)
	conn.On("RemoteAddr").Return(dmsgRemote)
	conn.On("Close").Return(noErr)

	appnet.ClearNetworkers()
	n := &appnet.MockNetworker{}
	n.On("DialContext", mock.Anything, remote).Return(conn, noErr)

	err = appnet.AddNetworker(appnet.TypeDMSG, n)
	require.NoError(t, err)

	cl, err := app2.NewClient(logging.MustGetLogger("app_client"), clientConfig)
	require.NoError(t, err)

	gotConn, err := cl.Dial(remote)
	require.NoError(t, err)
	require.NotNil(t, gotConn)
	require.Equal(t, remote, gotConn.RemoteAddr())

	err = s.Close()
	require.NoError(t, err)

	err = <-errCh
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "use of closed network connection"))
}

func prepAddrs() (dmsgLocal, dmsgRemote dmsg.Addr, remote appnet.Addr) {
	localPK, _ := cipher.GenerateKeyPair()
	localPort := uint16(10)
	dmsgLocal = dmsg.Addr{
		PK:   localPK,
		Port: localPort,
	}

	remotePK, _ := cipher.GenerateKeyPair()
	remotePort := uint16(11)
	dmsgRemote = dmsg.Addr{
		PK:   remotePK,
		Port: remotePort,
	}
	remote = appnet.Addr{
		Net:    appnet.TypeDMSG,
		PubKey: remotePK,
		Port:   routing.Port(remotePort),
	}

	return
}
