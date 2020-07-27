package visor

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/SkycoinProject/skycoin/src/util/logging"
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
//	conf := Config{AppLocalPath: "local", AppBinPath: "apps"}
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

// TODO(evanlinjin): Move to /pkg/app/launcher
//func TestVisorStartClose(t *testing.T) {
//	tmpDir, err := ioutil.TempDir(os.TempDir(), "")
//	require.NoError(t, err)
//	defer func() {
//		require.NoError(t, os.RemoveAll(tmpDir))
//		require.NoError(t, os.RemoveAll("apps-pid.txt"))
//	}()
//
//	r := &router.MockRouter{}
//	r.On("Serve", mock.Anything /* context */).Return(testhelpers.NoErr)
//	r.On("Close").Return(testhelpers.NoErr)
//
//	apps := make(map[string]AppConfig)
//	appCfg := []AppConfig{
//		{
//			App:       "skychat",
//			AutoStart: true,
//			Port:      1,
//		},
//		{
//			App:       "foo",
//			AutoStart: false,
//		},
//	}
//
//	for _, app := range appCfg {
//		apps[app.App] = app
//	}
//
//	defer func() {
//		require.NoError(t, os.RemoveAll("skychat"))
//	}()
//
//	visorCfg := Config{
//		KeyPair:       NewKeyPair(),
//		AppServerAddr: appcommon.DefaultAppSrvAddr,
//	}
//
//	logger := logging.MustGetLogger("test")
//
//	visor := &Visor{
//		conf:     &visorCfg,
//		router:   r,
//		appsConf: apps,
//		log:      logger,
//	}
//
//	appPID1 := appcommon.ProcID(10)
//
//	pm := &appserver.MockProcManager{}
//	pm.On("Start", mock.Anything).Return(appPID1, testhelpers.NoErr)
//	pm.On("Wait", apps["skychat"].App).Return(testhelpers.NoErr)
//	pm.On("Close").Return(testhelpers.NoErr)
//	visor.procM = pm
//
//	dmsgC := dmsg.NewClient(cipher.PubKey{}, cipher.SecKey{}, disc.NewMock(), nil)
//	go dmsgC.Serve()
//
//	var netConf snet.Config
//
//	network := snet.NewRaw(netConf, dmsgC, nil)
//	tmConf := &transport.ManagerConfig{
//		PubKey:          cipher.PubKey{},
//		DiscoveryClient: transport.NewDiscoveryMock(),
//	}
//
//	tm, err := transport.NewManager(network, tmConf)
//	visor.tpM = tm
//	require.NoError(t, err)
//
//	errCh := make(chan error)
//	go func() {
//		errCh <- visor.Start()
//	}()
//
//	require.NoError(t, <-errCh)
//	time.Sleep(100 * time.Millisecond)
//	require.NoError(t, visor.Close())
//}
//
//func TestVisorSpawnApp(t *testing.T) {
//	tmpDir, err := ioutil.TempDir(os.TempDir(), "")
//	require.NoError(t, err)
//	defer func() {
//		require.NoError(t, os.RemoveAll(tmpDir))
//		require.NoError(t, os.RemoveAll("apps-pid.txt"))
//	}()
//
//	r := &router.MockRouter{}
//	r.On("Serve", mock.Anything /* context */).Return(testhelpers.NoErr)
//	r.On("Close").Return(testhelpers.NoErr)
//
//	defer func() {
//		require.NoError(t, os.RemoveAll("skychat"))
//	}()
//
//	app := AppConfig{
//		App:       "skychat",
//		AutoStart: false,
//		Port:      10,
//		Args:      []string{"foo"},
//	}
//
//	apps := make(map[string]AppConfig)
//	apps["skychat"] = app
//
//	visorCfg := Config{
//		KeyPair:       NewKeyPair(),
//		AppServerAddr: appcommon.DefaultAppSrvAddr,
//	}
//
//	visor := &Visor{
//		router:   r,
//		appsConf: apps,
//		log:      logging.MustGetLogger("test"),
//		conf:     &visorCfg,
//	}
//
//	appPID := appcommon.ProcID(10)
//
//	pm := &appserver.MockProcManager{}
//	pm.On("Wait", app.App).Return(testhelpers.NoErr)
//	pm.On("Start", mock.Anything).Return(appPID, testhelpers.NoErr)
//	pm.On("ProcByName", app.App).Return(new(appserver.Proc), true)
//	pm.On("Stop", app.App).Return(testhelpers.NoErr)
//
//	visor.procM = pm
//
//	require.NoError(t, visor.StartApp(app.App))
//	time.Sleep(100 * time.Millisecond)
//
//	_, ok := visor.procM.ProcByName(app.App)
//	require.True(t, ok)
//
//	require.NoError(t, visor.StopApp(app.App))
//}
//
//func TestVisorSpawnAppValidations(t *testing.T) {
//	tmpDir, err := ioutil.TempDir(os.TempDir(), "")
//	require.NoError(t, err)
//	defer func() { require.NoError(t, os.RemoveAll(tmpDir)) }()
//
//	r := &router.MockRouter{}
//	r.On("Serve", mock.Anything /* context */).Return(testhelpers.NoErr)
//	r.On("Close").Return(testhelpers.NoErr)
//
//	defer func() {
//		require.NoError(t, os.RemoveAll("skychat"))
//	}()
//
//	c := &Config{
//		KeyPair:       NewKeyPair(),
//		AppServerAddr: appcommon.DefaultAppSrvAddr,
//	}
//
//	visor := &Visor{
//		router: r,
//		log:    logging.MustGetLogger("test"),
//		conf:   c,
//	}
//
//	t.Run("fail - can't bind to reserved port", func(t *testing.T) {
//		app := AppConfig{
//			App:  "skychat",
//			Port: 3,
//		}
//
//		appCfg := appcommon.ProcConfig{
//			AppName:     app.App,
//			AppSrvAddr:  appcommon.DefaultAppSrvAddr,
//			VisorPK:     c.Keys().PubKey,
//			RoutingPort: app.Port,
//			ProcWorkDir: filepath.Join(tmpDir, app.App),
//		}
//
//		appPID := appcommon.ProcID(10)
//
//		pm := &appserver.MockProcManager{}
//		pm.On("Run", mock.Anything, appCfg, app.Args, mock.Anything, mock.Anything).Return(appPID, testhelpers.NoErr)
//		pm.On("ProcByName", app.App).Return(new(appserver.Proc), false)
//
//		visor.procM = pm
//
//		errCh := make(chan error)
//		go func() {
//			errCh <- visor.SpawnApp(&app, nil)
//		}()
//
//		time.Sleep(100 * time.Millisecond)
//
//		err := <-errCh
//		require.Error(t, err)
//
//		wantErr := "can't bind to reserved port 3"
//		assert.Equal(t, wantErr, err.Error())
//	})
//
//	t.Run("fail - app already started", func(t *testing.T) {
//		app := AppConfig{
//			App:  "skychat",
//			Port: 10,
//		}
//		wantErr := fmt.Sprintf("failed to start app skychat: %s", appserver.ErrAppAlreadyStarted)
//
//		appPID := appcommon.ProcID(10)
//
//		pm := &appserver.MockProcManager{}
//		pm.On("Start", mock.Anything).Return(appPID, appserver.ErrAppAlreadyStarted)
//		pm.On("ProcByName", app.App).Return(new(appserver.Proc), true)
//
//		visor.procM = pm
//
//		errCh := make(chan error)
//		go func() {
//			errCh <- visor.SpawnApp(&app, nil)
//		}()
//
//		time.Sleep(100 * time.Millisecond)
//		err := <-errCh
//		require.Error(t, err)
//		assert.Equal(t, wantErr, err.Error())
//	})
//}
