package transport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDMSGFactoryNoNetwork covers the constructor and the accessor/close paths
// that do not touch the dmsg client. Listen/Connect/acceptLoop dereference a
// real *dmsg.Client (live dmsg network) and are not unit-testable as-is.
func TestDMSGFactoryNoNetwork(t *testing.T) {
	f := NewDMSGFactory(nil, DefaultCXOPort)
	require.NotNil(t, f)

	// No listener yet → empty address.
	assert.Empty(t, f.Address())

	// Close with a nil listener must be safe and idempotent.
	f.Close()
	f.Close()
}
