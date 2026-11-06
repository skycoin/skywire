package netutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDNSServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dns-servers")
	require.Equal(t, fallbackDNSServers, dnsServers(path), "no file yet")

	require.NoError(t, os.WriteFile(path, []byte("192.168.1.1\n\nnot an address\n2001:4860:4860::8888\n192.168.1.1\n8.8.8.8\n"), 0o600))
	require.Equal(t, []string{"192.168.1.1:53", "[2001:4860:4860::8888]:53", "8.8.8.8:53", "1.1.1.1:53"}, dnsServers(path))
}
