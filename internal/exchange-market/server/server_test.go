package server_test

import (
	"math"
	"net"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/skycoin/skywire/internal/exchange-client/market"
	"github.com/skycoin/skywire/internal/exchange-market/db"
	"github.com/skycoin/skywire/internal/exchange-market/protocol"
	"github.com/skycoin/skywire/internal/exchange-market/server"
)

// Valid mainnet payout addresses (SKY base58check, BTC P2PKH) — registration
// now validates addresses, so tests use real ones. Generated deterministically;
// see internal/exchange-market/walletaddr for the format rules.
const (
	skySeller = "FbmPhy5bhsMX8JNeAQdQ4W3DeCYFH97FBg"
	skyBuyer  = "23Lqf6WpmaiFdzr4g5gerqUBu8H7SyeDHJU"
	skyBuyer2 = "2joBpuNB6z3MReAjzQwGdxphxD5BmbrGV1k"
	skyOther  = "xPHfK6xn5LZvWAPB5EV6A62WyzQLywci1J"
	btcSeller = "19wiPpvWPxHEmcANKzwUjiRgABQk8wZzCp"
	btcBuyer  = "15rB1CxysqYZ6soyEoVpiUcz7Sb2yJwpS2"
	btcBuyer2 = "17qzzrRtvo6FYVHiN2xdtyrWXBEhujfAmN"
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
		WalletSKY: skySeller, WalletBTC: btcSeller,
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
	// The seller deposits the exact round amount (identified by sender + window).
	if listing.ExpectedAmountSKY != 10 || listing.MarketWallet != "sky-market-wallet" {
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
		WalletSKY: skyBuyer, WalletBTC: btcBuyer,
	})

	var products protocol.GetProductsResponse
	bindSuccess(t, buyer, protocol.TypeGetProducts, nil, &products)
	if len(products.Products) != 1 || products.Products[0].ID != "prod-1" {
		t.Fatalf("get_products = %+v, want the one seeded product", products.Products)
	}

	var buy protocol.BuyProductResponse
	bindSuccess(t, buyer, protocol.TypeBuyProduct, protocol.BuyProductRequest{ProductID: "prod-1"}, &buy)
	if buy.SellerWallet != btcSeller || buy.ExpectedPaymentAmount <= 2 {
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

// TestOnePendingListingPerSeller verifies a seller sends the exact round deposit
// amount and can only have one pending listing at a time — a second is rejected
// while the first is still awaiting its deposit. This keeps a single deposit from
// the seller's address unambiguous within one listing window.
func TestOnePendingListingPerSeller(t *testing.T) {
	database := newTestDB(t)
	if err := database.SetConfig("explorer_btc_provider", "esplora"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetConfig("wallet_sky", "sky-market-wallet"); err != nil {
		t.Fatal(err)
	}

	const sellerPK = "0311111111111111111111111111111111111111111111111111111111111111aa"
	seller := dialTestServer(t, database, sellerPK)
	mustSuccess(t, seller, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: skySeller, WalletBTC: btcSeller})

	var a protocol.CreateListingResponse
	req := protocol.CreateListingRequest{AmountSKY: 10, Price: 2, PaymentCurrency: "BTC"}
	bindSuccess(t, seller, protocol.TypeCreateListing, req, &a)
	if a.ExpectedAmountSKY != 10 {
		t.Fatalf("deposit amount = %v, want the exact round amount 10", a.ExpectedAmountSKY)
	}

	// A second pending listing from the same seller is rejected.
	second, err := seller.Do(protocol.TypeCreateListing, req)
	if err != nil {
		t.Fatalf("second create transport error: %v", err)
	}
	if !second.IsError() || errCode(t, second) != protocol.CodeInvalidRequest {
		t.Fatalf("second listing = %+v, want INVALID_REQUEST (one pending listing per seller)", second)
	}
}

// TestRegisterRejectsBadWallet verifies registration validates payout address
// format: a malformed BTC address (or SKY address) is rejected up front.
func TestRegisterRejectsBadWallet(t *testing.T) {
	database := newTestDB(t)
	const pk = "0311111111111111111111111111111111111111111111111111111111111111aa"
	c := dialTestServer(t, database, pk)

	// Valid SKY but a malformed BTC address.
	badBTC, err := c.Do(protocol.TypeRegister, protocol.RegisterRequest{
		WalletSKY: skySeller, WalletBTC: "bc1-not-a-real-address",
	})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !badBTC.IsError() || errCode(t, badBTC) != protocol.CodeInvalidWallet {
		t.Fatalf("register(bad BTC) = %+v, want INVALID_WALLET", badBTC)
	}

	// Malformed SKY address.
	badSKY, err := c.Do(protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: "not-a-sky-address"})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !badSKY.IsError() || errCode(t, badSKY) != protocol.CodeInvalidWallet {
		t.Fatalf("register(bad SKY) = %+v, want INVALID_WALLET", badSKY)
	}

	// A fully valid registration still succeeds.
	mustSuccess(t, c, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: skySeller, WalletBTC: btcSeller})
}

