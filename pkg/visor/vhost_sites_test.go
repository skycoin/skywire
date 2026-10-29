package visor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
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

// A port 80 forward lists Caddy's sites only when it hands the Host on to a
// server on this machine, as a "*" vhost does.
func TestLocalSiteList(t *testing.T) {
	v := &Visor{}
	v.caddySites.list, v.caddySites.at = []string{"magnetosphere.net"}, time.Now()
	log := logging.MustGetLogger("test")

	require.Equal(t, []string{"magnetosphere.net"}, v.localSiteList(true, "127.0.0.1", log)())
	require.Nil(t, v.localSiteList(false, "127.0.0.1", log), "Host not preserved")
	require.Nil(t, v.localSiteList(true, "10.0.0.5", log), "not this machine")
}
