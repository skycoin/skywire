package dmsgtest

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/nettest"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/disc"
)

// DefaultTimeout is the recommended timeout for the Env.
const DefaultTimeout = time.Minute
const maxSessions = 10

// Env can run an entire local dmsg environment inclusive of a mock discovery, dmsg servers and clients.
type Env struct {
	t       *testing.T
	timeout time.Duration

	d  disc.APIClient
	s  map[cipher.PubKey]*dmsg.Server
	c  map[cipher.PubKey]*dmsg.Client
	mx sync.RWMutex

	sWg sync.WaitGroup // waits for (*dmsg.Server).Serve() to return
	cWg sync.WaitGroup // waits for (*dmsg.Client).Serve() to return
}

// NewEnv creates a new dmsg environment.
// The inputs 't' and 'timeout' are optional.
// If 't' is specified, some log messages are displayed via 't.Log()'.
// If 'timeout' is not '0', starting entities (such as servers and clients) must complete in the given duration,
//	otherwise it will fail.
func NewEnv(t *testing.T, timeout time.Duration) *Env {
	return &Env{
		t:       t,
		timeout: timeout,
		s:       make(map[cipher.PubKey]*dmsg.Server),
		c:       make(map[cipher.PubKey]*dmsg.Client),
	}
}

// Startup runs the specified number of dmsg servers and clients.
// The input 'conf' is optional, and is passed when creating clients.
func (env *Env) Startup(entryTimeout time.Duration, servers, clients int, conf *dmsg.Config) error {
	ctx, cancel := timeoutContext(env.timeout)
	defer cancel()

	env.mx.Lock()
	defer env.mx.Unlock()

	env.d = disc.NewMock(entryTimeout)

	for i := 0; i < servers; i++ {
		if _, err := env.newServer(ctx, dmsg.DefaultUpdateInterval); err != nil {
			return err
		}
	}
	for i := 0; i < clients; i++ {
		pk, sk := cipher.GenerateKeyPair()
		if _, err := env.newClientWithKeys(ctx, pk, sk, conf); err != nil {
			return err
		}
	}
	return nil
}

// NewServer runs a new server.
func (env *Env) NewServer(updateInterval time.Duration) (*dmsg.Server, error) {
	ctx, cancel := timeoutContext(env.timeout)
	defer cancel()

	env.mx.Lock()
	defer env.mx.Unlock()

	return env.newServer(ctx, updateInterval)
}

func (env *Env) newServer(ctx context.Context, updateInterval time.Duration) (*dmsg.Server, error) {
	pk, sk := cipher.GenerateKeyPair()

	conf := dmsg.ServerConfig{
		MaxSessions:    maxSessions,
		UpdateInterval: updateInterval,
	}
	srv := dmsg.NewServer(pk, sk, env.d, &conf, nil)
	env.s[pk] = srv
	env.sWg.Add(1)

	l, err := nettest.NewLocalListener("tcp")
	if err != nil {
		return nil, err
	}

	go func() {
		if err := srv.Serve(l, ""); err != nil && env.t != nil {
			env.t.Logf("dmsgtest.Env: dmsg server of pk %s stopped serving with error: %v", pk, err)
		}
		env.mx.Lock()
		delete(env.s, srv.LocalPK())
		env.mx.Unlock()
		env.sWg.Done()
	}()

	select {
	case <-ctx.Done():
		_ = srv.Close() //nolint:errcheck
		return nil, ctx.Err()
	case <-srv.Ready():
		return srv, nil
	}
}

// NewClient runs a new client.
func (env *Env) NewClient(conf *dmsg.Config) (*dmsg.Client, error) {
	ctx, cancel := timeoutContext(env.timeout)
	defer cancel()

	env.mx.Lock()
	defer env.mx.Unlock()

	pk, sk := cipher.GenerateKeyPair()
	return env.newClientWithKeys(ctx, pk, sk, conf)
}

// NewClientWithKeys runs a new client with specified keys.
func (env *Env) NewClientWithKeys(pk cipher.PubKey, sk cipher.SecKey, conf *dmsg.Config) (*dmsg.Client, error) {
	ctx, cancel := timeoutContext(env.timeout)
	defer cancel()

	env.mx.Lock()
	defer env.mx.Unlock()

	return env.newClientWithKeys(ctx, pk, sk, conf)
}

func (env *Env) newClientWithKeys(ctx context.Context, pk cipher.PubKey, sk cipher.SecKey, conf *dmsg.Config) (*dmsg.Client, error) {
	c := dmsg.NewClient(pk, sk, env.d, conf)
	env.c[pk] = c
	env.cWg.Add(1)

	go func() {
		c.Serve()
		env.mx.Lock()
		delete(env.c, pk)
		env.mx.Unlock()
		env.cWg.Done()
	}()

	select {
	case <-ctx.Done():
		_ = c.Close() //nolint:errcheck
		return nil, ctx.Err()
	case <-c.Ready():
		return c, nil
	}
}

// Discovery returns the discovery client.
func (env *Env) Discovery() disc.APIClient {
	return env.d
}

// AllClients returns all the clients of the Env.
func (env *Env) AllClients() []*dmsg.Client {
	env.mx.RLock()
	defer env.mx.RUnlock()

	clients := make([]*dmsg.Client, 0, len(env.c))
	for _, c := range env.c {
		clients = append(clients, c)
	}
	sort.SliceStable(clients, func(i, j int) bool {
		cI := clients[i].LocalPK().Big()
		cJ := clients[j].LocalPK().Big()
		return cI.Cmp(cJ) < 0
	})
	return clients
}

// AllServers returns all the servers of the Env.
func (env *Env) AllServers() []*dmsg.Server {
	env.mx.RLock()
	defer env.mx.RUnlock()

	servers := make([]*dmsg.Server, 0, len(env.c))
	for _, s := range env.s {
		servers = append(servers, s)
	}
	sort.SliceStable(servers, func(i, j int) bool {
		cI := servers[i].LocalPK().Big()
		cJ := servers[j].LocalPK().Big()
		return cI.Cmp(cJ) < 0
	})
	return servers
}

// ClientOfPK returns client of a given public key.
func (env *Env) ClientOfPK(pk cipher.PubKey) (*dmsg.Client, bool) {
	env.mx.RLock()
	defer env.mx.RUnlock()

	for cPK, c := range env.c {
		if cPK == pk {
			return c, true
		}
	}

	return nil, false
}

// Shutdown closes all servers and clients of the Env.
func (env *Env) Shutdown() {
	env.CloseAllClients()
	env.CloseAllServers()
}

// CloseAllClients closes all clients of the Env.
func (env *Env) CloseAllClients() {
	for _, c := range env.AllClients() {
		if err := c.Close(); err != nil && env.t != nil {
			env.t.Logf("dmsgtest.Env: dmsg client of pk %s closed with error: %v", c.LocalPK(), err)
		}
	}
	env.cWg.Wait()
}

// CloseAllServers closes all servers of the Env.
func (env *Env) CloseAllServers() {
	for _, s := range env.AllServers() {
		if err := s.Close(); err != nil && env.t != nil {
			env.t.Logf("dmsgtest.Env: dmsg server of pk %s closed with error: %v", s.LocalPK(), err)
		}
	}
	env.sWg.Wait()
}

func timeoutContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx := context.Background()
	if timeout > 0 {
		return context.WithDeadline(ctx, time.Now().Add(timeout))
	}
	return context.WithCancel(ctx)
}
