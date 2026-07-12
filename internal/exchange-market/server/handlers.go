package server

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/internal/exchange-market/db"
	"github.com/skycoin/skywire/internal/exchange-market/protocol"
)

// Decimal precision per asset. SKY uses 6 (droplets); the supported external
// payment coins (BTC/BCH/LTC/DOGE/DASH) use 8.
const (
	skyDecimals     = 6
	paymentDecimals = 8
)

// Amount-collision tolerances: half the smallest unit of each asset, so two
// amounts are "the same" only if they round to the same on-chain value.
const (
	skyTol     = 0.5e-6
	paymentTol = 0.5e-8
)

// errNoUniqueAmount means the non-round amount space to a wallet is momentarily
// exhausted (too many concurrent pending payments of the same base amount).
var errNoUniqueAmount = errors.New("could not allocate a unique payment amount")

// allocUniqueAmount generates a non-round amount for base that is not already
// pending (per the exists check), retrying on collision. The caller must hold a
// lock spanning this call and the subsequent insert so the chosen amount can't
// be taken by a concurrent allocation.
func allocUniqueAmount(base float64, decimals int, exists func(float64) (bool, error)) (float64, error) {
	for i := 0; i < 100; i++ {
		amt := nonRound(base, decimals)
		dup, err := exists(amt)
		if err != nil {
			return 0, err
		}
		if !dup {
			return amt, nil
		}
	}
	return 0, errNoUniqueAmount
}

// success builds a success response, degrading to an internal error if the
// payload cannot be marshaled.
func success(id string, data any) protocol.Envelope {
	env, err := protocol.NewResponse(id, protocol.StatusSuccess, data)
	if err != nil {
		return protocol.ErrorResponse(id, protocol.CodeInternalError, "failed to encode response")
	}
	return env
}

// internal logs op with err and returns a generic internal-error response so
// implementation details never leak to clients.
func (s *Server) internal(id, op string, err error) protocol.Envelope {
	s.log.WithError(err).Errorf("exchange-market: %s failed", op)
	return protocol.ErrorResponse(id, protocol.CodeInternalError, "internal error")
}

// unfreeze best-effort returns a product to the market after a failed buy.
func (s *Server) unfreeze(productID string) {
	if err := s.db.UnfreezeProduct(productID); err != nil {
		s.log.WithError(err).Errorf("exchange-market: failed to unfreeze product %s", productID)
	}
}

// handleRegister upserts the caller's wallet addresses, keyed by the
// authenticated connection public key (never a self-reported field).
func (s *Server) handleRegister(pk string, req protocol.Envelope) protocol.Envelope {
	var r protocol.RegisterRequest
	if err := req.Bind(&r); err != nil {
		return protocol.ErrorResponse(req.ID, protocol.CodeInvalidRequest, "invalid register payload")
	}
	if r.WalletSKY == "" {
		return protocol.ErrorResponse(req.ID, protocol.CodeInvalidRequest, "wallet_sky is required")
	}
	u := &db.User{
		PubKey:           pk,
		WalletSKY:        r.WalletSKY,
		WalletBTC:        r.WalletBTC,
		WalletBCH:        r.WalletBCH,
		WalletLTC:        r.WalletLTC,
		WalletUSDT_ERC20: r.WalletUSDTERC20,
		WalletUSDT_TRC20: r.WalletUSDTTRC20,
	}
	if err := s.db.CreateUser(u); err != nil {
		return s.internal(req.ID, "register", err)
	}
	return success(req.ID, protocol.MessageData{Message: "User registered successfully"})
}

// handleGetCurrencies returns the payment currencies this market accepts, i.e.
// the ones whose explorer the operator has configured.
func (s *Server) handleGetCurrencies(req protocol.Envelope) protocol.Envelope {
	cur, err := s.db.AvailableCurrencies()
	if err != nil {
		return s.internal(req.ID, "get_currencies", err)
	}
	if cur == nil {
		cur = []string{}
	}
	return success(req.ID, protocol.GetCurrenciesResponse{Currencies: cur})
}

