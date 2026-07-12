// Package protocol is the single source of truth for the exchange wire
// protocol spoken between exchange-client and exchange-market over a Skywire
// dmsg transport.
//
// The transport itself is provided by Skywire: the market Listens on
// appnet.TypeDmsg and the client Dials the market's public key over the same
// network (see internal/exchange-market/server and internal/exchange-client/market).
// This package only defines what rides on that net.Conn: length-prefixed frames
// (reused from pkg/skychat/message) each carrying one JSON Envelope.
//
// Every exchange is request/response and correlated by Envelope.ID. The client
// sends a request Envelope and the market replies with a response Envelope
// (Type == TypeResponse) carrying the same ID.
//
// Identity: the market never trusts a public key carried in a payload. The
// authoritative user identity is the authenticated dmsg remote public key of
// the connection (conn.RemoteAddr().(appnet.Addr).PubKey). Payloads therefore
// carry no pubkey field — the server injects it from the transport.
package protocol

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/routing"
)

// Port is the Skywire routing port type, re-exported so callers can address
// the market without importing pkg/routing directly.
type Port = routing.Port

// DefaultPort is the routing port the market listens on for dmsg app
// connections, and the port the client dials on the market's public key.
// It mirrors skyenv.ExchangeMarketPort (kept as a literal here so this shared
// package has no dependency on pkg/skyenv).
const DefaultPort Port = 8050

// Request message types (client -> market).
const (
	TypeRegister       = "client.register"
	TypeGetCurrencies  = "client.get_currencies"
	TypeGetProducts    = "client.get_products"
	TypeCreateListing  = "client.create_listing"
	TypeBuyProduct     = "client.buy_product"
	TypeCancelListing  = "client.cancel_listing"
	TypeGetOrders      = "client.get_orders"
	TypeGetOrderStatus = "client.get_order_status"
)

// TypeResponse is the Type of every reply the market sends.
const TypeResponse = "response"

// Response status values.
const (
	StatusSuccess = "success"
	StatusError   = "error"
)

// Error codes (carried in ErrorData.Code on an error response).
const (
	CodeInvalidRequest      = "INVALID_REQUEST"
	CodeUserBanned          = "USER_BANNED"
	CodeProductNotFound     = "PRODUCT_NOT_FOUND"
	CodeProductUnavailable  = "PRODUCT_UNAVAILABLE"
	CodeListingNotFound     = "LISTING_NOT_FOUND"
	CodeOrderNotFound       = "ORDER_NOT_FOUND"
	CodeCurrencyUnavailable = "CURRENCY_UNAVAILABLE"
	CodeSessionConflict     = "SESSION_CONFLICT"
	CodeInternalError       = "INTERNAL_ERROR"
)

// PaymentCurrencies is the canonical set of payment currencies the market can
// verify (native coins looked up by address via the block explorer), in a
// stable display order. A market operator enables a subset of these; whether a
// given currency is actually available at a market is that enabled/disabled
// choice (see GetCurrenciesResponse and the per-coin explorer config).
//
// Tokens (USDT ERC-20/TRC-20) and privacy coins (XMR) are intentionally absent:
// tokens need per-contract explorer endpoints, and privacy coins cannot be
// verified by address at all.
var PaymentCurrencies = []string{"BTC", "BCH", "LTC", "DOGE", "DASH"}

// IsSupportedCurrency reports whether code is one of the canonical payment
// currencies the market can verify.
func IsSupportedCurrency(code string) bool {
	return slices.Contains(PaymentCurrencies, code)
}

