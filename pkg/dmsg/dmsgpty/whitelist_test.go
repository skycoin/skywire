// Package dmsgpty_test provides tests for whitelist and RPC components.
package dmsgpty_test

import (
	"bytes"
	"net"
	"net/rpc"
	"os"
	"path/filepath"
	"testing"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsgpty"
)

// --- MemoryWhitelist tests ---

func TestMemoryWhitelist_AddGetRemoveAll(t *testing.T) {
	wl := dmsgpty.NewMemoryWhitelist()

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	pk3, _ := cipher.GenerateKeyPair()

	// Initially empty.
	all, err := wl.All()
	require.NoError(t, err)
	require.Len(t, all, 0)

	// Get on missing key returns false.
	ok, err := wl.Get(pk1)
	require.NoError(t, err)
	require.False(t, ok)

	// Add single key.
	require.NoError(t, wl.Add(pk1))
	ok, err = wl.Get(pk1)
	require.NoError(t, err)
	require.True(t, ok)

	// Add multiple keys at once.
	require.NoError(t, wl.Add(pk2, pk3))
	all, err = wl.All()
	require.NoError(t, err)
	require.Len(t, all, 3)
	require.True(t, all[pk1])
	require.True(t, all[pk2])
	require.True(t, all[pk3])

	// Remove one key.
	require.NoError(t, wl.Remove(pk2))
	ok, err = wl.Get(pk2)
	require.NoError(t, err)
	require.False(t, ok)

	all, err = wl.All()
	require.NoError(t, err)
	require.Len(t, all, 2)

	// Remove remaining keys.
	require.NoError(t, wl.Remove(pk1, pk3))
	all, err = wl.All()
	require.NoError(t, err)
	require.Len(t, all, 0)
}

func TestMemoryWhitelist_AddDuplicate(t *testing.T) {
	wl := dmsgpty.NewMemoryWhitelist()
	pk, _ := cipher.GenerateKeyPair()

	require.NoError(t, wl.Add(pk))
	require.NoError(t, wl.Add(pk)) // duplicate add should not error

	all, err := wl.All()
	require.NoError(t, err)
	require.Len(t, all, 1)
}

func TestMemoryWhitelist_RemoveNonExistent(t *testing.T) {
	wl := dmsgpty.NewMemoryWhitelist()
	pk, _ := cipher.GenerateKeyPair()

	// Removing a key that was never added should not error.
	require.NoError(t, wl.Remove(pk))
}

// --- ConfigWhitelist tests ---

func TestConfigWhitelist_AddGetRemoveAll(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "test_wl_config.json")

	wl, err := dmsgpty.NewConfigWhitelist(confPath)
	require.NoError(t, err)

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	// Record initial count (global state may carry over from other tests).
	initAll, err := wl.All()
	require.NoError(t, err)
	initCount := len(initAll)

	// Get on missing key.
	ok, err := wl.Get(pk1)
	require.NoError(t, err)
	require.False(t, ok)

	// Add key and verify.
	require.NoError(t, wl.Add(pk1))
	ok, err = wl.Get(pk1)
	require.NoError(t, err)
	require.True(t, ok)

	// Add second key.
	require.NoError(t, wl.Add(pk2))
	all, err := wl.All()
	require.NoError(t, err)
	require.Len(t, all, initCount+2)

	// Remove first key.
	require.NoError(t, wl.Remove(pk1))
	ok, err = wl.Get(pk1)
	require.NoError(t, err)
	require.False(t, ok)

	all, err = wl.All()
	require.NoError(t, err)
	require.Len(t, all, initCount+1)
	require.True(t, all[pk2])
}

func TestConfigWhitelist_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "persist_config.json")

	pk1, _ := cipher.GenerateKeyPair()

	// Create whitelist and add a key.
	wl1, err := dmsgpty.NewConfigWhitelist(confPath)
	require.NoError(t, err)
	require.NoError(t, wl1.Add(pk1))

	// Create a new whitelist instance pointing to the same file.
	wl2, err := dmsgpty.NewConfigWhitelist(confPath)
	require.NoError(t, err)

	ok, err := wl2.Get(pk1)
	require.NoError(t, err)
	require.True(t, ok, "key should persist across whitelist instances")
}

func TestConfigWhitelist_AddDuplicate(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "dup_config.json")

	wl, err := dmsgpty.NewConfigWhitelist(confPath)
	require.NoError(t, err)

	pk, _ := cipher.GenerateKeyPair()
	require.NoError(t, wl.Add(pk))

	// Adding the same key again should return an error about duplicate.
	err = wl.Add(pk)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Already exists")
}

