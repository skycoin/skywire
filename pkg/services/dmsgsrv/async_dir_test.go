package dmsgsrv

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAsyncBaseDir(t *testing.T) {
	require.Equal(t, "/etc/skywire", asyncBaseDir("/etc/skywire/dmsg-server.json", "/other/config.json"))
	require.Equal(t, "/other", asyncBaseDir("", "/other/config.json"))
	require.Equal(t, ".", asyncBaseDir("dmsg-server.json", ""))
	require.Equal(t, "", asyncBaseDir("", ""))
}
