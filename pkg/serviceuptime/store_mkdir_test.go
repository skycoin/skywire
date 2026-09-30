package serviceuptime

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A path whose directory does not exist yet is created, as a service's
// default path is in a fresh container.
func TestOpenStoreCreatesItsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "var", "lib", "skywire", "tpd", "uptime.db")
	s, err := OpenStore(path)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	require.FileExists(t, path)
}
