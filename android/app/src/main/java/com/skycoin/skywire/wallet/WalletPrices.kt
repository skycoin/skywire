package com.skycoin.skywire.wallet

import android.content.Context
import android.content.SharedPreferences
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
 * coins it holds. A price is asked for every ten minutes, and the last is stored.
 */
object WalletPrices {
    /** The AppPreferences key of the on switch; off by default. */
    const val PREF_KEY = "wallet_usd_prices"

    /** A quote older than this is shown with its time and converts no amounts. */
    const val FRESH_MS = 60 * 60 * 1000L

    /** One coin in US dollars at the time [at], epoch ms, the service answered. */
    data class Quote(val usd: Double, val at: Long)

    private const val TTL_MS = 10 * 60 * 1000L
    private const val STORE = "wallet_prices"
    private val cache = ConcurrentHashMap<String, Quote>()

    @Volatile
    private var store: SharedPreferences? = null
    private val client = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .build()

    /** Where the last quotes are kept across restarts. */
    fun open(context: Context) {
        if (store == null) store = context.applicationContext.getSharedPreferences(STORE, Context.MODE_PRIVATE)
    }

    /** The last price known for [priceId], from this run or an earlier one. */
    fun last(priceId: String): Quote? =
        cache[priceId] ?: store?.getString(priceId, null)?.let(::decode)?.also { cache.putIfAbsent(priceId, it) }

    suspend fun usd(priceId: String): Double = withContext(Dispatchers.IO) {
        last(priceId)?.takeIf { System.currentTimeMillis() - it.at < TTL_MS }?.let { return@withContext it.usd }
        val req = Request.Builder().url("https://api.coinpaprika.com/v1/tickers/$priceId?quotes=USD").build()
        val price = client.newCall(req).execute().use { resp ->
            if (!resp.isSuccessful) throw IOException("price service answered HTTP ${resp.code}")
            parse(resp.body.string())
        }
        val quote = Quote(price, System.currentTimeMillis())
        cache[priceId] = quote
        store?.edit()?.putString(priceId, encode(quote))?.apply()
        price
    }

    internal fun parse(body: String): Double =
        Json.parseToJsonElement(body).jsonObject["quotes"]!!.jsonObject["USD"]!!.jsonObject["price"]!!.jsonPrimitive.double

    internal fun encode(q: Quote): String = "${q.usd}|${q.at}"

    internal fun decode(s: String): Quote? {
        val usd = s.substringBefore('|').toDoubleOrNull()?.takeIf { it > 0.0 && it.isFinite() } ?: return null
        val at = s.substringAfter('|', "").toLongOrNull()?.takeIf { it > 0 } ?: return null
        return Quote(usd, at)
    }
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
