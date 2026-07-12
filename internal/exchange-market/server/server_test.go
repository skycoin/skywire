package server_test

import (
	"net"
	"path/filepath"
	"slices"
	"testing"

	"github.com/skycoin/skywire/internal/exchange-client/market"
	"github.com/skycoin/skywire/internal/exchange-market/db"
	"github.com/skycoin/skywire/internal/exchange-market/protocol"
	"github.com/skycoin/skywire/internal/exchange-market/server"
)

// newTestDB spins up a real (temp-file) SQLite database with migrations and
// default config applied.
func newTestDB(t *testing.T) *db.Database {
	t.Helper()
	path := filepath.Join(t.TempDir(), "market.db")
	database, err := db.New(path, "")
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() }) //nolint
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := database.InitDefaultConfig(); err != nil {
		t.Fatalf("init config: %v", err)
	}
	return database
}

// dialTestServer wires a client Conn to a Server.Serve loop over net.Pipe,
// authenticating the client as buyerPK.
func dialTestServer(t *testing.T, database *db.Database, buyerPK string) *market.Conn {
	t.Helper()
	srv := server.New(database, nil, protocol.DefaultPort)
	clientEnd, serverEnd := net.Pipe()
	go srv.Serve(serverEnd, buyerPK)
	c := market.NewConn(clientEnd)
	t.Cleanup(func() { _ = c.Close() }) //nolint
	return c
}