// handleGetProducts returns all active products.
func (s *Server) handleGetProducts(req protocol.Envelope) protocol.Envelope {
	products, err := s.db.GetActiveProducts()
	if err != nil {
		return s.internal(req.ID, "get_products", err)
	}
	views := make([]protocol.ProductView, 0, len(products))
	for _, p := range products {
		views = append(views, protocol.ProductView{
			ID:              p.ID,
			SellerPubKey:    p.SellerPubKey,
			AmountSKY:       p.AmountSKY,
			Price:           p.Price,
			PaymentCurrency: p.PaymentCurrency,
			CreatedAt:       p.CreatedAt.Format(time.RFC3339),
		})
	}
	return success(req.ID, protocol.GetProductsResponse{Products: views})
}

// handleCreateListing records a sell order awaiting the seller's SKY deposit to
// the market wallet, and returns the non-round deposit amount to transfer.
func (s *Server) handleCreateListing(pk string, req protocol.Envelope) protocol.Envelope {
	var r protocol.CreateListingRequest
	if err := req.Bind(&r); err != nil {
		return protocol.ErrorResponse(req.ID, protocol.CodeInvalidRequest, "invalid listing payload")
	}
	if r.AmountSKY <= 0 || r.Price <= 0 {
		return protocol.ErrorResponse(req.ID, protocol.CodeInvalidRequest, "amount_sky and price must be positive")
	}

	// The seller must be registered (wallet addresses on file).
	if u, err := s.db.GetUser(pk); err != nil {
		return s.internal(req.ID, "create_listing.get_user", err)
	} else if u == nil {
		return protocol.ErrorResponse(req.ID, protocol.CodeInvalidRequest, "register your wallets first")
	}

	// The chosen payment currency must be enabled at this market.
	avail, err := s.db.IsCurrencyAvailable(r.PaymentCurrency)
	if err != nil {
		return s.internal(req.ID, "create_listing.currency", err)
	}
	if !avail {
		return protocol.ErrorResponse(req.ID, protocol.CodeCurrencyUnavailable, "payment currency not available at this market: "+r.PaymentCurrency)
	}

	marketWallet, err := s.db.GetMarketWallet()
	if err != nil {
		return s.internal(req.ID, "create_listing.wallet", err)
	}
	if marketWallet == "" {
		return protocol.ErrorResponse(req.ID, protocol.CodeInternalError, "market wallet not configured")
	}

	expiry := s.listingExpiry()

	// Allocate a unique non-round deposit amount and insert atomically, so no two
	// pending listings share an amount to the (shared) market wallet.
	s.allocMu.Lock()
	expected, err := allocUniqueAmount(r.AmountSKY, skyDecimals, func(a float64) (bool, error) {
		return s.db.PendingListingAmountExists(a, skyTol)
	})
	if err != nil {
		s.allocMu.Unlock()
		return s.internal(req.ID, "create_listing.alloc", err)
	}
	listing := &db.PendingListing{
		ID:                uuid.NewString(),
		SellerPubKey:      pk,
		AmountSKY:         r.AmountSKY,
		ExpectedAmountSKY: expected,
		Price:             r.Price,
		PaymentCurrency:   r.PaymentCurrency,
		Status:            "pending",
		ExpiresAt:         time.Now().UTC().Add(expiry),
	}
	err = s.db.CreatePendingListing(listing)
	s.allocMu.Unlock()
	if err != nil {
		return s.internal(req.ID, "create_listing", err)
	}

	return success(req.ID, protocol.CreateListingResponse{
		ListingID:         listing.ID,
		ExpectedAmountSKY: listing.ExpectedAmountSKY,
		MarketWallet:      marketWallet,
		ExpiresAt:         listing.ExpiresAt.Format(time.RFC3339),
	})
}

