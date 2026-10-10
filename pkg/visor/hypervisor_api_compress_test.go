package visor

import (
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

// The hypervisor's /api surface gzips JSON over httputil.CompressMinBytes,
// which httputil tests. A reply smaller than that, like this one, stays plain.
func TestHypervisorAPISmallJSONStaysPlain(t *testing.T) {
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

	require.Empty(t, resp.Header.Get("Content-Encoding"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(body), `"exists"`), "body: %s", body)
}
