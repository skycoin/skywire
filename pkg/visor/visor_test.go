package visor

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/disc"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/internal/testhelpers"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/snet"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/util/pathutil"
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
//	conf.Visor.PubKey = pk
//	conf.Visor.SecKey = sk
//	conf.Dmsg.Discovery = "http://skywire.skycoin.com:8002"
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

	visorCfg := Config{
		KeyPair:       NewKeyPair(),
		AppServerAddr: appcommon.DefaultServerAddr,
	}

	logger := logging.MustGetLogger("test")

	visor := &Visor{
		conf:         &visorCfg,
		router:       r,
		appsConf:     apps,
		logger:       logger,
		appRPCServer: appserver.New(logger, visorCfg.AppServerAddr),
	}

	pm := &appserver.MockProcManager{}
	appCfg1 := appcommon.Config{
		Name:       apps["skychat"].App,
		ServerAddr: appcommon.DefaultServerAddr,
		VisorPK:    visorCfg.Keys().PubKey.Hex(),
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

	var netConf snet.Config

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
		KeyPair:       NewKeyPair(),
		AppServerAddr: appcommon.DefaultServerAddr,
	}

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
		ServerAddr: appcommon.DefaultServerAddr,
		VisorPK:    visorCfg.Keys().PubKey.Hex(),
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
	r := &router.MockRouter{}
	r.On("Serve", mock.Anything /* context */).Return(testhelpers.NoErr)
	r.On("Close").Return(testhelpers.NoErr)

	defer func() {
		require.NoError(t, os.RemoveAll("skychat"))
	}()

	c := &Config{
		KeyPair:       NewKeyPair(),
		AppServerAddr: appcommon.DefaultServerAddr,
	}

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
			ServerAddr: appcommon.DefaultServerAddr,
			VisorPK:    c.Keys().PubKey.Hex(),
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
			ServerAddr: appcommon.DefaultServerAddr,
			VisorPK:    c.Keys().PubKey.Hex(),
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
