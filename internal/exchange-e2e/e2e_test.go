// Package exchange_e2e drives a full exchange trade across a real in-process
// dmsg mesh: the market on one dmsg identity (visor-A), a seller and a buyer on
// two others (visor-B / visor-C). It exercises the actual wire path — dmsg
// Noise+yamux, the exchange length-prefixed framing, the request/response
// protocol, the server business logic, and the background-job lifecycle — end to
// end. The blockchain is a scripted stand-in (scriptChain); swapping it for a
// real Skycoin node + Esplora is the remaining infra-dependent step of the live
// E2E, but everything above the chain is validated here.
//
// Not run under -short (the in-process dmsg mesh adds a few seconds).
package exchange_e2e

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/internal/exchange-client/market"
	"github.com/skycoin/skywire/internal/exchange-market/db"
	"github.com/skycoin/skywire/internal/exchange-market/jobs"
	"github.com/skycoin/skywire/internal/exchange-market/protocol"
	"github.com/skycoin/skywire/internal/exchange-market/server"
	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
)

const marketPort = uint16(8050)

// Real mainnet payout addresses (SKY base58check, BTC P2PKH) — registration
// validates address format, so tests use well-formed ones.
const (
	skySeller = "FbmPhy5bhsMX8JNeAQdQ4W3DeCYFH97FBg"
	skyBuyer  = "23Lqf6WpmaiFdzr4g5gerqUBu8H7SyeDHJU"
	btcSeller = "19wiPpvWPxHEmcANKzwUjiRgABQk8wZzCp"
	btcBuyer  = "15rB1CxysqYZ6soyEoVpiUcz7Sb2yJwpS2"
)

// scriptChain is a jobs.Chain whose on-chain answers are armed by the test, so
// the deposit/payment/delivery lifecycle can be driven deterministically without
// a real node. Swap this for chain.New(...) against a real Skycoin node +
// Esplora to complete the fully-live E2E.
type scriptChain struct {
	mu      sync.Mutex
	deposit bool
	confs   int
	sends   []sendCall
}

type sendCall struct {
	addr string
	amt  float64
}

func (c *scriptChain) DepositConfirmed(string, string, float64, time.Time, time.Time) (bool, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deposit, "dep-tx", nil
}

func (c *scriptChain) PaymentConfirmations(string, string, string, float64, time.Time, time.Time) (int, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.confs, "pay-tx", nil
}

func (c *scriptChain) SendSKY(addr string, amt float64) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sends = append(c.sends, sendCall{addr, amt})
	return "send-tx", nil
}

func (c *scriptChain) EscrowBalance(string) (float64, error) { return 0, nil }

func (c *scriptChain) armDeposit() { c.mu.Lock(); c.deposit = true; c.mu.Unlock() }
func (c *scriptChain) armPayment(n int) {
	c.mu.Lock()
	c.confs = n
	c.mu.Unlock()
}
func (c *scriptChain) sendCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sends)
}
func (c *scriptChain) lastSend() sendCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.sends) == 0 {
		return sendCall{}
	}
	return c.sends[len(c.sends)-1]
}