// TestCreateListingPrecision verifies SKY amounts are normalized to the network's
// 3-decimal precision (so the seller can deposit the exact amount) and that out-
// of-range amounts are rejected.
func TestCreateListingPrecision(t *testing.T) {
	database := newTestDB(t)
	if err := database.SetConfig("explorer_btc_provider", "esplora"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetConfig("wallet_sky", "sky-market-wallet"); err != nil {
		t.Fatal(err)
	}
	const sellerPK = "0311111111111111111111111111111111111111111111111111111111111111aa"
	seller := dialTestServer(t, database, sellerPK)
	mustSuccess(t, seller, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: skySeller, WalletBTC: btcSeller})

	// A high-precision AmountSKY is rounded to 3 decimals; the deposit is that
	// exact round amount (no non-round delta).
	var l protocol.CreateListingResponse
	bindSuccess(t, seller, protocol.TypeCreateListing, protocol.CreateListingRequest{
		AmountSKY: 10.123456789, Price: 2, PaymentCurrency: "BTC",
	}, &l)
	if math.Abs(l.ExpectedAmountSKY-10.123) > 1e-9 {
		t.Fatalf("deposit %v, want normalized to 3 decimals (10.123)", l.ExpectedAmountSKY)
	}

	// An amount above the safe float range is rejected.
	tooBig, err := seller.Do(protocol.TypeCreateListing, protocol.CreateListingRequest{
		AmountSKY: 1e10, Price: 2, PaymentCurrency: "BTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !tooBig.IsError() || errCode(t, tooBig) != protocol.CodeInvalidRequest {
		t.Fatalf("too-large listing = %+v, want INVALID_REQUEST", tooBig)
	}

	// An amount that rounds to zero at SKY precision is rejected.
	tooSmall, err := seller.Do(protocol.TypeCreateListing, protocol.CreateListingRequest{
		AmountSKY: 1e-9, Price: 2, PaymentCurrency: "BTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !tooSmall.IsError() || errCode(t, tooSmall) != protocol.CodeInvalidRequest {
		t.Fatalf("too-small listing = %+v, want INVALID_REQUEST", tooSmall)
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
		WalletSKY: skySeller, WalletBTC: btcSeller,
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
	mustSuccess(t, other, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: skyOther})
	var theirs protocol.GetListingsResponse
	bindSuccess(t, other, protocol.TypeGetListings, nil, &theirs)
	if len(theirs.Listings) != 0 {
		t.Fatalf("other caller get_listings = %+v, want none", theirs.Listings)
	}
}

// TestBuyerCancelBlocksRebuy verifies a buyer can cancel their in-flight buy
// (releasing the product) but is then blocked from buying that same product
// again, while another buyer still can.
func TestBuyerCancelBlocksRebuy(t *testing.T) {
	database := newTestDB(t)
	if err := database.SetConfig("explorer_btc_provider", "esplora"); err != nil {
		t.Fatal(err)
	}

	const sellerPK = "0311111111111111111111111111111111111111111111111111111111111111aa"
	const buyerPK = "0322222222222222222222222222222222222222222222222222222222222222bb"
	const buyer2PK = "0333333333333333333333333333333333333333333333333333333333333333cc"

	seller := dialTestServer(t, database, sellerPK)
	mustSuccess(t, seller, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: skySeller, WalletBTC: btcSeller})
	if err := database.CreateProduct(&db.Product{
		ID: "prod-1", SellerPubKey: sellerPK, AmountSKY: 10, Price: 2, PaymentCurrency: "BTC", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}

	buyer := dialTestServer(t, database, buyerPK)
	mustSuccess(t, buyer, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: skyBuyer, WalletBTC: btcBuyer})

	var buy protocol.BuyProductResponse
	bindSuccess(t, buyer, protocol.TypeBuyProduct, protocol.BuyProductRequest{ProductID: "prod-1"}, &buy)

	// Cancel the in-flight order; the product returns to active.
	mustSuccess(t, buyer, protocol.TypeCancelOrder, protocol.CancelOrderRequest{OrderID: buy.OrderID})
	if p, _ := database.GetProduct("prod-1"); p == nil || p.Status != "active" { //nolint
		t.Fatalf("product after cancel = %+v, want active", p)
	}
	// A voluntary cancel counts as a freeze violation (toward the ban system).
	if n, _ := database.CountRecentViolations(buyerPK, time.Now().UTC().Add(-time.Hour)); n != 1 { //nolint
		t.Fatalf("violations after cancel = %d, want 1", n)
	}

	// The same buyer may not buy it again.
	reb, err := buyer.Do(protocol.TypeBuyProduct, protocol.BuyProductRequest{ProductID: "prod-1"})
	if err != nil {
		t.Fatalf("rebuy transport error: %v", err)
	}
	if !reb.IsError() || errCode(t, reb) != protocol.CodeBuyerBlocked {
		t.Fatalf("rebuy = %+v, want BUYER_BLOCKED", reb)
	}

	// A different buyer still can.
	buyer2 := dialTestServer(t, database, buyer2PK)
	mustSuccess(t, buyer2, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: skyBuyer2, WalletBTC: btcBuyer2})
	var buy2 protocol.BuyProductResponse
	bindSuccess(t, buyer2, protocol.TypeBuyProduct, protocol.BuyProductRequest{ProductID: "prod-1"}, &buy2)
	if buy2.OrderID == "" {
		t.Fatal("second buyer should be able to buy the released product")
	}
}

