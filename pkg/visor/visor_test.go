package visor

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"

	"github.com/stretchr/testify/mock"

	"github.com/SkycoinProject/skywire-mainnet/internal/testhelpers"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appserver"

	"github.com/SkycoinProject/skywire-mainnet/pkg/router"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/disc"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
	"github.com/stretchr/testify/require"

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
//func TestNewNode(t *testing.T) {
//	pk, sk := cipher.GenerateKeyPair()
//	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//		require.NoError(t, json.NewEncoder(w).Encode(&httpauth.NextNonceResponse{Edge: pk, NextNonce: 1}))
//	}))
//	defer srv.Close()
//
//	conf := Config{Version: "1.0", LocalPath: "local", AppsPath: "apps"}
//	conf.Node.StaticPubKey = pk
//	conf.Node.StaticSecKey = sk
//	conf.Messaging.Discovery = "http://skywire.skycoin.net:8001"
//	conf.Messaging.ServerCount = 10
//	conf.Transport.Discovery = srv.URL
//	conf.Apps = []AppConfig{
//		{App: "foo", Version: "1.1", Port: 1},
//		{App: "bar", AutoStart: true, Port: 2},
//	}
//
//	defer func() {
//		require.NoError(t, os.RemoveAll("local"))
//	}()
//
//	node, err := NewNode(&conf, masterLogger)
//	require.NoError(t, err)
//
//	assert.NotNil(t, node.router)
//	assert.NotNil(t, node.appsConf)
//	assert.NotNil(t, node.appsPath)
//	assert.NotNil(t, node.localPath)
//	assert.NotNil(t, node.startedApps)
//}

// TODO (Darkren): fix tests
func TestNodeStartClose(t *testing.T) {
	r := &router.MockRouter{}
	r.On("Serve", mock.Anything /* context */).Return(testhelpers.NoErr)
	r.On("Close").Return(testhelpers.NoErr)

	conf := []AppConfig{
		{
			App:       "skychat",
			Version:   "1.0",
			AutoStart: true,
			Port:      1,
		},
		{
			App:       "foo",
			Version:   "1.0",
			AutoStart: false,
		},
	}

	defer func() {
		require.NoError(t, os.RemoveAll("skychat"))
	}()

	nodeCfg := Config{}

	node := &Node{
		conf:     &nodeCfg,
		router:   r,
		appsConf: conf,
		logger:   logging.MustGetLogger("test"),
	}

	pm := &appserver.MockProcManager{}
	appCfg1 := appcommon.Config{
		Name:     conf[0].App,
		Version:  conf[0].Version,
		SockFile: nodeCfg.AppServerSockFile,
		VisorPK:  nodeCfg.Node.StaticPubKey.Hex(),
		WorkDir:  filepath.Join("", conf[0].App, fmt.Sprintf("v%s", conf[0].Version)),
	}
	appArgs1 := append([]string{filepath.Join(node.dir(), conf[0].App)}, conf[0].Args...)
	appPID1 := appcommon.ProcID(10)
	pm.On("Run", mock.Anything, appCfg1, appArgs1, mock.Anything, mock.Anything).
		Return(appPID1, testhelpers.NoErr)
	pm.On("Wait", conf[0].App).Return(testhelpers.NoErr)

	pm.On("Range", mock.AnythingOfType("func(string, *appserver.Proc) bool")).Return(func(f func(string, *appserver.Proc) bool) {
		f(conf[0].App, nil)
	})

	node.procManager = pm

	dmsgC := dmsg.NewClient(cipher.PubKey{}, cipher.SecKey{}, disc.NewMock())
	netConf := snet.Config{
		PubKey:       cipher.PubKey{},
		SecKey:       cipher.SecKey{},
		TpNetworks:   nil,
		DmsgDiscAddr: "",
		DmsgMinSrvs:  0,
	}

	network := snet.NewRaw(netConf, dmsgC, nil)
	tmConf := &transport.ManagerConfig{
		PubKey:          cipher.PubKey{},
		DiscoveryClient: transport.NewDiscoveryMock(),
	}

	tm, err := transport.NewManager(network, tmConf)
	node.tm = tm
	require.NoError(t, err)

	errCh := make(chan error)
	go func() {
		errCh <- node.Start()
	}()

	require.NoError(t, <-errCh)
	time.Sleep(100 * time.Millisecond)
	require.NoError(t, node.Close())

	//require.False(t, node.procManager.Exists(conf[0].App))
	//require.False(t, node.procManager.Exists(conf[1].App))
}