// TestExchangeTradeOverDmsg runs list -> deposit -> buy -> pay -> confirm ->
// deliver across a real dmsg mesh with three identities.
func TestExchangeTradeOverDmsg(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test; skipped under -short")
	}

	pkMarket, skMarket := cipher.GenerateKeyPair()
	pkSeller, skSeller := cipher.GenerateKeyPair()
	pkBuyer, skBuyer := cipher.GenerateKeyPair()

	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	if err := env.Startup(dmsgtest.DefaultTimeout, 1, 0, &dmsg.Config{MinSessions: 1}); err != nil {
		t.Fatalf("dmsg env startup: %v", err)
	}
	t.Cleanup(env.Shutdown)

	dmsgMarket := mustClient(t, env, pkMarket, skMarket)
	dmsgSeller := mustClient(t, env, pkSeller, skSeller)
	dmsgBuyer := mustClient(t, env, pkBuyer, skBuyer)
	waitReady(t, dmsgMarket)
	waitReady(t, dmsgSeller)
	waitReady(t, dmsgBuyer)

	// --- market: real DB + server (over dmsg) + background jobs ---
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
	if err := database.SetConfig("explorer_btc_provider", "esplora"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetConfig("wallet_sky", "sky-market-wallet"); err != nil {
		t.Fatal(err)
	}
	// Disable the commission so the expected deposit equals the listed amount,
	// keeping the end-to-end amount assertions simple.
	if err := database.SetConfig("commission_rate_percent", "0"); err != nil {
		t.Fatal(err)
	}
	if err := database.SetConfig("commission_min_sky", "0"); err != nil {
		t.Fatal(err)
	}

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
			// The dmsg peer PK is authenticated by the Noise handshake; it is
			// the market's notion of the calling user's identity.
			go srv.Serve(stream, stream.RawRemoteAddr().PK.Hex())
		}
	}()

	chain := &scriptChain{}
	runner := jobs.NewRunner(database, chain, nil)

	seller := dialMarket(t, dmsgSeller, pkMarket)
	buyer := dialMarket(t, dmsgBuyer, pkMarket)

	// --- seller registers and lists 10 SKY for 2 BTC ---
	mustOK(t, seller, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: skySeller, WalletBTC: btcSeller})

	var listing protocol.CreateListingResponse
	bindOK(t, seller, protocol.TypeCreateListing, protocol.CreateListingRequest{
		AmountSKY: 10, Price: 2, PaymentCurrency: "BTC",
	}, &listing)
	// The seller deposits the exact round amount (identified by sender + window).
	if listing.ExpectedAmountSKY != 10 || listing.MarketWallet != "sky-market-wallet" {
		t.Fatalf("unexpected listing response: %+v", listing)
	}

	// The seller sees it pending until the deposit lands.
	var mine protocol.GetListingsResponse
	bindOK(t, seller, protocol.TypeGetListings, nil, &mine)
	if len(mine.Listings) != 1 || mine.Listings[0].Status != "pending" {
		t.Fatalf("get_listings = %+v, want one pending", mine.Listings)
	}

	// --- deposit confirms -> Listing Checker promotes to a product ---
	chain.armDeposit()
	if err := runner.RunListingCheck(); err != nil {
		t.Fatalf("RunListingCheck: %v", err)
	}
	bindOK(t, seller, protocol.TypeGetListings, nil, &mine)
	if len(mine.Listings) != 1 || mine.Listings[0].Status != "confirmed" {
		t.Fatalf("after deposit, get_listings = %+v, want one confirmed", mine.Listings)
	}

	// --- buyer registers, sees the product, and buys it ---
	mustOK(t, buyer, protocol.TypeRegister, protocol.RegisterRequest{WalletSKY: skyBuyer, WalletBTC: btcBuyer})

	var products protocol.GetProductsResponse
	bindOK(t, buyer, protocol.TypeGetProducts, nil, &products)
	if len(products.Products) != 1 || products.Products[0].AmountSKY != 10 {
		t.Fatalf("get_products = %+v, want the one listed product", products.Products)
	}
	productID := products.Products[0].ID

	var buy protocol.BuyProductResponse
	bindOK(t, buyer, protocol.TypeBuyProduct, protocol.BuyProductRequest{ProductID: productID}, &buy)
	if buy.SellerWallet != btcSeller || buy.ExpectedPaymentAmount <= 2 {
		t.Fatalf("unexpected buy response: %+v", buy)
	}

	// The buyer's live order status starts at pending_payment / 0 confs.
	var st protocol.GetOrderStatusResponse
	bindOK(t, buyer, protocol.TypeGetOrderStatus, protocol.GetOrderStatusRequest{OrderID: buy.OrderID}, &st)
	if st.Status != "pending_payment" || st.Confirmations != 0 {
		t.Fatalf("order status = %+v, want pending_payment / 0 confs", st)
	}

	// --- payment confirms -> Escrow Checker delivers SKY, completes trade ---
	chain.armPayment(jobs.RequiredConfirmations)
	if err := runner.RunEscrowCheck(); err != nil {
		t.Fatalf("RunEscrowCheck: %v", err)
	}

	bindOK(t, buyer, protocol.TypeGetOrderStatus, protocol.GetOrderStatusRequest{OrderID: buy.OrderID}, &st)
	if st.Status != "completed" {
		t.Fatalf("final order status = %+v, want completed", st)
	}
	if st.Confirmations < jobs.RequiredConfirmations {
		t.Fatalf("final confirmations = %d, want >= %d", st.Confirmations, jobs.RequiredConfirmations)
	}
	// SKY was delivered to the buyer's wallet.
	if chain.sendCount() != 1 {
		t.Fatalf("SendSKY calls = %d, want exactly one delivery", chain.sendCount())
	}
	if s := chain.lastSend(); s.addr != skyBuyer || s.amt != 10 {
		t.Fatalf("delivery = %+v, want 10 SKY to the buyer SKY wallet", s)
	}

	// The buyer's order also shows completed in get_orders.
	var orders protocol.GetOrdersResponse
	bindOK(t, buyer, protocol.TypeGetOrders, nil, &orders)
	if len(orders.Orders) != 1 || orders.Orders[0].Status != "completed" {
		t.Fatalf("get_orders = %+v, want one completed buy", orders.Orders)
	}
}