// handleBuyProduct freezes a product for the buyer and opens a buy order,
// returning the non-round amount to pay the seller.
func (s *Server) handleBuyProduct(pk string, req protocol.Envelope) protocol.Envelope {
	var r protocol.BuyProductRequest
	if err := req.Bind(&r); err != nil || r.ProductID == "" {
		return protocol.ErrorResponse(req.ID, protocol.CodeInvalidRequest, "invalid buy payload")
	}

	banned, err := s.db.IsUserBanned(pk)
	if err != nil {
		return s.internal(req.ID, "buy.ban_check", err)
	}
	if banned {
		return protocol.ErrorResponse(req.ID, protocol.CodeUserBanned, "you are temporarily banned from buying")
	}

	// The buyer must be registered so we can deliver SKY to them later.
	if u, err := s.db.GetUser(pk); err != nil {
		return s.internal(req.ID, "buy.get_user", err)
	} else if u == nil {
		return protocol.ErrorResponse(req.ID, protocol.CodeInvalidRequest, "register your wallets first")
	}

	product, err := s.db.GetProduct(r.ProductID)
	if err != nil {
		return s.internal(req.ID, "buy.get_product", err)
	}
	if product == nil {
		return protocol.ErrorResponse(req.ID, protocol.CodeProductNotFound, "product not found")
	}
	if product.Status != "active" {
		return protocol.ErrorResponse(req.ID, protocol.CodeProductUnavailable, "this product is no longer available")
	}

	// A buyer who previously canceled (or let expire) an order for this product
	// may not buy it again.
	blocked, err := s.db.BuyerBlockedFromProduct(pk, product.ID)
	if err != nil {
		return s.internal(req.ID, "buy.block_check", err)
	}
	if blocked {
		return protocol.ErrorResponse(req.ID, protocol.CodeBuyerBlocked, "you can no longer buy this product")
	}

	// Resolve the seller's wallet for the product's payment currency.
	sellerWallet, err := s.db.GetUserWallet(product.SellerPubKey, product.PaymentCurrency)
	if err != nil {
		return s.internal(req.ID, "buy.seller_wallet", err)
	}
	if sellerWallet == "" {
		return protocol.ErrorResponse(req.ID, protocol.CodeInternalError, "seller wallet not configured")
	}

	// Freeze first: this atomically moves active->frozen and rejects a racing
	// second buyer (design §5 product freeze).
	if err := s.db.FreezeProduct(product.ID, pk); err != nil {
		return protocol.ErrorResponse(req.ID, protocol.CodeProductUnavailable, "this product is no longer available")
	}

	// Allocate a unique non-round payment amount to the seller's wallet and
	// insert the order atomically, so no two pending orders to that wallet share
	// an amount. Roll back the freeze on any failure.
	s.allocMu.Lock()
	expected, err := allocUniqueAmount(product.Price, paymentDecimals, func(a float64) (bool, error) {
		return s.db.OrderPaymentAmountExists(sellerWallet, product.PaymentCurrency, a, paymentTol)
	})
	if err != nil {
		s.allocMu.Unlock()
		s.unfreeze(product.ID)
		return s.internal(req.ID, "buy.alloc", err)
	}
	order := &db.Order{
		ID:                    uuid.NewString(),
		ProductID:             product.ID,
		BuyerPubKey:           pk,
		AmountSKY:             product.AmountSKY,
		Price:                 product.Price,
		PaymentCurrency:       product.PaymentCurrency,
		ExpectedPaymentAmount: expected,
		SellerWallet:          sellerWallet,
		Status:                "pending_payment",
		ExpiresAt:             time.Now().UTC().Add(s.orderExpiry()),
	}
	err = s.db.CreateOrder(order)
	s.allocMu.Unlock()
	if err != nil {
		s.unfreeze(product.ID)
		return s.internal(req.ID, "buy.create_order", err)
	}

	return success(req.ID, protocol.BuyProductResponse{
		OrderID:               order.ID,
		AmountSKY:             order.AmountSKY,
		Price:                 order.Price,
		PaymentCurrency:       order.PaymentCurrency,
		ExpectedPaymentAmount: order.ExpectedPaymentAmount,
		SellerWallet:          order.SellerWallet,
		ExpiresAt:             order.ExpiresAt.Format(time.RFC3339),
	})
}

