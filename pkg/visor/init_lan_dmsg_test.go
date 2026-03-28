package visor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

func TestLoadOrGenerateKeyPair(t *testing.T) {
	log := logging.MustGetLogger("test")
	tmpDir := t.TempDir()

	t.Run("generates new keypair when file doesn't exist", func(t *testing.T) {
		keyFile := filepath.Join(tmpDir, "new_keys.json")
		pk, sk, err := loadOrGenerateKeyPair(keyFile, log)
		require.NoError(t, err)
		assert.False(t, pk.Null())
		assert.False(t, sk.Null())

		// File should have been created
		_, err = os.Stat(keyFile)
		require.NoError(t, err)

		// Verify the saved keypair is valid JSON
		data, err := os.ReadFile(keyFile)
		require.NoError(t, err)
		var kp lanDmsgKeyPair
		require.NoError(t, json.Unmarshal(data, &kp))
		assert.Equal(t, pk.Hex(), kp.PK)
		assert.Equal(t, sk.Hex(), kp.SK)
	})

	t.Run("loads existing keypair from file", func(t *testing.T) {
		keyFile := filepath.Join(tmpDir, "existing_keys.json")

		// Generate and save a keypair
		origPK, origSK := cipher.GenerateKeyPair()
		kp := lanDmsgKeyPair{PK: origPK.Hex(), SK: origSK.Hex()}
		data, err := json.Marshal(kp)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(keyFile, data, 0600))

		// Load it back
		pk, sk, err := loadOrGenerateKeyPair(keyFile, log)
		require.NoError(t, err)
		assert.Equal(t, origPK, pk)
		assert.Equal(t, origSK, sk)
	})

	t.Run("generates new keypair on corrupt file", func(t *testing.T) {
		keyFile := filepath.Join(tmpDir, "corrupt_keys.json")
		require.NoError(t, os.WriteFile(keyFile, []byte("not json"), 0600))

		pk, sk, err := loadOrGenerateKeyPair(keyFile, log)
		require.NoError(t, err)
		assert.False(t, pk.Null())
		assert.False(t, sk.Null())
	})

	t.Run("keypair is stable across loads", func(t *testing.T) {
		keyFile := filepath.Join(tmpDir, "stable_keys.json")

		pk1, sk1, err := loadOrGenerateKeyPair(keyFile, log)
		require.NoError(t, err)

		pk2, sk2, err := loadOrGenerateKeyPair(keyFile, log)
		require.NoError(t, err)

		assert.Equal(t, pk1, pk2)
		assert.Equal(t, sk1, sk2)
	})
}

func TestStartLANDmsgServer(t *testing.T) {
	tmpDir := t.TempDir()
	masterLogger := logging.NewMasterLogger()

	conf := &visorconfig.LANDmsgServerConf{
		Enable:      true,
		Port:        0, // Use random port
		MaxSessions: 10,
		KeyFile:     filepath.Join(tmpDir, "test_server_keys.json"),
	}

	server, err := startLANDmsgServer(conf, masterLogger)
	require.NoError(t, err)
	require.NotNil(t, server)

	defer server.Server.Close() //nolint:errcheck

	// Server should have a valid PK
	assert.False(t, server.PK.Null())

	// Server should have an address
	assert.NotEmpty(t, server.Address)

	// Keypair should be persisted
	_, err = os.Stat(filepath.Join(tmpDir, "test_server_keys.json"))
	assert.NoError(t, err)

	t.Log("LAN DMSG server started on", server.Address, "with PK", server.PK)
}

func TestLANDmsgServerClientConnection(t *testing.T) {
	tmpDir := t.TempDir()
	masterLogger := logging.NewMasterLogger()

	// Start the LAN DMSG server
	conf := &visorconfig.LANDmsgServerConf{
		Enable:      true,
		Port:        0,
		MaxSessions: 10,
		KeyFile:     filepath.Join(tmpDir, "server_keys.json"),
	}

	server, err := startLANDmsgServer(conf, masterLogger)
	require.NoError(t, err)
	require.NotNil(t, server)
	defer server.Server.Close() //nolint:errcheck

	// Give the server a moment to start serving
	time.Sleep(500 * time.Millisecond)

	// Create a DMSG client and connect to the LAN server
	clientPK, clientSK := cipher.GenerateKeyPair()

	// Use a direct client so the DMSG client knows about the server
	serverEntry := &dmsgdisc.Entry{
		Static: server.PK,
		Server: &dmsgdisc.Server{
			Address:           server.Address,
			AvailableSessions: 10,
		},
	}

	// The client needs a discovery that knows about the server
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

	dmsgC.Close() //nolint:errcheck
}

// newTestDirectClient creates a simple in-memory discovery client for testing.
// This avoids importing the direct package which may conflict with other imports.
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

func (c *testDirectClient) AllEntries(_ context.Context) ([]string, error) {
	var entries []string
	for _, e := range c.entries {
		entries = append(entries, e.Static.Hex())
	}
	return entries, nil
}

func (c *testDirectClient) AllClientsByServer(_ context.Context) (map[string][]*dmsgdisc.Entry, error) {
	return nil, nil
}

func (c *testDirectClient) ClientsByServer(_ context.Context, _ cipher.PubKey) ([]*dmsgdisc.Entry, error) {
	return nil, nil
}