func TestConfigWhitelist_FileCreatedIfMissing(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "subdir", "auto_config.json")

	wl, err := dmsgpty.NewConfigWhitelist(confPath)
	require.NoError(t, err)

	// The file should be created after first operation.
	_, err = wl.All()
	require.NoError(t, err)

	_, err = os.Stat(confPath)
	require.NoError(t, err, "config file should exist after first operation")
}

// --- WhitelistGateway RPC method tests ---

func TestWhitelistGateway_Whitelist(t *testing.T) {
	wl := dmsgpty.NewMemoryWhitelist()
	gw := dmsgpty.NewWhitelistGateway(wl)

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	require.NoError(t, wl.Add(pk1, pk2))

	var out []cipher.PubKey
	err := gw.Whitelist(nil, &out)
	require.NoError(t, err)
	require.Len(t, out, 2)

	// Verify both keys are present.
	found := make(map[cipher.PubKey]bool)
	for _, pk := range out {
		found[pk] = true
	}
	require.True(t, found[pk1])
	require.True(t, found[pk2])
}

func TestWhitelistGateway_WhitelistAdd(t *testing.T) {
	wl := dmsgpty.NewMemoryWhitelist()
	gw := dmsgpty.NewWhitelistGateway(wl)

	pk, _ := cipher.GenerateKeyPair()
	pks := []cipher.PubKey{pk}

	var empty struct{}
	err := gw.WhitelistAdd(&pks, &empty)
	require.NoError(t, err)

	ok, err := wl.Get(pk)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestWhitelistGateway_WhitelistRemove(t *testing.T) {
	wl := dmsgpty.NewMemoryWhitelist()
	gw := dmsgpty.NewWhitelistGateway(wl)

	pk, _ := cipher.GenerateKeyPair()
	require.NoError(t, wl.Add(pk))

	pks := []cipher.PubKey{pk}
	var empty struct{}
	err := gw.WhitelistRemove(&pks, &empty)
	require.NoError(t, err)

	ok, err := wl.Get(pk)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestWhitelistGateway_EmptyWhitelist(t *testing.T) {
	wl := dmsgpty.NewMemoryWhitelist()
	gw := dmsgpty.NewWhitelistGateway(wl)

	var out []cipher.PubKey
	err := gw.Whitelist(nil, &out)
	require.NoError(t, err)
	require.Len(t, out, 0)
}

// --- WhitelistClient + WhitelistGateway integration over net.Pipe ---

func TestWhitelistClientGateway_Integration(t *testing.T) {
	wl := dmsgpty.NewMemoryWhitelist()
	gw := dmsgpty.NewWhitelistGateway(wl)

	connServer, connClient := net.Pipe()

	// Server side: read the request, write response, then serve RPC.
	serverReady := make(chan error, 1)
	go func() {
		defer connServer.Close() //nolint:errcheck,gosec

		// Read the length-prefixed URI request.
		prefix := make([]byte, 1)
		if _, err := connServer.Read(prefix); err != nil {
			serverReady <- err
			return
		}
		uri := make([]byte, prefix[0])
		if _, err := connServer.Read(uri); err != nil {
			serverReady <- err
			return
		}

		// Write accept response (0 byte).
		if _, err := connServer.Write([]byte{0}); err != nil {
			serverReady <- err
			return
		}
		serverReady <- nil

		// Serve RPC on the connection.
		rpcServer := rpc.NewServer()
		if err := rpcServer.RegisterName("whitelist", gw); err != nil {
			return
		}
		rpcServer.ServeConn(connServer)
	}()

	// Client side.
	wlClient, err := dmsgpty.NewWhitelistClient(connClient)
	require.NoError(t, err)
	require.NoError(t, <-serverReady)

	// View empty whitelist.
	pks, err := wlClient.ViewWhitelist()
	require.NoError(t, err)
	require.Len(t, pks, 0)

	// Add keys.
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	require.NoError(t, wlClient.WhitelistAdd(pk1, pk2))

	pks, err = wlClient.ViewWhitelist()
	require.NoError(t, err)
	require.Len(t, pks, 2)

	// Remove one key.
	require.NoError(t, wlClient.WhitelistRemove(pk1))

	pks, err = wlClient.ViewWhitelist()
	require.NoError(t, err)
	require.Len(t, pks, 1)

	require.NoError(t, connClient.Close())
}

// --- RPC util round-trip tests ---
// writeRequest/readRequest and writeResponse/readResponse are unexported,
// so we test them indirectly through the WhitelistClient handshake protocol.

func TestRPCUtil_RequestResponseRoundTrip(t *testing.T) {
	// Test the request/response protocol by simulating what NewWhitelistClient does.
	connA, connB := net.Pipe()

	done := make(chan error, 1)
	go func() {
		defer connA.Close() //nolint:errcheck,gosec

		// Read length-prefixed request.
		prefix := make([]byte, 1)
		if _, err := connA.Read(prefix); err != nil {
			done <- err
			return
		}
		uri := make([]byte, prefix[0])
		if _, err := connA.Read(uri); err != nil {
			done <- err
			return
		}

		// Verify the URI matches expected.
		assert.Equal(t, "dmsgpty/whitelist", string(uri))

		// Write accept (0).
		_, err := connA.Write([]byte{0})
		done <- err
	}()

	// Client side sends the request and reads the response.
	// Write length prefix + URI (same as writeRequest).
	uriStr := "dmsgpty/whitelist"
	buf := make([]byte, 0, 1+len(uriStr))
	buf = append(buf, byte(len(uriStr))) //nolint:gosec
	buf = append(buf, []byte(uriStr)...)
	_, err := connB.Write(buf)
	require.NoError(t, err)

	// Read response (same as readResponse).
	resp := make([]byte, 1)
	_, err = connB.Read(resp)
	require.NoError(t, err)
	require.Equal(t, byte(0), resp[0], "response should be accept (0)")

	require.NoError(t, <-done)
	require.NoError(t, connB.Close())
}

func TestRPCUtil_RejectResponse(t *testing.T) {
	// Test that a rejection byte (non-zero) is correctly detected.
	connA, connB := net.Pipe()

	go func() {
		defer connA.Close() //nolint:errcheck,gosec
		// Read and discard the request.
		prefix := make([]byte, 1)
		connA.Read(prefix) //nolint:errcheck,gosec
		uri := make([]byte, prefix[0])
		connA.Read(uri) //nolint:errcheck,gosec

		// Write reject (1).
		connA.Write([]byte{1}) //nolint:errcheck,gosec
	}()

	// NewWhitelistClient should fail when server rejects.
	_, err := dmsgpty.NewWhitelistClient(connB)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rejected")

	require.NoError(t, connB.Close())
}

// --- Config tests ---

func TestDefaultConfig(t *testing.T) {
	c := dmsgpty.DefaultConfig()
	require.NotEmpty(t, c.DmsgDisc)
	require.NotZero(t, c.DmsgSessions)
	require.Equal(t, uint16(22), c.DmsgPort)
	require.NotEmpty(t, c.CLINet)
	require.NotEmpty(t, c.CLIAddr)
}

func TestWriteConfig(t *testing.T) {
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "write_test_config.json")

	c := dmsgpty.DefaultConfig()
	c.PK = "testpk"
	c.SK = "testsk"

	err := dmsgpty.WriteConfig(c, confPath)
	require.NoError(t, err)

	// Verify file was written.
	data, err := os.ReadFile(confPath) //nolint:gosec
	require.NoError(t, err)
	require.Contains(t, string(data), "testpk")
	require.Contains(t, string(data), "testsk")
}

func TestParseWindowsEnv_NonWindows(t *testing.T) {
	// On non-Windows, ParseWindowsEnv should return the input unchanged.
	input := "%HOMEDRIVE%%HOMEPATH%\\dmsgpty.sock"
	result := dmsgpty.ParseWindowsEnv(input)
	require.NotEmpty(t, result)
}

// --- Additional edge case: large write/read round-trip ---

func TestRPCUtil_LargeURI(t *testing.T) {
	// Test with a URI close to the 255-byte limit.
	connA, connB := net.Pipe()

	// Create a URI of exactly 255 bytes.
	largeURI := bytes.Repeat([]byte("a"), 255)

	done := make(chan error, 1)
	go func() {
		defer connA.Close() //nolint:errcheck,gosec
		prefix := make([]byte, 1)
		if _, err := connA.Read(prefix); err != nil {
			done <- err
			return
		}
		uri := make([]byte, prefix[0])
		if _, err := connA.Read(uri); err != nil {
			done <- err
			return
		}
		assert.Equal(t, string(largeURI), string(uri))
		_, err := connA.Write([]byte{0})
		done <- err
	}()

	// Write: length prefix (255) + URI.
	buf := make([]byte, 0, 256)
	buf = append(buf, byte(255))
	buf = append(buf, largeURI...)
	_, err := connB.Write(buf)
	require.NoError(t, err)

	resp := make([]byte, 1)
	_, err = connB.Read(resp)
	require.NoError(t, err)
	require.Equal(t, byte(0), resp[0])

	require.NoError(t, <-done)
	require.NoError(t, connB.Close())
}