// Envelope is the JSON message exchanged in each frame. For a request, Status
// is empty and Data holds the request payload. For a response, Type is
// TypeResponse, Status is "success" or "error", and Data holds the response
// payload (or an ErrorData on error).
type Envelope struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Timestamp int64           `json:"timestamp"`
	Status    string          `json:"status,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// NewRequest builds a request Envelope with a fresh correlation ID and the
// given payload marshaled into Data.
func NewRequest(msgType string, data any) (Envelope, error) {
	return newEnvelope(msgType, "", data)
}

// NewResponse builds a success/error response Envelope reusing the request's
// correlation ID.
func NewResponse(id, status string, data any) (Envelope, error) {
	env, err := newEnvelope(TypeResponse, status, data)
	if err != nil {
		return Envelope{}, err
	}
	env.ID = id
	return env, nil
}

// ErrorResponse is a convenience for building an error response envelope.
func ErrorResponse(id, code, message string) Envelope {
	// ErrorData marshaling cannot fail, so the error is safe to drop.
	env, _ := NewResponse(id, StatusError, ErrorData{Code: code, Message: message}) //nolint:errcheck
	return env
}

func newEnvelope(msgType, status string, data any) (Envelope, error) {
	env := Envelope{
		Type:      msgType,
		ID:        uuid.NewString(),
		Timestamp: time.Now().Unix(),
		Status:    status,
	}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return Envelope{}, err
		}
		env.Data = raw
	}
	return env, nil
}

// Bind unmarshals the envelope's Data into v.
func (e Envelope) Bind(v any) error {
	if len(e.Data) == 0 {
		return nil
	}
	return json.Unmarshal(e.Data, v)
}

// Marshal encodes the envelope as its JSON wire bytes (one frame payload).
func (e Envelope) Marshal() ([]byte, error) { return json.Marshal(e) }

// Unmarshal decodes a frame payload into an Envelope.
func Unmarshal(payload []byte) (Envelope, error) {
	var e Envelope
	err := json.Unmarshal(payload, &e)
	return e, err
}

// IsError reports whether the envelope is an error response.
func (e Envelope) IsError() bool { return e.Status == StatusError }

// --- Payloads ---

// ErrorData is the Data of an error response.
type ErrorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RegisterRequest registers/updates the caller's wallet addresses. The user is
// identified by the connection's public key, not by any field here.
type RegisterRequest struct {
	WalletSKY       string `json:"wallet_sky"`
	WalletBTC       string `json:"wallet_btc,omitempty"`
	WalletBCH       string `json:"wallet_bch,omitempty"`
	WalletLTC       string `json:"wallet_ltc,omitempty"`
	WalletUSDTERC20 string `json:"wallet_usdt_erc20,omitempty"`
	WalletUSDTTRC20 string `json:"wallet_usdt_trc20,omitempty"`
}

// MessageData is a simple human-readable message payload (generic success).
type MessageData struct {
	Message string `json:"message"`
}

// GetCurrenciesResponse lists the payment currencies this market currently
// accepts — i.e. the ones whose blockchain explorer the market operator has
// configured. A currency absent from this list cannot be used to create a
// listing or buy a product; the client hides it accordingly.
type GetCurrenciesResponse struct {
	Currencies []string `json:"currencies"`
}

// ProductView is a product as presented to clients (no internal freeze fields).
type ProductView struct {
	ID              string  `json:"id"`
	SellerPubKey    string  `json:"seller_pubkey"`
	AmountSKY       float64 `json:"amount_sky"`
	Price           float64 `json:"price"`
	PaymentCurrency string  `json:"payment_currency"`
	CreatedAt       string  `json:"created_at"`
}

// GetProductsResponse is the reply to TypeGetProducts.
type GetProductsResponse struct {
	Products []ProductView `json:"products"`
}

// CreateListingRequest creates a sell order.
type CreateListingRequest struct {
	AmountSKY       float64 `json:"amount_sky"`
	Price           float64 `json:"price"`
	PaymentCurrency string  `json:"payment_currency"`
}

// CreateListingResponse tells the seller the non-round amount to deposit.
type CreateListingResponse struct {
	ListingID         string  `json:"listing_id"`
	ExpectedAmountSKY float64 `json:"expected_amount_sky"`
	MarketWallet      string  `json:"market_wallet"`
	ExpiresAt         string  `json:"expires_at"`
}

// BuyProductRequest freezes and buys a product.
type BuyProductRequest struct {
	ProductID string `json:"product_id"`
}

// BuyProductResponse tells the buyer the non-round amount to pay.
type BuyProductResponse struct {
	OrderID               string  `json:"order_id"`
	AmountSKY             float64 `json:"amount_sky"`
	Price                 float64 `json:"price"`
	PaymentCurrency       string  `json:"payment_currency"`
	ExpectedPaymentAmount float64 `json:"expected_payment_amount"`
	SellerWallet          string  `json:"seller_wallet"`
	ExpiresAt             string  `json:"expires_at"`
}

// CancelListingRequest cancels a pending sell order.
type CancelListingRequest struct {
	ListingID string `json:"listing_id"`
}

// OrderView is an order as presented to clients.
type OrderView struct {
	ID              string  `json:"id"`
	Type            string  `json:"type"` // "buy" or "sell"
	ProductID       string  `json:"product_id"`
	AmountSKY       float64 `json:"amount_sky"`
	Price           float64 `json:"price"`
	PaymentCurrency string  `json:"payment_currency"`
	Status          string  `json:"status"`
	ExpiresAt       string  `json:"expires_at"`
	CreatedAt       string  `json:"created_at"`
}

// GetOrdersResponse is the reply to TypeGetOrders.
type GetOrdersResponse struct {
	Orders []OrderView `json:"orders"`
}

// GetOrderStatusRequest asks for one order's live status.
type GetOrderStatusRequest struct {
	OrderID string `json:"order_id"`
}

// GetOrderStatusResponse reports an order's live status.
type GetOrderStatusResponse struct {
	OrderID       string `json:"order_id"`
	Status        string `json:"status"`
	Confirmations int    `json:"confirmations"`
	PaymentTxHash string `json:"payment_tx_hash,omitempty"`
	PaidAt        string `json:"paid_at,omitempty"`
}
