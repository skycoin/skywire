package com.skycoin.skywire.wallet

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.double
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.IOException
import java.math.BigDecimal
import java.math.RoundingMode
import java.text.NumberFormat
import java.util.Currency
import java.util.Locale
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit

/**
 * USD prices from coinpaprika, as skycoin-web shows them. Off unless the user
 * turns them on: asking tells coinpaprika this device's address and which
 * coins it holds. Each price is kept for ten minutes, skycoin-web's interval.
 */
object WalletPrices {
    /** The AppPreferences key of the on switch; off by default. */
    const val PREF_KEY = "wallet_usd_prices"

    private const val TTL_MS = 10 * 60 * 1000L
    private val cache = ConcurrentHashMap<String, Pair<Double, Long>>()
    private val client = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .build()

    suspend fun usd(priceId: String): Double = withContext(Dispatchers.IO) {
        cache[priceId]?.takeIf { System.currentTimeMillis() - it.second < TTL_MS }?.let { return@withContext it.first }
        val req = Request.Builder().url("https://api.coinpaprika.com/v1/tickers/$priceId?quotes=USD").build()
        val price = client.newCall(req).execute().use { resp ->
            if (!resp.isSuccessful) throw IOException("price service answered HTTP ${resp.code}")
            parse(resp.body.string())
        }
        cache[priceId] = price to System.currentTimeMillis()
        price
    }

    internal fun parse(body: String): Double =
        Json.parseToJsonElement(body).jsonObject["quotes"]!!.jsonObject["USD"]!!.jsonObject["price"]!!.jsonPrimitive.double
}

/** [units] of this coin in USD at [price], as the user's locale writes money. */
fun CoinSpec.usdText(units: ULong, price: Double): String =
    usdFormat().format(BigDecimal(units.toString()).movePointLeft(exponent).multiply(BigDecimal.valueOf(price)))

/** [units] in USD as a plain number for an input field, cents rounded down: "12.34". */
fun CoinSpec.usdPlain(units: ULong, price: Double): String =
    BigDecimal(units.toString()).movePointLeft(exponent).multiply(BigDecimal.valueOf(price))
        .setScale(2, RoundingMode.DOWN).toPlainString()

/** A USD amount typed by the user, as this coin's amount at [price], or null when it is not a number. */
fun CoinSpec.coinTextFromUsd(usdText: String, price: Double): String? {
    if (price <= 0.0) return null
    val usd = usdText.trim().toBigDecimalOrNull()?.takeIf { it.signum() > 0 } ?: return null
    // Down, to the decimals the coin is shown with: never more than was asked for.
    return usd.divide(BigDecimal.valueOf(price), displayDecimals, RoundingMode.DOWN).stripTrailingZeros().toPlainString()
}

private fun usdFormat(): NumberFormat = NumberFormat.getCurrencyInstance(Locale.getDefault()).apply {
    currency = Currency.getInstance("USD")
}