/*func TestNodeSpawnApp(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	r := new(mockRouter)
	executer := &MockExecuter{}
	defer func() {
		require.NoError(t, os.RemoveAll("skychat"))
	}()
	apps := []AppConfig{{App: "skychat", Version: "1.0", AutoStart: false, Port: 10, Args: []string{"foo"}}}
	node := &Node{router: r, exec: executer, appsConf: apps, startedApps: map[string]*appBind{}, logger: logging.MustGetLogger("test"),
		conf: &Config{}}
	node.conf.Node.StaticPubKey = pk
	pathutil.EnsureDir(node.dir())
	defer func() {
		require.NoError(t, os.RemoveAll(node.dir()))
	}()

	require.NoError(t, node.StartApp("skychat"))
	time.Sleep(100 * time.Millisecond)

	require.NotNil(t, node.startedApps["skychat"])

	executer.Lock()
	require.Len(t, executer.cmds, 1)
	assert.Equal(t, "skychat.v1.0", executer.cmds[0].Path)
	assert.Equal(t, "skychat/v1.0", executer.cmds[0].Dir)
	assert.Equal(t, "skychat.v1.0", executer.cmds[0].Args[0])
	assert.Equal(t, "foo", executer.cmds[0].Args[2])
	executer.Unlock()

	ports := r.Ports()
	require.Len(t, ports, 1)
	assert.Equal(t, routing.Port(10), ports[0])

	require.NoError(t, node.StopApp("skychat"))
}

func TestNodeSpawnAppValidations(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	conn, _ := net.Pipe()
	r := new(mockRouter)
	executer := &MockExecuter{err: errors.New("foo")}
	defer func() {
		require.NoError(t, os.RemoveAll("skychat"))
	}()
	c := &Config{}
	c.Node.StaticPubKey = pk
	node := &Node{router: r, exec: executer,
		startedApps: map[string]*appBind{"skychat": {conn, 10}},
		logger:      logging.MustGetLogger("test"),
		conf:        c,
	}
	defer os.Remove(node.dir()) // nolint

	cases := []struct {
		conf *AppConfig
		err  string
	}{
		{&AppConfig{App: "skychat", Version: "1.0", Port: 2}, "can't bind to reserved port 2"},
		{&AppConfig{App: "skychat", Version: "1.0", Port: 10}, "app skychat is already started"},
		{&AppConfig{App: "foo", Version: "1.0", Port: 11}, "failed to run app executable: foo"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.err, func(t *testing.T) {
			errCh := make(chan error)
			go func() {
				errCh <- node.SpawnApp(tc.conf, nil)
			}()

			time.Sleep(100 * time.Millisecond)
			require.NoError(t, node.Close())
			err := <-errCh
			require.Error(t, err)
			assert.Equal(t, tc.err, err.Error())
		})
	}
}

type MockExecuter struct {
	sync.Mutex
	err    error
	cmds   []*exec.Cmd
	stopCh chan struct{}
}

func (exc *MockExecuter) Start(cmd *exec.Cmd) (int, error) {
	exc.Lock()
	defer exc.Unlock()
	if exc.stopCh != nil {
		return -1, errors.New("already executing")
	}

	exc.stopCh = make(chan struct{})

	if exc.err != nil {
		return -1, exc.err
	}

	if exc.cmds == nil {
		exc.cmds = make([]*exec.Cmd, 0)
	}

	exc.cmds = append(exc.cmds, cmd)

	return 10, nil
}

func (exc *MockExecuter) Stop(pid int) error {
	exc.Lock()
	if exc.stopCh != nil {
		select {
		case <-exc.stopCh:
		default:
			close(exc.stopCh)
		}
	}
	exc.Unlock()
	return nil
}

func (exc *MockExecuter) Wait(cmd *exec.Cmd) error {
	<-exc.stopCh
	return nil
}

/*type mockRouter struct {
	sync.Mutex

	ports []routing.Port

	didStart bool
	didClose bool

	errChan chan error
}

func (r *mockRouter) DialRoutes(ctx context.Context, rPK cipher.PubKey, lPort, rPort routing.Port, opts *router.DialOptions) (*router.RouteGroup, error) {
	panic("implement me")
}

func (r *mockRouter) AcceptRoutes(ctx context.Context) (*router.RouteGroup, error) {
	panic("implement me")
}

func (r *mockRouter) Ports() []routing.Port {
	r.Lock()
	p := r.ports
	r.Unlock()
	return p
}

func (r *mockRouter) Serve(context.Context) error {
	r.didStart = true
	return nil
}

func (r *mockRouter) ServeApp(conn net.Conn, port routing.Port, appConf *app.Config) error {
	r.Lock()
	if r.ports == nil {
		r.ports = []routing.Port{}
	}

	r.ports = append(r.ports, port)
	r.Unlock()

	if r.errChan == nil {
		r.Lock()
		r.errChan = make(chan error)
		r.Unlock()
	}

	return <-r.errChan
}

func (r *mockRouter) Close() error {
	if r == nil {
		return nil
	}
	r.didClose = true
	r.Lock()
	if r.errChan != nil {
		close(r.errChan)
	}
	r.Unlock()
	return nil
}

func (r *mockRouter) IsSetupTransport(*transport.ManagedTransport) bool {
	return false
}

func (r *mockRouter) SetupIsTrusted(cipher.PubKey) bool {
	return true
}
*/
