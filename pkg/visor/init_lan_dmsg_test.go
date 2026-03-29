package visor

import (
	"context"
	"testing"
	"time"

	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestStartLANDmsgServer(t *testing.T) {
	masterLogger := logging.NewMasterLogger()

	conf := &visorconfig.LANDmsgServerConf{
		Enable:      true,
		Port:        0, // Auto-select port
		MaxSessions: 10,
	}

	// Create a minimal visor config (Flush is a no-op without a config path)
	visorConf := &visorconfig.V1{}
	visorConf.Hypervisor = &visorconfig.HypervisorConfig{
		LANDmsgServer: conf,
	}

	server, err := startLANDmsgServer(conf, visorConf, masterLogger)
	require.NoError(t, err)
	require.NotNil(t, server)
	defer server.Server.Close() //nolint:errcheck

	// Server should have a valid PK
	assert.False(t, server.PK.Null())

	// Server should have an address with a real port
	assert.NotEmpty(t, server.Address)
	assert.Greater(t, server.Port, 0)

	// PK should have been saved to config
	assert.Equal(t, server.PK, conf.PK)
	assert.False(t, conf.SK.Null())

	t.Logf("LAN DMSG server started on %s (port %d) with PK %s", server.Address, server.Port, server.PK)
}

func TestLANDmsgServerClientConnection(t *testing.T) {
	masterLogger := logging.NewMasterLogger()

	conf := &visorconfig.LANDmsgServerConf{
		Enable:      true,
		Port:        0,
		MaxSessions: 10,
	}

	visorConf := &visorconfig.V1{}
	visorConf.Hypervisor = &visorconfig.HypervisorConfig{
		LANDmsgServer: conf,
	}

	server, err := startLANDmsgServer(conf, visorConf, masterLogger)
	require.NoError(t, err)
	require.NotNil(t, server)
	defer server.Server.Close() //nolint:errcheck

	// Give the server a moment to start serving
	time.Sleep(500 * time.Millisecond)

	// Create a DMSG client and connect to the LAN server
	clientPK, clientSK := cipher.GenerateKeyPair()

	serverEntry := &dmsgdisc.Entry{
		Static: server.PK,
		Server: &dmsgdisc.Server{
			Address:           server.Address,
			AvailableSessions: 10,
		},
	}

	clientDisc := newTestDirectClient([]*dmsgdisc.Entry{serverEntry})

	dmsgConf := &dmsg.Config{
		MinSessions: 1,
	}
	dmsgC := dmsg.NewClient(clientPK, clientSK, clientDisc, dmsgConf)
	dmsgC.SetLogger(masterLogger.PackageLogger("test_client"))

	go dmsgC.Serve(context.Background()) //nolint:errcheck

	// Wait for the client to connect
	time.Sleep(2 * time.Second)

	sessions := dmsgC.AllSessions()
	assert.GreaterOrEqual(t, len(sessions), 1, "client should have at least one session")

	if len(sessions) > 0 {
		assert.Equal(t, server.PK, sessions[0].RemotePK(), "session should be to the LAN server")
		t.Log("Client connected to LAN server successfully")
	}

	err = dmsgC.Close()
	assert.NoError(t, err)
}

// testDirectClient is a minimal in-memory disc.APIClient for testing.
type testDirectClient struct {
	entries map[cipher.PubKey]*dmsgdisc.Entry
}

func newTestDirectClient(entries []*dmsgdisc.Entry) dmsgdisc.APIClient {
	m := make(map[cipher.PubKey]*dmsgdisc.Entry)
	for _, e := range entries {
		m[e.Static] = e
	}
	return &testDirectClient{entries: m}
}

func (c *testDirectClient) Entry(_ context.Context, pk cipher.PubKey) (*dmsgdisc.Entry, error) {
	if e, ok := c.entries[pk]; ok {
		return e, nil
	}
	return nil, dmsgdisc.ErrKeyNotFound
}
func (c *testDirectClient) PostEntry(_ context.Context, e *dmsgdisc.Entry) error {
	c.entries[e.Static] = e
	return nil
}
func (c *testDirectClient) DelEntry(_ context.Context, e *dmsgdisc.Entry) error {
	delete(c.entries, e.Static)
	return nil
}
func (c *testDirectClient) PutEntry(_ context.Context, _ cipher.SecKey, e *dmsgdisc.Entry) error {
	c.entries[e.Static] = e
	return nil
}
func (c *testDirectClient) AvailableServers(_ context.Context) ([]*dmsgdisc.Entry, error) {
	var entries []*dmsgdisc.Entry
	for _, e := range c.entries {
		if e.Server != nil {
			entries = append(entries, e)
		}
	}
	return entries, nil
}
func (c *testDirectClient) AllServers(_ context.Context) ([]*dmsgdisc.Entry, error) {
	return c.AvailableServers(context.Background())
}
func (c *testDirectClient) AllEntries(_ context.Context) ([]string, error) { return nil, nil }
func (c *testDirectClient) AllClientsByServer(_ context.Context) (map[string][]*dmsgdisc.Entry, error) {
	return nil, nil
}
func (c *testDirectClient) ClientsByServer(_ context.Context, _ cipher.PubKey) ([]*dmsgdisc.Entry, error) {
	return nil, nil
}
