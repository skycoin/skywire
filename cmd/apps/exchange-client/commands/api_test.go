package commands

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/skycoin/skywire/internal/exchange-market/db"
	"github.com/skycoin/skywire/internal/exchange-market/server"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
)

// fakeDialer stands in for the visor's app.Client: every Dial returns one end
// of a net.Pipe whose other end is served by a real market Server.
type fakeDialer struct{ srv *server.Server }

func (f fakeDialer) Dial(_ appnet.Addr) (net.Conn, error) {
	clientEnd, serverEnd := net.Pipe()
	go f.srv.Serve(serverEnd, "03aaaabbbbccccddddeeeeffff00001111222233334444555566667777888899aa")
	return clientEnd, nil
}

func newTestMarket(t *testing.T) *server.Server {
	t.Helper()
	database, err := db.New(filepath.Join(t.TempDir(), "m.db"), "")
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := database.InitDefaultConfig(); err != nil {
		t.Fatalf("init config: %v", err)
	}
	return server.New(database, nil, 0)
}

func newTestServer(t *testing.T, defaultPK string) *httptest.Server {
	t.Helper()
	sess := newSession(fakeDialer{srv: newTestMarket(t)}, defaultPK)
	mux := http.NewServeMux()
	registerAPI(mux, sess)
	ts := httptest.NewServer(mux)
	t.Cleanup(func() { ts.Close(); sess.close() })
	return ts
}

// TestConfigExposesDefaultPK verifies the --market-pk value reaches the UI.
func TestConfigExposesDefaultPK(t *testing.T) {
	ts := newTestServer(t, "the-default-pk")
	var out struct {
		MarketPK string `json:"market_pk"`
	}
	getJSON(t, ts.URL+"/api/config", &out)
	if out.MarketPK != "the-default-pk" {
		t.Fatalf("market_pk = %q, want the default", out.MarketPK)
	}
}

// TestConnectFlow drives status -> connect -> status -> disconnect through the
// HTTP API, exercising the real dmsg-style handshake against a market.
func TestConnectFlow(t *testing.T) {
	ts := newTestServer(t, "")

	// Initially disconnected.
	var st struct {
		Connected bool `json:"connected"`
	}
	getJSON(t, ts.URL+"/api/status", &st)
	if st.Connected {
		t.Fatal("expected disconnected initially")
	}

	// Connect with a valid market public key.
	pk, _ := cipher.GenerateKeyPair()
	var connResp struct {
		Connected bool     `json:"connected"`
		MarketPK  string   `json:"market_pk"`
		Curr      []string `json:"currencies"`
	}
	body, _ := json.Marshal(map[string]string{"market_pk": pk.Hex()})
	res, err := http.Post(ts.URL+"/api/connect", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST connect: %v", err)
	}
	decode(t, res, &connResp)
	if res.StatusCode != http.StatusOK || !connResp.Connected || connResp.MarketPK != pk.Hex() {
		t.Fatalf("connect resp = %+v (status %d), want connected", connResp, res.StatusCode)
	}

	// Status now reflects the connection.
	getJSON(t, ts.URL+"/api/status", &st)
	if !st.Connected {
		t.Fatal("expected connected after /api/connect")
	}

	// Disconnect.
	res, err = http.Post(ts.URL+"/api/disconnect", "application/json", nil)
	if err != nil {
		t.Fatalf("POST disconnect: %v", err)
	}
	res.Body.Close()
	getJSON(t, ts.URL+"/api/status", &st)
	if st.Connected {
		t.Fatal("expected disconnected after /api/disconnect")
	}
}

// TestConnectRejectsEmptyPK verifies connect fails cleanly with no key.
func TestConnectRejectsEmptyPK(t *testing.T) {
	ts := newTestServer(t, "")
	body, _ := json.Marshal(map[string]string{"market_pk": ""})
	res, err := http.Post(ts.URL+"/api/connect", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST connect: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK {
		t.Fatal("expected error status for empty market_pk")
	}
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	res, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	decode(t, res, out)
}

func decode(t *testing.T, res *http.Response, out any) {
	t.Helper()
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
