package jobs_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/skycoin/skywire/internal/exchange-market/db"
	"github.com/skycoin/skywire/internal/exchange-market/jobs"
)

// fakeChain is a controllable Chain for tests.
type fakeChain struct {
	deposit bool
	confs   int
	sends   []sendCall
}

type sendCall struct {
	addr string
	amt  float64
}

func (f *fakeChain) DepositConfirmed(string, float64) (bool, string, error) {
	return f.deposit, "deposit-tx", nil
}
func (f *fakeChain) PaymentConfirmations(string, string, float64) (int, string, error) {
	return f.confs, "pay-tx", nil
}
func (f *fakeChain) SendSKY(addr string, amt float64) (string, error) {
	f.sends = append(f.sends, sendCall{addr, amt})
	return "send-tx", nil
}

func newDB(t *testing.T) *db.Database {
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
	return database
}

func mustUser(t *testing.T, d *db.Database, pk, skyWallet string) {
	t.Helper()
	if err := d.CreateUser(&db.User{PubKey: pk, WalletSKY: skyWallet}); err != nil {
		t.Fatalf("create user: %v", err)
	}
}

// TestExpiry expires an overdue listing and an overdue order, releasing the
// product and recording a freeze violation.
func TestExpiry(t *testing.T) {
	d := newDB(t)
	r := jobs.NewRunner(d, &fakeChain{}, nil)

	const seller, buyer = "03seller", "03buyer"
	mustUser(t, d, seller, "sky-seller")
	mustUser(t, d, buyer, "sky-buyer")

	// Overdue pending listing.
	if err := d.CreatePendingListing(&db.PendingListing{
		ID: "l1", SellerPubKey: seller, AmountSKY: 5, ExpectedAmountSKY: 5.01, Price: 1,
		PaymentCurrency: "BTC", Status: "pending", ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	// Active product, frozen by the buyer, with an overdue order.
	if err := d.CreateProduct(&db.Product{
		ID: "p1", SellerPubKey: seller, AmountSKY: 5, Price: 1, PaymentCurrency: "BTC", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.FreezeProduct("p1", buyer); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateOrder(&db.Order{
		ID: "o1", ProductID: "p1", BuyerPubKey: buyer, AmountSKY: 5, Price: 1,
		PaymentCurrency: "BTC", ExpectedPaymentAmount: 1.01, SellerWallet: "bc1-seller",
		Status: "pending_payment", ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	if err := r.RunExpiry(); err != nil {
		t.Fatalf("RunExpiry: %v", err)
	}

	listing, _ := d.GetPendingListing("l1")
	if listing.Status != "expired" {
		t.Fatalf("listing status = %q, want expired", listing.Status)
	}
	order, _ := d.GetOrder("o1")
	if order.Status != "expired" {
		t.Fatalf("order status = %q, want expired", order.Status)
	}
	product, _ := d.GetProduct("p1")
	if product.Status != "active" {
		t.Fatalf("product status = %q, want active (released)", product.Status)
	}
	n, _ := d.CountRecentViolations(buyer, time.Now().UTC().Add(-time.Hour))
	if n != 1 {
		t.Fatalf("violations = %d, want 1", n)
	}
}

// TestListingCheck promotes a pending listing whose deposit is confirmed.
func TestListingCheck(t *testing.T) {
	d := newDB(t)
	if err := d.SetConfig("wallet_sky", "market-wallet"); err != nil {
		t.Fatal(err)
	}
	mustUser(t, d, "03seller", "sky-seller")
	if err := d.CreatePendingListing(&db.PendingListing{
		ID: "l1", SellerPubKey: "03seller", AmountSKY: 10, ExpectedAmountSKY: 10.02, Price: 2,
		PaymentCurrency: "BTC", Status: "pending", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	r := jobs.NewRunner(d, &fakeChain{deposit: true}, nil)
	if err := r.RunListingCheck(); err != nil {
		t.Fatalf("RunListingCheck: %v", err)
	}

	listing, _ := d.GetPendingListing("l1")
	if listing.Status != "confirmed" {
		t.Fatalf("listing status = %q, want confirmed", listing.Status)
	}
	products, _ := d.GetActiveProducts()
	if len(products) != 1 || products[0].AmountSKY != 10 {
		t.Fatalf("expected one active product from the listing, got %+v", products)
	}
}

// TestEscrowCheck completes an order once its payment reaches the threshold and
// delivers SKY to the buyer.
func TestEscrowCheck(t *testing.T) {
	d := newDB(t)
	mustUser(t, d, "03seller", "sky-seller")
	mustUser(t, d, "03buyer", "sky-buyer")
	if err := d.CreateProduct(&db.Product{
		ID: "p1", SellerPubKey: "03seller", AmountSKY: 7, Price: 2, PaymentCurrency: "BTC", Status: "frozen",
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateOrder(&db.Order{
		ID: "o1", ProductID: "p1", BuyerPubKey: "03buyer", AmountSKY: 7, Price: 2,
		PaymentCurrency: "BTC", ExpectedPaymentAmount: 2.01, SellerWallet: "bc1-seller",
		Status: "pending_payment", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	chain := &fakeChain{confs: 2}
	r := jobs.NewRunner(d, chain, nil)
	if err := r.RunEscrowCheck(); err != nil {
		t.Fatalf("RunEscrowCheck: %v", err)
	}

	order, _ := d.GetOrder("o1")
	if order.Status != "completed" {
		t.Fatalf("order status = %q, want completed", order.Status)
	}
	product, _ := d.GetProduct("p1")
	if product.Status != "sold" {
		t.Fatalf("product status = %q, want sold", product.Status)
	}
	if len(chain.sends) != 1 || chain.sends[0].addr != "sky-buyer" || chain.sends[0].amt != 7 {
		t.Fatalf("SendSKY calls = %+v, want one send of 7 to sky-buyer", chain.sends)
	}
}

// TestEscrowCheckNoopDefers verifies that without a chain backend the order is
// confirmed but not completed (delivery deferred).
func TestEscrowCheckNoopDefers(t *testing.T) {
	d := newDB(t)
	mustUser(t, d, "03seller", "sky-seller")
	mustUser(t, d, "03buyer", "sky-buyer")
	if err := d.CreateProduct(&db.Product{
		ID: "p1", SellerPubKey: "03seller", AmountSKY: 7, Price: 2, PaymentCurrency: "BTC", Status: "frozen",
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateOrder(&db.Order{
		ID: "o1", ProductID: "p1", BuyerPubKey: "03buyer", AmountSKY: 7, Price: 2,
		PaymentCurrency: "BTC", ExpectedPaymentAmount: 2.01, SellerWallet: "bc1-seller",
		Status: "pending_payment", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	// PaymentConfirmations reaches threshold, but NoopChain.SendSKY fails.
	r := jobs.NewRunner(d, noopButConfirmed{}, nil)
	if err := r.RunEscrowCheck(); err != nil {
		t.Fatalf("RunEscrowCheck: %v", err)
	}
	order, _ := d.GetOrder("o1")
	if order.Status != "confirmed" {
		t.Fatalf("order status = %q, want confirmed (delivery deferred)", order.Status)
	}
}

// noopButConfirmed confirms payment but cannot send (like a misconfigured chain).
type noopButConfirmed struct{ jobs.NoopChain }

func (noopButConfirmed) PaymentConfirmations(string, string, float64) (int, string, error) {
	return 2, "pay-tx", nil
}

// TestBanManager bans a user over the violation limit and lifts an expired ban.
func TestBanManager(t *testing.T) {
	d := newDB(t)
	mustUser(t, d, "03seller", "sky")
	mustUser(t, d, "03offender", "sky")

	// A product + order to satisfy the freeze_violations foreign keys.
	if err := d.CreateProduct(&db.Product{
		ID: "p1", SellerPubKey: "03seller", AmountSKY: 1, Price: 1, PaymentCurrency: "BTC", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateOrder(&db.Order{
		ID: "o1", ProductID: "p1", BuyerPubKey: "03offender", AmountSKY: 1, Price: 1,
		PaymentCurrency: "BTC", ExpectedPaymentAmount: 1.01, SellerWallet: "w",
		Status: "expired", ExpiresAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := d.CreateFreezeViolation("03offender", "o1"); err != nil {
			t.Fatal(err)
		}
	}

	// An already-expired ban that should be lifted.
	mustUser(t, d, "03old", "sky")
	if err := d.CreateBan("03old", 3, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	r := jobs.NewRunner(d, &fakeChain{}, nil)
	if err := r.RunBanManager(); err != nil {
		t.Fatalf("RunBanManager: %v", err)
	}

	banned, _ := d.IsUserBanned("03offender")
	if !banned {
		t.Fatal("offender should be banned after 3 violations")
	}
	stillBanned, _ := d.IsUserBanned("03old")
	if stillBanned {
		t.Fatal("expired ban should have been lifted")
	}
}

// TestCleanup deletes completed orders past the retention window.
func TestCleanup(t *testing.T) {
	d := newDB(t)
	if err := d.SetConfig("cleanup_days", "0"); err != nil { // retention 0 => delete once completed
		t.Fatal(err)
	}
	mustUser(t, d, "03seller", "sky")
	mustUser(t, d, "03buyer", "sky")
	if err := d.CreateProduct(&db.Product{
		ID: "p1", SellerPubKey: "03seller", AmountSKY: 1, Price: 1, PaymentCurrency: "BTC", Status: "sold",
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.CreateOrder(&db.Order{
		ID: "o1", ProductID: "p1", BuyerPubKey: "03buyer", AmountSKY: 1, Price: 1,
		PaymentCurrency: "BTC", ExpectedPaymentAmount: 1.01, SellerWallet: "w",
		Status: "completed", ExpiresAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := d.MarkOrderCompleted("o1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // ensure completed_at is strictly in the past

	r := jobs.NewRunner(d, &fakeChain{}, nil)
	if err := r.RunCleanup(); err != nil {
		t.Fatalf("RunCleanup: %v", err)
	}
	if o, _ := d.GetOrder("o1"); o != nil {
		t.Fatalf("completed order should have been cleaned up, got %+v", o)
	}
}
