package visor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseBrowseProxy: the one proxy field a browser has (#4484 stage 6) —
// "[scheme://]host:port", socks5 by default, loopback names resolved to the
// visor's own loopback.
func TestParseBrowseProxy(t *testing.T) {
	u, err := parseBrowseProxy("vnet:1080")
	require.NoError(t, err)
	require.Equal(t, "socks5", u.Scheme)
	require.Equal(t, "127.0.0.1:1080", u.Host)

	u, err = parseBrowseProxy("localhost:1080")
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:1080", u.Host)

	u, err = parseBrowseProxy("http://192.168.1.2:3128")
	require.NoError(t, err)
	require.Equal(t, "http", u.Scheme)
	require.Equal(t, "192.168.1.2:3128", u.Host)

	u, err = parseBrowseProxy("socks5h://proxy.example:1080")
	require.NoError(t, err)
	require.Equal(t, "proxy.example:1080", u.Host)

	for _, bad := range []string{"", "   ", "ftp://x:1", "192.168.1.2", "socks5://host"} {
		_, err := parseBrowseProxy(bad)
		require.Error(t, err, bad)
	}
}
