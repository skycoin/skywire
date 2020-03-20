package visor

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/disc"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkycoinProject/skywire-mainnet/internal/testhelpers"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appserver"
	"github.com/SkycoinProject/skywire-mainnet/pkg/router"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/pathutil"
)

var masterLogger *logging.MasterLogger

func TestMain(m *testing.M) {
	masterLogger = logging.NewMasterLogger()
	loggingLevel, ok := os.LookupEnv("TEST_LOGGING_LEVEL")
	if ok {
		lvl, err := logging.LevelFromString(loggingLevel)
		if err != nil {
			log.Fatal(err)
		}
		masterLogger.SetLevel(lvl)
	} else {
		masterLogger.Out = ioutil.Discard
	}

	os.Exit(m.Run())
}

// TODO(nkryuchkov): fix and uncomment
//func TestNewVisor(t *testing.T) {
//	pk, sk := cipher.GenerateKeyPair()
//	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//		require.NoError(t, json.NewEncoder(w).Encode(&httpauth.NextNonceResponse{Edge: pk, NextNonce: 1}))
//	}))
//	defer srv.Close()
//
//	conf := Config{LocalPath: "local", AppsPath: "apps"}
//	conf.Visor.StaticPubKey = pk
//	conf.Visor.StaticSecKey = sk
//	conf.Dmsg.Discovery = "http://skywire.skycoin.com:8001"
//	conf.Dmsg.ServerCount = 10
//	conf.Transport.Discovery = srv.URL
//	conf.Apps = []AppConfig{
//		{App: "foo", Port: 1},
//		{App: "bar", AutoStart: true, Port: 2},
//	}
//
//	defer func() {
//		require.NoError(t, os.RemoveAll("local"))
//	}()
//
//	visor, err := NewVisor(&conf, masterLogger)
//	require.NoError(t, err)
//
//	assert.NotNil(t, visor.router)
//	assert.NotNil(t, visor.appsConf)
//	assert.NotNil(t, visor.appsPath)
//	assert.NotNil(t, visor.localPath)
//	assert.NotNil(t, visor.startedApps)
//}

func TestVisorStartClose(t *testing.T) {
	r := &router.MockRouter{}
	r.On("Serve", mock.Anything /* context */).Return(testhelpers.NoErr)
	r.On("Close").Return(testhelpers.NoErr)

	apps := make(map[string]AppConfig)
	appCfg := []AppConfig{
		{
			App:       "skychat",
			AutoStart: true,
			Port:      1,
		},
		{
			App:       "foo",
			AutoStart: false,
		},
	}

	for _, app := range appCfg {
		apps[app.App] = app
	}

	defer func() {
		require.NoError(t, os.RemoveAll("skychat"))
	}()

	var (
		visorCfg = Config{
			AppServerAddr: ":5505",
		}
		logger = logging.MustGetLogger("test")
		server = appserver.New(logger, visorCfg.AppServerAddr)
	)

	visor := &Visor{
		conf:         &visorCfg,
		router:       r,
		appsConf:     apps,
		logger:       logger,
		appRPCServer: server,
	}

	pm := &appserver.MockProcManager{}
	appCfg1 := appcommon.Config{
		Name:       apps["skychat"].App,
		ServerAddr: ":5505",
		VisorPK:    visorCfg.Visor.StaticPubKey.Hex(),
		WorkDir:    filepath.Join("", apps["skychat"].App),
	}
	appArgs1 := append([]string{filepath.Join(visor.dir(), apps["skychat"].App)}, apps["skychat"].Args...)
	appPID1 := appcommon.ProcID(10)
	pm.On("Start", mock.Anything, appCfg1, appArgs1, mock.Anything, mock.Anything).
		Return(appPID1, testhelpers.NoErr)
	pm.On("Wait", apps["skychat"].App).Return(testhelpers.NoErr)

	pm.On("StopAll").Return()

	visor.procManager = pm

	dmsgC := dmsg.NewClient(cipher.PubKey{}, cipher.SecKey{}, disc.NewMock(), nil)
	go dmsgC.Serve()

	netConf := snet.Config{
		PubKey:          cipher.PubKey{},
		SecKey:          cipher.SecKey{},
		TpNetworks:      nil,
		DmsgDiscAddr:    "",
		DmsgMinSessions: 0,
	}

	network := snet.NewRaw(netConf, dmsgC, nil)
	tmConf := &transport.ManagerConfig{
		PubKey:          cipher.PubKey{},
		DiscoveryClient: transport.NewDiscoveryMock(),
	}

	tm, err := transport.NewManager(network, tmConf)
	visor.tm = tm
	require.NoError(t, err)

	errCh := make(chan error)
	go func() {
		errCh <- visor.Start()
	}()

	require.NoError(t, <-errCh)
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, visor.Close())
}