// --- helpers ---

func mustClient(t *testing.T, env *dmsgtest.Env, pk cipher.PubKey, sk cipher.SecKey) *dmsg.Client {
	t.Helper()
	c, err := env.NewClientWithKeys(pk, sk, &dmsg.Config{MinSessions: 1})
	if err != nil {
		t.Fatalf("dmsg NewClientWithKeys: %v", err)
	}
	return c
}

func waitReady(t *testing.T, c *dmsg.Client) {
	t.Helper()
	select {
	case <-c.Ready():
	case <-time.After(15 * time.Second):
		t.Fatalf("dmsg client %s not ready", c.LocalPK().Hex()[:8])
	}
}

// dialMarket dials the market's dmsg listener with a short retry (concurrent
// dmsg handshakes can transiently fail on a loaded runner) and wraps the stream
// in the exchange framing.
func dialMarket(t *testing.T, c *dmsg.Client, marketPK cipher.PubKey) *market.Conn {
	t.Helper()
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		raw, err := c.Dial(ctx, dmsg.Addr{PK: marketPK, Port: marketPort})
		cancel()
		if err == nil {
			conn := market.NewConn(raw)
			t.Cleanup(func() { _ = conn.Close() }) //nolint:errcheck
			return conn
		}
		lastErr = err
		time.Sleep(time.Duration(200+attempt*100) * time.Millisecond)
	}
	t.Fatalf("dial market over dmsg: %v", lastErr)
	return nil
}

func mustOK(t *testing.T, c *market.Conn, msgType string, data any) protocol.Envelope {
	t.Helper()
	resp, err := c.Do(msgType, data)
	if err != nil {
		t.Fatalf("%s transport error: %v", msgType, err)
	}
	if resp.IsError() {
		var e protocol.ErrorData
		_ = resp.Bind(&e) //nolint:errcheck
		t.Fatalf("%s failed: %s (%s)", msgType, e.Message, e.Code)
	}
	return resp
}

func bindOK(t *testing.T, c *market.Conn, msgType string, data, out any) {
	t.Helper()
	resp := mustOK(t, c, msgType, data)
	if err := resp.Bind(out); err != nil {
		t.Fatalf("%s bind response: %v", msgType, err)
	}
}

var _ net.Conn = (*dmsg.Stream)(nil) // the market serves over a dmsg stream (a net.Conn)