// TestSellerCancelConfirmedOffer verifies a seller can cancel a confirmed offer
// (deactivating the product and marking the listing canceled for refund), but
// not once a buyer has frozen it.
func TestSellerCancelConfirmedOffer(t *testing.T) {
	database := newTestDB(t)
	if err := database.SetConfig("explorer_btc_provider", "esplora"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetConfig("wallet_sky", "sky-market-wallet"); err != nil {
		t.Fatal(err)
	}

	const sellerPK = "0311111111111111111111111111111111111111111111111111111111111111aa"
	const buyerPK = "0322222222222222222222222222222222222222222222222222222222222222bb"

	seller := dialTestServer(t, database, sellerPK)
	mustSuccess(t, seller, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: skySeller, WalletBTC: btcSeller})

	var listing protocol.CreateListingResponse
	bindSuccess(t, seller, protocol.TypeCreateListing, protocol.CreateListingRequest{
		AmountSKY: 10, Price: 2, PaymentCurrency: "BTC",
	}, &listing)

	// Simulate the Listing Checker promoting the confirmed deposit into a product.
	if err := database.UpdatePendingListingStatus(listing.ListingID, "confirmed", "dep-tx"); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateProduct(&db.Product{
		ID: "prod-1", ListingID: listing.ListingID, SellerPubKey: sellerPK,
		AmountSKY: 10, Price: 2, PaymentCurrency: "BTC", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}

	// Cancel the confirmed offer: product deactivated, listing canceled.
	mustSuccess(t, seller, protocol.TypeCancelListing, protocol.CancelListingRequest{ListingID: listing.ListingID})
	if p, _ := database.GetProduct("prod-1"); p == nil || p.Status != "cancelled" { //nolint
		t.Fatalf("product after seller cancel = %+v, want canceled", p)
	}
	if l, _ := database.GetPendingListing(listing.ListingID); l == nil || l.Status != "canceled" { //nolint
		t.Fatalf("listing after seller cancel = %+v, want canceled", l)
	}

	// A second, frozen offer cannot be canceled.
	var listing2 protocol.CreateListingResponse
	bindSuccess(t, seller, protocol.TypeCreateListing, protocol.CreateListingRequest{
		AmountSKY: 5, Price: 1, PaymentCurrency: "BTC",
	}, &listing2)
	if err := database.UpdatePendingListingStatus(listing2.ListingID, "confirmed", "dep-tx-2"); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateProduct(&db.Product{
		ID: "prod-2", ListingID: listing2.ListingID, SellerPubKey: sellerPK,
		AmountSKY: 5, Price: 1, PaymentCurrency: "BTC", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.FreezeProduct("prod-2", buyerPK); err != nil {
		t.Fatal(err)
	}
	resp, err := seller.Do(protocol.TypeCancelListing, protocol.CancelListingRequest{ListingID: listing2.ListingID})
	if err != nil {
		t.Fatalf("cancel frozen transport error: %v", err)
	}
	if !resp.IsError() || errCode(t, resp) != protocol.CodeProductUnavailable {
		t.Fatalf("cancel of frozen offer = %+v, want PRODUCT_UNAVAILABLE", resp)
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
