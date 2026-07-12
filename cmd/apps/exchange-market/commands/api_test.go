package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/skycoin/skywire/internal/exchange-market/app"
	"github.com/skycoin/skywire/internal/exchange-market/db"
)

func newTestOperatorServer(t *testing.T) (*httptest.Server, *db.Database) {
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

	mux := http.NewServeMux()
	// A zero app.Client is fine here: VisorPubKey() guards on a nil embed.
	registerOperatorAPI(mux, database, &app.Client{})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, database
}

// TestOperatorConfigReadWrite verifies config GET/POST, including the
// editable-key whitelist.
func TestOperatorConfigReadWrite(t *testing.T) {
	ts, _ := newTestOperatorServer(t)

	// GET returns the seeded defaults.
	var cfg map[string]string
	getJSONInto(t, ts.URL+"/api/config", &cfg)
	if _, ok := cfg["explorer_btc"]; !ok {
		t.Fatalf("config missing explorer_btc: %v", cfg)
	}

	// POST a valid update.
	body, _ := json.Marshal(map[string]string{"explorer_btc": "https://btc.example", "wallet_sky": "sky-w"})
	res, err := http.Post(ts.URL+"/api/config", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST config: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("POST config = %d, want 200", res.StatusCode)
	}
	getJSONInto(t, ts.URL+"/api/config", &cfg)
	if cfg["explorer_btc"] != "https://btc.example" || cfg["wallet_sky"] != "sky-w" {
		t.Fatalf("config not persisted: %v", cfg)
	}

	// POST an unknown key is rejected.
	bad, _ := json.Marshal(map[string]string{"evil_key": "x"})
	res, err = http.Post(ts.URL+"/api/config", "application/json", bytes.NewReader(bad))
	if err != nil {
		t.Fatalf("POST bad config: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown key = %d, want 400", res.StatusCode)
	}
}

// TestOperatorMonitoring verifies the monitoring endpoints return their arrays.
func TestOperatorMonitoring(t *testing.T) {
	ts, database := newTestOperatorServer(t)

	// Seed one product so /api/products is non-empty.
	if err := database.CreateUser(&db.User{PubKey: "03seller", WalletSKY: "sky"}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateProduct(&db.Product{
		ID: "p1", SellerPubKey: "03seller", AmountSKY: 5, Price: 1, PaymentCurrency: "BTC", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}

	var prods struct {
		Products []map[string]any `json:"products"`
	}
	getJSONInto(t, ts.URL+"/api/products", &prods)
	if len(prods.Products) != 1 || prods.Products[0]["id"] != "p1" {
		t.Fatalf("products = %+v, want p1", prods.Products)
	}

	for _, path := range []string{"/api/orders", "/api/bans"} {
		res, err := http.Get(ts.URL + path) //nolint:noctx
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, res.StatusCode)
		}
	}
}

// TestServeUIServesHTMLAndAPI runs the real serveUI (embedded HTML + operator
// API) and checks both surfaces respond.
func TestServeUIServesHTMLAndAPI(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const addr = "127.0.0.1:18050"
	go serveUI(ctx, &app.Client{}, database, addr)

	base := "http://" + addr
	var body []byte
	for i := 0; i < 40; i++ {
		res, err := http.Get(base + "/") //nolint:noctx
		if err == nil {
			body, _ = io.ReadAll(res.Body)
			res.Body.Close()
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !bytes.Contains(body, []byte("Operator")) {
		t.Fatalf("index.html not served (got %d bytes)", len(body))
	}

	// The API is mounted alongside the HTML.
	res, err := http.Get(base + "/api/config") //nolint:noctx
	if err != nil {
		t.Fatalf("GET /api/config: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/api/config = %d, want 200", res.StatusCode)
	}
}

func getJSONInto(t *testing.T, url string, out any) {
	t.Helper()
	res, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}
