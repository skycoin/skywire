package visor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// The shape is what Caddy 2.11's admin API returns for a Caddyfile config.
func TestCaddySites(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	config := `{
 "srv0": {"listen": [":443"], "routes": [
  {"match": [{"host": ["skycoin.com", "www.skycoin.com"]}], "handle": []},
  {"match": [{"host": ["*.haltingstate.net", "127.0.0.1", "localhost"]}]},
  {"match": [{"host": ["` + pk.DNSLabel() + `.skywire.dev"]}]},
  {"handle": []}
 ]},
 "srv1": {"listen": [":80"], "routes": [
  {"match": [{"host": ["Skycoin.com", "explorer.skycoin.com"]}, {"path": ["/x"]}]}
 ]}
}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, config) //nolint:errcheck,gosec // a test fixture
	}))
	defer srv.Close()

	sites, err := caddySites(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, []string{"explorer.skycoin.com", "skycoin.com", "www.skycoin.com"}, sites)
}
