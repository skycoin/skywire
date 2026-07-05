// Package commands cmd/apps/skynet/commands/skynet_test.go
//
// Covers createRPCClient, the one helper callable without a live visor app
// environment. The rest of the package (RunSkynet) sits behind app.NewClient,
// which Fatals without the visor-injected environment, so its port-parsing and
// registration logic is not reachable in a unit test without refactoring.
package commands

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateRPCClientDialError verifies a malformed/unreachable address is
// surfaced as an error rather than a client.
func TestCreateRPCClientDialError(t *testing.T) {
	// A missing-port address fails net.DialTimeout immediately (no waiting on
	// the 5s timeout).
	client, err := createRPCClient("missing-port")
	assert.Error(t, err)
	assert.Nil(t, client)
}

// TestCreateRPCClientSuccess verifies that, given a reachable TCP endpoint, a
// non-nil visor RPC client is returned. The listener need not speak RPC: the
// constructor only wraps the established connection.
func TestCreateRPCClientSuccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close() //nolint:errcheck

	client, err := createRPCClient(ln.Addr().String())
	require.NoError(t, err)
	assert.NotNil(t, client)
}