// handleCancelListing cancels the caller's own sell offer. It works while the
// listing is still pending (no deposit yet — nothing to return) and after it has
// become an active product, provided no buyer has selected (frozen) it. For a
// confirmed listing the derived product is deactivated and the escrowed SKY is
// returned by the Return Scheduler (measured from when the SKY was deposited: if
// that was already longer than the return delay ago, it goes back on the next
// scheduler tick, otherwise once the delay elapses).
func (s *Server) handleCancelListing(pk string, req protocol.Envelope) protocol.Envelope {
	var r protocol.CancelListingRequest
	if err := req.Bind(&r); err != nil || r.ListingID == "" {
		return protocol.ErrorResponse(req.ID, protocol.CodeInvalidRequest, "invalid cancel payload")
	}

	listing, err := s.db.GetPendingListing(r.ListingID)
	if err != nil {
		return s.internal(req.ID, "cancel.get_listing", err)
	}
	// Treat "not yours" the same as "not found" so listing IDs aren't probeable.
	if listing == nil || listing.SellerPubKey != pk {
		return protocol.ErrorResponse(req.ID, protocol.CodeListingNotFound, "listing not found")
	}

	switch listing.Status {
	case "pending":
		// No deposit confirmed yet: just cancel. The Return Scheduler still
		// checks the chain in case a deposit lands late.
		if err := s.db.UpdatePendingListingStatus(listing.ID, "canceled", ""); err != nil {
			return s.internal(req.ID, "cancel.update", err)
		}
		return success(req.ID, protocol.MessageData{Message: "Listing canceled."})

	case "confirmed":
		// The deposit settled and became a product. It can only be canceled
		// while still active — once a buyer freezes or buys it, the seller is
		// committed to the trade.
		product, err := s.db.GetProductByListingID(listing.ID)
		if err != nil {
			return s.internal(req.ID, "cancel.get_product", err)
		}
		if product != nil && product.Status != "active" {
			return protocol.ErrorResponse(req.ID, protocol.CodeProductUnavailable, "a buyer has already selected this offer; it can no longer be canceled")
		}
		if product != nil {
			if err := s.db.MarkProductCancelled(product.ID); err != nil {
				return s.internal(req.ID, "cancel.deactivate_product", err)
			}
		}
		if err := s.db.UpdatePendingListingStatus(listing.ID, "canceled", listing.TxHash); err != nil {
			return s.internal(req.ID, "cancel.update", err)
		}
		return success(req.ID, protocol.MessageData{Message: "Offer canceled. Your SKY will be returned within 1 hour of the original deposit."})

	default:
		return protocol.ErrorResponse(req.ID, protocol.CodeInvalidRequest, "listing can no longer be canceled")
	}
}

// handleCancelOrder cancels the caller's own in-flight buy order before payment
// is observed, releasing the product back to the market. The buyer is then
// blocked from re-buying that same product (BuyerBlockedFromProduct).
func (s *Server) handleCancelOrder(pk string, req protocol.Envelope) protocol.Envelope {
	var r protocol.CancelOrderRequest
	if err := req.Bind(&r); err != nil || r.OrderID == "" {
		return protocol.ErrorResponse(req.ID, protocol.CodeInvalidRequest, "invalid cancel payload")
	}

	order, err := s.db.GetOrder(r.OrderID)
	if err != nil {
		return s.internal(req.ID, "cancel_order.get", err)
	}
	// Only the buyer on the order may cancel it; hide others as not-found.
	if order == nil || order.BuyerPubKey != pk {
		return protocol.ErrorResponse(req.ID, protocol.CodeOrderNotFound, "order not found")
	}
	// Only cancelable before payment is observed. Once paid/confirmed the
	// payment is in flight and the trade must settle.
	if order.Status != "pending_payment" {
		return protocol.ErrorResponse(req.ID, protocol.CodeInvalidRequest, "this order can no longer be canceled")
	}

	if err := s.db.MarkOrderCanceled(order.ID); err != nil {
		return s.internal(req.ID, "cancel_order.update", err)
	}
	// Release the product so other buyers can purchase it.
	s.unfreeze(order.ProductID)
	// A voluntary cancel counts as a freeze violation, same as letting the order
	// expire: a buyer who repeatedly freezes then backs out is banned by the Ban
	// Manager (design §5). Best-effort — the cancel itself already succeeded.
	if err := s.db.CreateFreezeViolation(order.BuyerPubKey, order.ID); err != nil {
		s.log.WithError(err).Errorf("exchange-market: failed to record freeze violation for %s", order.BuyerPubKey)
	}
	return success(req.ID, protocol.MessageData{Message: "Order canceled. You cannot buy this product again."})
}

// handleGetOrders returns the caller's buy and sell orders.
func (s *Server) handleGetOrders(pk string, req protocol.Envelope) protocol.Envelope {
	buys, err := s.db.GetBuyerOrders(pk)
	if err != nil {
		return s.internal(req.ID, "get_orders.buyer", err)
	}
	sells, err := s.db.GetSellerOrders(pk)
	if err != nil {
		return s.internal(req.ID, "get_orders.seller", err)
	}

	views := make([]protocol.OrderView, 0, len(buys)+len(sells))
	for _, o := range buys {
		views = append(views, orderView(o, "buy"))
	}
	for _, o := range sells {
		views = append(views, orderView(o, "sell"))
	}
	return success(req.ID, protocol.GetOrdersResponse{Orders: views})
}

