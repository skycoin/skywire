// Package commands cmd/dmsg/dmsgweb/commands/peerpk_test.go
//
// Covers peerPK's fallback branch: a connection that is not a *dmsg.Stream has
// no dmsg public key, so peerPK returns the zero key. The *dmsg.Stream branch
// needs a live dmsg session and is not unit-testable as-is; printEnvs ends in
// os.Exit and server/proxyTCPConnections are network paths.
package commands

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestPeerPKNonStream verifies a non-dmsg connection yields the zero public key.
func TestPeerPKNonStream(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close() //nolint:errcheck
	defer c2.Close() //nolint:errcheck

	assert.Equal(t, cipher.PubKey{}, peerPK(c1))
}