// TestTradeRoundTrip drives register -> get_currencies -> create_listing ->
// get_products -> buy_product -> get_orders over the framed transport against a
// real database, verifying both the protocol and the market business logic.
func TestTradeRoundTrip(t *testing.T) {
	database := newTestDB(t)

	const sellerPK = "0311111111111111111111111111111111111111111111111111111111111111aa"
	const buyerPK = "0322222222222222222222222222222222222222222222222222222222222222bb"

	// Enable BTC as a payment currency, and set the market escrow wallet.
	if err := database.SetConfig("explorer_btc_provider", "esplora"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetConfig("wallet_sky", "sky-market-wallet"); err != nil {
		t.Fatal(err)
	}

	// --- seller registers and lists a product ---
	seller := dialTestServer(t, database, sellerPK)

	mustSuccess(t, seller, protocol.TypeRegister, protocol.RegisterRequest{
		WalletSKY: "sky-seller", WalletBTC: "bc1-seller",
	})

	// get_currencies should now include BTC (explorer configured) but not LTC.
	var cur protocol.GetCurrenciesResponse
	bindSuccess(t, seller, protocol.TypeGetCurrencies, nil, &cur)
	if !slices.Contains(cur.Currencies, "BTC") || slices.Contains(cur.Currencies, "LTC") {
		t.Fatalf("available currencies = %v, want BTC present and LTC absent", cur.Currencies)
	}

	// A currency without a configured explorer must be rejected.
	rejected, err := seller.Do(protocol.TypeCreateListing, protocol.CreateListingRequest{
		AmountSKY: 10, Price: 2, PaymentCurrency: "LTC",
	})
	if err != nil {
		t.Fatalf("create_listing(LTC) transport error: %v", err)
	}
	if !rejected.IsError() || errCode(t, rejected) != protocol.CodeCurrencyUnavailable {
		t.Fatalf("create_listing(LTC) = %+v, want CURRENCY_UNAVAILABLE", rejected)
	}

	var listing protocol.CreateListingResponse
	bindSuccess(t, seller, protocol.TypeCreateListing, protocol.CreateListingRequest{
		AmountSKY: 10, Price: 2, PaymentCurrency: "BTC",
	}, &listing)
	if listing.ExpectedAmountSKY <= 10 || listing.MarketWallet != "sky-market-wallet" {
		t.Fatalf("unexpected listing response: %+v", listing)
	}

	// The Listing Checker job (not part of this phase) would normally promote a
	// confirmed listing into products. Simulate that so we can exercise buying.
	activeProduct := &db.Product{
		ID: "prod-1", SellerPubKey: sellerPK, AmountSKY: 10, Price: 2,
		PaymentCurrency: "BTC", Status: "active",
	}
	if err := database.CreateProduct(activeProduct); err != nil {
		t.Fatalf("seed product: %v", err)
	}

	// --- buyer registers, sees the product, and buys it ---
	buyer := dialTestServer(t, database, buyerPK)
	mustSuccess(t, buyer, protocol.TypeRegister, protocol.RegisterRequest{
		WalletSKY: "sky-buyer", WalletBTC: "bc1-buyer",
	})

	var products protocol.GetProductsResponse
	bindSuccess(t, buyer, protocol.TypeGetProducts, nil, &products)
	if len(products.Products) != 1 || products.Products[0].ID != "prod-1" {
		t.Fatalf("get_products = %+v, want the one seeded product", products.Products)
	}

	var buy protocol.BuyProductResponse
	bindSuccess(t, buyer, protocol.TypeBuyProduct, protocol.BuyProductRequest{ProductID: "prod-1"}, &buy)
	if buy.SellerWallet != "bc1-seller" || buy.ExpectedPaymentAmount <= 2 {
		t.Fatalf("unexpected buy response: %+v", buy)
	}

	// The product is now frozen: a second buy must be rejected.
	second, err := buyer.Do(protocol.TypeBuyProduct, protocol.BuyProductRequest{ProductID: "prod-1"})
	if err != nil {
		t.Fatalf("second buy transport error: %v", err)
	}
	if !second.IsError() || errCode(t, second) != protocol.CodeProductUnavailable {
		t.Fatalf("second buy = %+v, want PRODUCT_UNAVAILABLE", second)
	}

	// The buyer's order shows up in get_orders as a pending_payment buy.
	var orders protocol.GetOrdersResponse
	bindSuccess(t, buyer, protocol.TypeGetOrders, nil, &orders)
	if len(orders.Orders) != 1 || orders.Orders[0].Type != "buy" || orders.Orders[0].Status != "pending_payment" {
		t.Fatalf("get_orders = %+v, want one pending_payment buy", orders.Orders)
	}
}

// TestUniqueDepositAmounts verifies two listings of the same SKY amount get
// distinct non-round deposit amounts (uniqueness guarantee to the shared market
// wallet), so a deposit can only match one listing.
func TestUniqueDepositAmounts(t *testing.T) {
	database := newTestDB(t)
	if err := database.SetConfig("explorer_btc_provider", "esplora"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetConfig("wallet_sky", "sky-market-wallet"); err != nil {
		t.Fatal(err)
	}

	const sellerPK = "0311111111111111111111111111111111111111111111111111111111111111aa"
	seller := dialTestServer(t, database, sellerPK)
	mustSuccess(t, seller, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: "sky-seller", WalletBTC: "bc1"})

	var a, b protocol.CreateListingResponse
	req := protocol.CreateListingRequest{AmountSKY: 10, Price: 2, PaymentCurrency: "BTC"}
	bindSuccess(t, seller, protocol.TypeCreateListing, req, &a)
	bindSuccess(t, seller, protocol.TypeCreateListing, req, &b)

	if a.ExpectedAmountSKY == b.ExpectedAmountSKY {
		t.Fatalf("two listings got the same deposit amount %v — not unique", a.ExpectedAmountSKY)
	}
	if a.ExpectedAmountSKY <= 10 || b.ExpectedAmountSKY <= 10 {
		t.Fatalf("deposit amounts should be non-round above base: %v, %v", a.ExpectedAmountSKY, b.ExpectedAmountSKY)
	}
}

// TestUnknownMessageType verifies the dispatcher rejects unknown types cleanly.
// TestGetListings verifies a seller sees their own pending listings (with the
// deposit amount and market wallet) and their live status, and that another
// caller does not see them.
func TestGetListings(t *testing.T) {
	database := newTestDB(t)

	const sellerPK = "0311111111111111111111111111111111111111111111111111111111111111aa"
	const otherPK = "0322222222222222222222222222222222222222222222222222222222222222bb"

	if err := database.SetConfig("explorer_btc_provider", "esplora"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetConfig("wallet_sky", "sky-market-wallet"); err != nil {
		t.Fatal(err)
	}

	seller := dialTestServer(t, database, sellerPK)
	mustSuccess(t, seller, protocol.TypeRegister, protocol.RegisterRequest{
		WalletSKY: "sky-seller", WalletBTC: "bc1-seller",
	})

	var listing protocol.CreateListingResponse
	bindSuccess(t, seller, protocol.TypeCreateListing, protocol.CreateListingRequest{
		AmountSKY: 10, Price: 2, PaymentCurrency: "BTC",
	}, &listing)

	// The seller sees the pending listing with the deposit amount + wallet.
	var mine protocol.GetListingsResponse
	bindSuccess(t, seller, protocol.TypeGetListings, nil, &mine)
	if len(mine.Listings) != 1 {
		t.Fatalf("get_listings = %+v, want one pending listing", mine.Listings)
	}
	l := mine.Listings[0]
	if l.Status != "pending" || l.ExpectedAmountSKY != listing.ExpectedAmountSKY || mine.MarketWallet != "sky-market-wallet" {
		t.Fatalf("listing view = %+v (wallet %q), want pending with the deposit amount", l, mine.MarketWallet)
	}

	// Once the deposit is confirmed, the listing shows as confirmed.
	if err := database.UpdatePendingListingStatus(l.ID, "confirmed", "deposit-tx"); err != nil {
		t.Fatal(err)
	}
	bindSuccess(t, seller, protocol.TypeGetListings, nil, &mine)
	if len(mine.Listings) != 1 || mine.Listings[0].Status != "confirmed" || mine.Listings[0].TxHash != "deposit-tx" {
		t.Fatalf("after confirm, get_listings = %+v, want one confirmed listing with tx", mine.Listings)
	}

	// A different caller does not see the seller's listings.
	other := dialTestServer(t, database, otherPK)
	mustSuccess(t, other, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: "sky-other"})
	var theirs protocol.GetListingsResponse
	bindSuccess(t, other, protocol.TypeGetListings, nil, &theirs)
	if len(theirs.Listings) != 0 {
		t.Fatalf("other caller get_listings = %+v, want none", theirs.Listings)
	}
}

func TestUnknownMessageType(t *testing.T) {
	database := newTestDB(t)
	c := dialTestServer(t, database, "03deadbeef")
	resp, err := c.Do("client.nonsense", nil)
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !resp.IsError() || errCode(t, resp) != protocol.CodeInvalidRequest {
		t.Fatalf("resp = %+v, want INVALID_REQUEST", resp)
	}
}

// --- helpers ---

func mustSuccess(t *testing.T, c *market.Conn, msgType string, data any) protocol.Envelope {
	t.Helper()
	resp, err := c.Do(msgType, data)
	if err != nil {
		t.Fatalf("%s transport error: %v", msgType, err)
	}
	if resp.IsError() {
		t.Fatalf("%s failed: %s", msgType, errCode(t, resp))
	}
	return resp
}

func bindSuccess(t *testing.T, c *market.Conn, msgType string, data, out any) {
	t.Helper()
	resp := mustSuccess(t, c, msgType, data)
	if err := resp.Bind(out); err != nil {
		t.Fatalf("%s bind response: %v", msgType, err)
	}
}

func errCode(t *testing.T, env protocol.Envelope) string {
	t.Helper()
	var e protocol.ErrorData
	if err := env.Bind(&e); err != nil {
		t.Fatalf("bind error data: %v", err)
	}
	return e.Code
}