// handleGetListings returns the caller's own pending sell listings so the seller
// can track each one's lifecycle: pending (awaiting the SKY deposit) -> confirmed
// (deposit detected on-chain; the listing is now a purchasable product). It also
// returns the market wallet so the UI can restate the deposit target for
// still-pending listings.
func (s *Server) handleGetListings(pk string, req protocol.Envelope) protocol.Envelope {
	listings, err := s.db.GetSellerPendingListings(pk)
	if err != nil {
		return s.internal(req.ID, "get_listings", err)
	}
	wallet, err := s.db.GetMarketWallet()
	if err != nil {
		return s.internal(req.ID, "get_listings.wallet", err)
	}
	views := make([]protocol.ListingView, 0, len(listings))
	for _, l := range listings {
		v := protocol.ListingView{
			ID:                l.ID,
			AmountSKY:         l.AmountSKY,
			ExpectedAmountSKY: l.ExpectedAmountSKY,
			Price:             l.Price,
			PaymentCurrency:   l.PaymentCurrency,
			Status:            l.Status,
			ExpiresAt:         l.ExpiresAt.Format(time.RFC3339),
			CreatedAt:         l.CreatedAt.Format(time.RFC3339),
			TxHash:            l.TxHash,
		}
		if l.ConfirmedAt != nil {
			v.ConfirmedAt = l.ConfirmedAt.Format(time.RFC3339)
		}
		views = append(views, v)
	}
	return success(req.ID, protocol.GetListingsResponse{Listings: views, MarketWallet: wallet})
}

// handleGetOrderStatus returns the live status of one of the caller's orders.
func (s *Server) handleGetOrderStatus(pk string, req protocol.Envelope) protocol.Envelope {
	var r protocol.GetOrderStatusRequest
	if err := req.Bind(&r); err != nil || r.OrderID == "" {
		return protocol.ErrorResponse(req.ID, protocol.CodeInvalidRequest, "invalid order-status payload")
	}
	order, err := s.db.GetOrder(r.OrderID)
	if err != nil {
		return s.internal(req.ID, "get_order_status", err)
	}
	// Only the buyer on the order may query it; hide others as not-found.
	if order == nil || order.BuyerPubKey != pk {
		return protocol.ErrorResponse(req.ID, protocol.CodeOrderNotFound, "order not found")
	}

	resp := protocol.GetOrderStatusResponse{
		OrderID:       order.ID,
		Status:        order.Status,
		Confirmations: order.Confirmations,
		PaymentTxHash: order.PaymentTxHash,
	}
	if order.PaidAt != nil {
		resp.PaidAt = order.PaidAt.Format(time.RFC3339)
	}
	return success(req.ID, resp)
}

// orderView projects a db.Order into the client-facing view with a buy/sell tag.
func orderView(o *db.Order, kind string) protocol.OrderView {
	return protocol.OrderView{
		ID:              o.ID,
		Type:            kind,
		ProductID:       o.ProductID,
		AmountSKY:       o.AmountSKY,
		Price:           o.Price,
		PaymentCurrency: o.PaymentCurrency,
		Status:          o.Status,
		ExpiresAt:       o.ExpiresAt.Format(time.RFC3339),
		CreatedAt:       o.CreatedAt.Format(time.RFC3339),
	}
}

func (s *Server) listingExpiry() time.Duration {
	if m, err := s.db.GetListingExpiryMinutes(); err == nil && m > 0 {
		return time.Duration(m) * time.Minute
	}
	return 15 * time.Minute
}

func (s *Server) orderExpiry() time.Duration {
	if m, err := s.db.GetOrderExpiryMinutes(); err == nil && m > 0 {
		return time.Duration(m) * time.Minute
	}
	return 15 * time.Minute
}

// nonRound returns base plus a small random delta at the asset's precision, so
// the on-chain amount is unique and precisely identifiable in the recipient
// wallet (design §1 non-round-number validation). decimals is the asset's
// precision (6 for SKY, 8 for the payment coins); the result is rounded to it so
// it is always a valid on-chain amount.
func nonRound(base float64, decimals int) float64 {
	scale := math.Pow(10, float64(decimals))
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return math.Round(base*scale) / scale
	}
	// 1..1000 least-significant units of delta.
	n := binary.BigEndian.Uint32(b[:])%1000 + 1
	return math.Round(base*scale+float64(n)) / scale
}