func TestVisorSpawnApp(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	r := &router.MockRouter{}
	r.On("Serve", mock.Anything /* context */).Return(testhelpers.NoErr)
	r.On("Close").Return(testhelpers.NoErr)

	defer func() {
		require.NoError(t, os.RemoveAll("skychat"))
	}()

	app := AppConfig{
		App:       "skychat",
		AutoStart: false,
		Port:      10,
		Args:      []string{"foo"},
	}

	apps := make(map[string]AppConfig)
	apps["skychat"] = app

	visorCfg := Config{
		AppServerAddr: ":5505",
	}
	visorCfg.Visor.StaticPubKey = pk

	visor := &Visor{
		router:   r,
		appsConf: apps,
		logger:   logging.MustGetLogger("test"),
		conf:     &visorCfg,
	}

	require.NoError(t, pathutil.EnsureDir(visor.dir()))

	defer func() {
		require.NoError(t, os.RemoveAll(visor.dir()))
	}()

	appCfg := appcommon.Config{
		Name:       app.App,
		ServerAddr: ":5505",
		VisorPK:    visorCfg.Visor.StaticPubKey.Hex(),
		WorkDir:    filepath.Join("", app.App),
	}

	appArgs := append([]string{filepath.Join(visor.dir(), app.App)}, app.Args...)
	appPID := appcommon.ProcID(10)

	pm := &appserver.MockProcManager{}
	pm.On("Wait", app.App).Return(testhelpers.NoErr)
	pm.On("Start", mock.Anything, appCfg, appArgs, mock.Anything, mock.Anything).
		Return(appPID, testhelpers.NoErr)
	pm.On("Exists", app.App).Return(true)
	pm.On("Stop", app.App).Return(testhelpers.NoErr)

	visor.procManager = pm

	require.NoError(t, visor.StartApp(app.App))
	time.Sleep(100 * time.Millisecond)

	require.True(t, visor.procManager.Exists(app.App))

	require.NoError(t, visor.StopApp(app.App))
}

func TestVisorSpawnAppValidations(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	r := &router.MockRouter{}
	r.On("Serve", mock.Anything /* context */).Return(testhelpers.NoErr)
	r.On("Close").Return(testhelpers.NoErr)

	defer func() {
		require.NoError(t, os.RemoveAll("skychat"))
	}()

	c := &Config{
		AppServerAddr: ":5505",
	}
	c.Visor.StaticPubKey = pk

	visor := &Visor{
		router: r,
		logger: logging.MustGetLogger("test"),
		conf:   c,
	}

	require.NoError(t, pathutil.EnsureDir(visor.dir()))

	defer func() {
		require.NoError(t, os.RemoveAll(visor.dir()))
	}()

	t.Run("fail - can't bind to reserved port", func(t *testing.T) {
		app := AppConfig{
			App:  "skychat",
			Port: 3,
		}

		appCfg := appcommon.Config{
			Name:       app.App,
			ServerAddr: ":5505",
			VisorPK:    c.Visor.StaticPubKey.Hex(),
			WorkDir:    filepath.Join("", app.App),
		}

		appArgs := append([]string{filepath.Join(visor.dir(), app.App)}, app.Args...)
		appPID := appcommon.ProcID(10)

		pm := &appserver.MockProcManager{}
		pm.On("Run", mock.Anything, appCfg, appArgs, mock.Anything, mock.Anything).
			Return(appPID, testhelpers.NoErr)
		pm.On("Exists", app.App).Return(false)

		visor.procManager = pm

		errCh := make(chan error)
		go func() {
			errCh <- visor.SpawnApp(&app, nil)
		}()

		time.Sleep(100 * time.Millisecond)

		err := <-errCh
		require.Error(t, err)

		wantErr := "can't bind to reserved port 3"
		assert.Equal(t, wantErr, err.Error())
	})

	t.Run("fail - app already started", func(t *testing.T) {
		app := AppConfig{
			App:  "skychat",
			Port: 10,
		}
		wantErr := fmt.Sprintf("error running app skychat: %s", appserver.ErrAppAlreadyStarted)

		pm := &appserver.MockProcManager{}
		appCfg := appcommon.Config{
			Name:       app.App,
			ServerAddr: ":5505",
			VisorPK:    c.Visor.StaticPubKey.Hex(),
			WorkDir:    filepath.Join("", app.App),
		}
		appArgs := append([]string{filepath.Join(visor.dir(), app.App)}, app.Args...)

		appPID := appcommon.ProcID(10)
		pm.On("Start", mock.Anything, appCfg, appArgs, mock.Anything, mock.Anything).
			Return(appPID, appserver.ErrAppAlreadyStarted)
		pm.On("Exists", app.App).Return(true)

		visor.procManager = pm

		errCh := make(chan error)
		go func() {
			errCh <- visor.SpawnApp(&app, nil)
		}()

		time.Sleep(100 * time.Millisecond)
		err := <-errCh
		require.Error(t, err)
		assert.Equal(t, wantErr, err.Error())
	})
}
