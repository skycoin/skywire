//go:build !tinygo

package disc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestNewHTTP_EmptyAddress_NoOp verifies that an empty discovery address yields
// a no-op client: server-list lookups return empty without error (so the dmsg
// serve-loop doesn't spin on an invalid host), and entry lookups fail cleanly.
func TestNewHTTP_EmptyAddress_NoOp(t *testing.T) {
	c := NewHTTP("", nil, logging.MustGetLogger("disc-test"))
	_, ok := c.(noDiscoveryClient)
	require.True(t, ok, "empty address must yield the no-op client")

	srvs, err := c.AvailableServers(context.Background())
	require.NoError(t, err)
	assert.Empty(t, srvs)

	all, err := c.AllServers(context.Background())
	require.NoError(t, err)
	assert.Empty(t, all)

	pk, _ := cipher.GenerateKeyPair()
	_, err = c.Entry(context.Background(), pk)
	assert.ErrorIs(t, err, ErrNoAvailableServers)

	assert.NoError(t, c.PostEntry(context.Background(), nil))
}
