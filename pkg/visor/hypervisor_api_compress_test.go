package visor

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// The hypervisor's /api surface must negotiate gzip. Everything on it
// is JSON, and the node list re-fetches the whole thing every 10s.
func TestHypervisorAPICompressesJSON(t *testing.T) {
	conf := visorconfig.MakeConfig(false)
	conf.FillDefaults(false)
	conf.DBPath = filepath.Join(t.TempDir(), "users_test.db")

	hv, err := NewHypervisor(conf, nil, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, os.RemoveAll(conf.DBPath)) }()
	defer func() { _ = hv.users.Close() }() //nolint:errcheck

	srv := httptest.NewServer(hv.HTTPHandler())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/user-exists", nil)
	require.NoError(t, err)
	// net/http's transport strips Content-Encoding when it added
	// Accept-Encoding itself; set it explicitly so the header survives.
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := http.DefaultTransport.RoundTrip(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck

	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	require.Contains(t, resp.Header.Get("Vary"), "Accept-Encoding")

	zr, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	body, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(body), `"exists"`), "body: %s", body)
}
