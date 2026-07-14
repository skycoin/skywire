//go:build livenet

// Guided, full two-party live trade over a real in-process dmsg mesh against a
// real Skycoin node + Esplora. Unlike TestExchangeTradeOverDmsg (scripted chain),
// this wires the REAL chain backend and pauses for a human to make the on-chain
// SKY deposit and LTC payment manually, polling the market's jobs until each step
// settles. It reuses the mesh/helpers from e2e_test.go.
//
//	E2E_NODE_URL=https://node.skycoin.com \
//	E2E_ESCROW_SEED='...' E2E_ESCROW_ADDR=C3xi... \
//	E2E_SELLER_SKY=22Nn... E2E_SELLER_LTC=ltc1... \
//	E2E_BUYER_SKY=uV99... E2E_BUYER_LTC=ltc1... \
//	E2E_AMOUNT_SKY=0.5 E2E_PRICE_LTC=0.001 \
//	go test -tags livenet -run TestLiveExchangeTrade -v -timeout 40m ./internal/exchange-e2e/
package exchange_e2e

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/skycoin/skywire/internal/exchange-market/chain"
	"github.com/skycoin/skywire/internal/exchange-market/db"
	"github.com/skycoin/skywire/internal/exchange-market/jobs"
	"github.com/skycoin/skywire/internal/exchange-market/protocol"
	"github.com/skycoin/skywire/internal/exchange-market/server"
	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func TestLiveExchangeTrade(t *testing.T) {
	seed := os.Getenv("E2E_ESCROW_SEED")
	escrow := os.Getenv("E2E_ESCROW_ADDR")
	sellerSKY := os.Getenv("E2E_SELLER_SKY")
	sellerLTC := os.Getenv("E2E_SELLER_LTC")
	buyerSKY := os.Getenv("E2E_BUYER_SKY")
	buyerLTC := os.Getenv("E2E_BUYER_LTC")
	if seed == "" || escrow == "" || sellerSKY == "" || sellerLTC == "" || buyerSKY == "" || buyerLTC == "" {
		t.Skip("set E2E_ESCROW_SEED/ADDR, E2E_SELLER_SKY/LTC, E2E_BUYER_SKY/LTC to run the live trade")
	}
	nodeURL := getenv("E2E_NODE_URL", "https://node.skycoin.com")
	amount, err := strconv.ParseFloat(getenv("E2E_AMOUNT_SKY", "0.5"), 64)
	if err != nil {
		t.Fatalf("E2E_AMOUNT_SKY: %v", err)
	}
	price, err := strconv.ParseFloat(getenv("E2E_PRICE_LTC", "0.001"), 64)
	if err != nil {
		t.Fatalf("E2E_PRICE_LTC: %v", err)
	}
	// Confirmation depth for the run. Default 1 so the test doesn't stall waiting
	// for a second Skycoin block (Skycoin mints blocks only on activity); raise via
	// E2E_CONFIRMATIONS for a stricter run.
	confs, err := strconv.Atoi(getenv("E2E_CONFIRMATIONS", "1"))
	if err != nil || confs < 1 {
		t.Fatalf("E2E_CONFIRMATIONS: %v", err)
	}
	const cur = "LTC"

	// --- dmsg mesh + three identities (market, seller, buyer) ---
	pkMarket, skMarket := cipher.GenerateKeyPair()
	pkSeller, skSeller := cipher.GenerateKeyPair()
	pkBuyer, skBuyer := cipher.GenerateKeyPair()

	mesh := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	if err := mesh.Startup(dmsgtest.DefaultTimeout, 1, 0, &dmsg.Config{MinSessions: 1}); err != nil {
		t.Fatalf("dmsg env startup: %v", err)
	}
	t.Cleanup(mesh.Shutdown)
	dmsgMarket := mustClient(t, mesh, pkMarket, skMarket)
	dmsgSeller := mustClient(t, mesh, pkSeller, skSeller)
	dmsgBuyer := mustClient(t, mesh, pkBuyer, skBuyer)
	waitReady(t, dmsgMarket)
	waitReady(t, dmsgSeller)
	waitReady(t, dmsgBuyer)

	// --- market: real DB + REAL chain backend (Skycoin node + Esplora) ---
	database, err := db.New(filepath.Join(t.TempDir(), "market.db"), "")
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() }) //nolint:errcheck
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := database.InitDefaultConfig(); err != nil {
		t.Fatalf("init config: %v", err)
	}
	set := func(k, v string) {
		if err := database.SetConfig(k, v); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
	set("explorer_ltc_provider", "esplora") // litecoinspace default endpoint
	set("confirmations_required", strconv.Itoa(confs))
	// Wide windows so the manual deposit/payment (and their confirmations) always
	// fall inside the listing/order deposit window.
	set("listing_expiry_minutes", "180")
	set("order_expiry_minutes", "180")

	// Configure SKY as the sell coin: its own fullnode + escrow hot wallet.
	if err := database.UpsertSellCoin(&db.SellCoin{
		Symbol: "SKY", Name: "Skycoin", NodeURL: nodeURL,
		WalletSeed: seed, WalletAddr: escrow, Confirmations: confs, Enabled: true,
	}); err != nil {
		t.Fatalf("configure SKY sell coin: %v", err)
	}

	chainBackend := chain.New(database)
	if got := chainBackend; got == nil {
		t.Fatal("nil chain backend")
	}
	runner := jobs.NewRunner(database, chainBackend, nil)

	srv := server.New(database, nil, protocol.Port(marketPort))
	lis, err := dmsgMarket.Listen(marketPort)
	if err != nil {
		t.Fatalf("market Listen: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() }) //nolint:errcheck
	go func() {
		for {
			stream, err := lis.AcceptStream()
			if err != nil {
				return
			}
			go srv.Serve(stream, stream.RawRemoteAddr().PK.Hex())
		}
	}()

	seller := dialMarket(t, dmsgSeller, pkMarket)

	// --- seller registers and lists ---
	mustOK(t, seller, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: sellerSKY, WalletLTC: sellerLTC})
	var listing protocol.CreateListingResponse
	bindOK(t, seller, protocol.TypeCreateListing, protocol.CreateListingRequest{
		Amount: amount, Price: price, PaymentCurrency: cur,
	}, &listing)

	t.Logf("\n\n==================== ACTION 1 — DEPOSIT SKY ====================\n"+
		"  Send EXACTLY  %.3f SKY   (%.3f amount + %.3f commission)\n"+
		"  FROM  %s\n"+
		"  TO    %s\n"+
		"  (send from the seller SKY address above, so the sender matches)\n"+
		"  Waiting for the deposit to confirm on-chain...\n"+
		"================================================================\n",
		listing.ExpectedAmount, listing.Amount, listing.Commission, sellerSKY, escrow)

	pollUntil(t, 25*time.Minute, 15*time.Second, "SKY deposit", func() bool {
		if err := runner.RunListingCheck(); err != nil {
			t.Logf("listing-check: %v", err)
			return false
		}
		var m protocol.GetListingsResponse
		bindOK(t, seller, protocol.TypeGetListings, nil, &m)
		if len(m.Listings) == 1 {
			t.Logf("  listing status = %s", m.Listings[0].Status)
			return m.Listings[0].Status == "confirmed"
		}
		return false
	})
	t.Log("  ✓ DEPOSIT CONFIRMED — the listing is now a live product.")

	// --- buyer registers, sees the product, buys ---
	// Dial the buyer only now — its connection would otherwise sit idle through the
	// deposit wait and be dropped by the market's session idle-timeout.
	buyer := dialMarket(t, dmsgBuyer, pkMarket)
	mustOK(t, buyer, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: buyerSKY, WalletLTC: buyerLTC})
	var products protocol.GetProductsResponse
	bindOK(t, buyer, protocol.TypeGetProducts, nil, &products)
	if len(products.Products) == 0 {
		t.Fatal("no products visible to the buyer after deposit")
	}
	var buy protocol.BuyProductResponse
	bindOK(t, buyer, protocol.TypeBuyProduct, protocol.BuyProductRequest{ProductID: products.Products[0].ID}, &buy)

	t.Logf("\n\n==================== ACTION 2 — PAY LTC ====================\n"+
		"  Send EXACTLY  %.8f LTC\n"+
		"  FROM  %s\n"+
		"  TO    %s\n"+
		"  Waiting for %d confirmation(s), then the market delivers SKY...\n"+
		"===========================================================\n",
		buy.ExpectedPaymentAmount, buyerLTC, buy.SellerWallet, confs)

	pollUntil(t, 40*time.Minute, 20*time.Second, "LTC payment + SKY delivery", func() bool {
		if err := runner.RunEscrowCheck(); err != nil {
			t.Logf("escrow-check: %v", err)
			return false
		}
		var st protocol.GetOrderStatusResponse
		bindOK(t, buyer, protocol.TypeGetOrderStatus, protocol.GetOrderStatusRequest{OrderID: buy.OrderID}, &st)
		t.Logf("  order status = %-15s confirmations = %d/%d  tx=%s", st.Status, st.Confirmations, st.RequiredConfirmations, st.PaymentTxHash)
		return st.Status == "completed"
	})

	var st protocol.GetOrderStatusResponse
	bindOK(t, buyer, protocol.TypeGetOrderStatus, protocol.GetOrderStatusRequest{OrderID: buy.OrderID}, &st)
	t.Logf("\n\n==================== TRADE COMPLETE ====================\n"+
		"  order status   = %s\n"+
		"  confirmations  = %d\n"+
		"  LTC payment tx = %s\n"+
		"  %.3f SKY delivered to the buyer (%s); ~%.3f SKY commission retained in escrow.\n"+
		"=======================================================\n",
		st.Status, st.Confirmations, st.PaymentTxHash, listing.Amount, buyerSKY, listing.Commission)

	if bal, err := chainBackend.EscrowBalance("SKY", escrow); err == nil {
		t.Logf("  escrow balance now: %.6f SKY", bal)
	}
}

// pollUntil calls cond every interval until it returns true or timeout elapses.
func pollUntil(t *testing.T, timeout, interval time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(interval)
	}
}
