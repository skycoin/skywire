package com.skycoin.skywire.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

// The market's payloads as skydex-client passes them through (skycoin
// src/skydex/protocol). Go leaves empty slices as null, so lists are nullable.

/** client.get_currencies: payment coins with an explorer, and the coins a seller may list. */
@Serializable
data class DexCurrencies(
    val currencies: List<String>? = null,
    @SerialName("sell_coins") val sellCoins: List<String>? = null,
    @SerialName("market_name") val marketName: String = "",
)

@Serializable
data class DexProduct(
    val id: String = "",
    @SerialName("seller_pubkey") val sellerPubKey: String = "",
    @SerialName("sell_coin") val sellCoin: String = "",
    val amount: Double = 0.0,
    val price: Double = 0.0,
    @SerialName("payment_currency") val paymentCurrency: String = "",
    @SerialName("created_at") val createdAt: String = "",
)

@Serializable
data class DexProducts(val products: List<DexProduct>? = null)

/**
 * An order as the market reports it. The last three fields are this phone's:
 * History keeps finished orders after the market purges them, tagged with the
 * market they came from, the same record the web page kept.
 */
@Serializable
data class DexOrder(
    val id: String = "",
    val type: String = "",
    @SerialName("product_id") val productId: String = "",
    @SerialName("sell_coin") val sellCoin: String = "",
    val amount: Double = 0.0,
    val price: Double = 0.0,
    @SerialName("payment_currency") val paymentCurrency: String = "",
    @SerialName("expected_payment_amount") val expectedPaymentAmount: Double = 0.0,
    @SerialName("seller_wallet") val sellerWallet: String = "",
    val status: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("market_pk") val marketPk: String = "",
    @SerialName("market_name") val marketName: String = "",
    @SerialName("saved_at") val savedAt: Long = 0,
)

@Serializable
data class DexOrders(val orders: List<DexOrder>? = null)

/** A seller's own listing: pending (awaiting the deposit), then confirmed. */
@Serializable
data class DexListing(
    val id: String = "",
    @SerialName("sell_coin") val sellCoin: String = "",
    val amount: Double = 0.0,
    @SerialName("expected_amount") val expectedAmount: Double = 0.0,
    val price: Double = 0.0,
    @SerialName("payment_currency") val paymentCurrency: String = "",
    val status: String = "",
    @SerialName("market_wallet") val marketWallet: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("confirmed_at") val confirmedAt: String = "",
    @SerialName("tx_hash") val txHash: String = "",
    @SerialName("product_status") val productStatus: String = "",
    val cancelable: Boolean = false,
)

@Serializable
data class DexListings(
    val listings: List<DexListing>? = null,
    @SerialName("market_wallet") val marketWallet: String = "",
)

@Serializable
data class DexOrderStatus(
    @SerialName("order_id") val orderId: String = "",
    val status: String = "",
    val confirmations: Int = 0,
    @SerialName("required_confirmations") val requiredConfirmations: Int = 0,
    @SerialName("payment_tx_hash") val paymentTxHash: String = "",
    @SerialName("paid_at") val paidAt: String = "",
)

/** client.create_listing: what to deposit, and where. */
@Serializable
data class DexCreatedListing(
    @SerialName("listing_id") val listingId: String = "",
    @SerialName("sell_coin") val sellCoin: String = "",
    val amount: Double = 0.0,
    val commission: Double = 0.0,
    @SerialName("expected_amount") val expectedAmount: Double = 0.0,
    @SerialName("market_wallet") val marketWallet: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
)

/** client.buy_product: the exact amount to pay the seller. */
@Serializable
data class DexPurchase(
    @SerialName("order_id") val orderId: String = "",
    @SerialName("sell_coin") val sellCoin: String = "",
    val amount: Double = 0.0,
    val price: Double = 0.0,
    @SerialName("payment_currency") val paymentCurrency: String = "",
    @SerialName("expected_payment_amount") val expectedPaymentAmount: Double = 0.0,
    @SerialName("seller_wallet") val sellerWallet: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
)

@Serializable
data class DexMessage(val message: String = "")

@Serializable
internal data class DexRegisterRequest(
    @SerialName("wallet_sky") val walletSky: String,
    val wallets: Map<String, String>,
)

@Serializable
internal data class DexListingRequest(
    @SerialName("sell_coin") val sellCoin: String,
    val amount: Double,
    val price: Double,
    @SerialName("payment_currency") val paymentCurrency: String,
)

/** A refusal from the market, or from skydex-client: the text is theirs, shown as is. */
class SkydexException(message: String, val code: String = "", val httpStatus: Int = 0) : java.io.IOException(message)
