package com.skycoin.skywire.ui.dex

import kotlinx.serialization.Serializable
import java.math.BigDecimal
import java.util.Locale

/**
 * A market the user has connected to before, offered again in the recents
 * dropdown. The name is the operator's, learned from the connect handshake —
 * a 66-character key is not something anyone recognises in a list.
 */
@Serializable
data class SavedMarket(val pk: String, val name: String = "", val ts: Long = 0) {
    /** What the dropdown row reads as. */
    val label: String get() = if (name.isEmpty()) shortMarketPk(pk) else "$name · ${shortMarketPk(pk)}"
}

/**
 * The one skydex-client flag this screen owns: `--market-pk`.
 *
 * It is written back through the visor so the *config* records which market
 * this phone trades on — it survives a core restart and an app reinstall, and
 * it pre-fills the page's own connect form. It does not connect anything by
 * itself; see [com.skycoin.skywire.api.SkydexApi].
 *
 * Everything else in that argv — the listen address, the password file — is
 * the phone profile's ([com.skycoin.skywire.core.SkydexProfile]), pinned at
 * config time and not this screen's to touch.
 */
object DexArgs {

    // The visor writes the double-dash form; the single-dash spelling is
    // accepted too because a hand-edited config may carry it.
    private val MARKET_PK = listOf("--market-pk", "-market-pk")

    fun marketPk(args: List<String>): String? = value(args, MARKET_PK)?.takeIf { it.isNotEmpty() }

    /**
     * [args] rendered back as the single string the API's `args` field takes,
     * with `--market-pk` set to [pk]. Values are quoted only when they need
     * it — the server parses this with shell-like rules.
     */
    fun withMarketPk(args: List<String>, pk: String): String {
        val flag = args.indexOfFirst { token -> MARKET_PK.any { token == it || token.startsWith("$it=") } }
        val updated = when {
            flag < 0 -> args + listOf(MARKET_PK.first(), pk)
            args[flag].contains('=') -> args.toMutableList()
                .also { it[flag] = args[flag].substringBefore('=') + "=" + pk }
            flag + 1 < args.size -> args.toMutableList().also { it[flag + 1] = pk }
            // Trailing flag with no value: a broken argv the visor would
            // reject anyway — complete it rather than shifting everything.
            else -> args + pk
        }
        return updated.joinToString(" ") { token ->
            if (token.any { it.isWhitespace() }) "\"" + token.replace("\"", "\\\"") + "\"" else token
        }
    }

    /** Accepts both `--flag value` and `--flag=value`. */
    private fun value(args: List<String>, flags: List<String>): String? {
        args.forEachIndexed { i, token ->
            flags.forEach { flag ->
                if (token.startsWith("$flag=")) return token.substringAfter('=')
                if (token == flag && i + 1 < args.size) return args[i + 1]
            }
        }
        return null
    }
}

/**
 * Whether [value] is shaped like a market public key: 33 bytes of hex behind
 * a compressed-point prefix, which is what the client's `UnmarshalText`
 * accepts. Checked here so a typo is caught in the field instead of after a
 * dial that was never going to resolve.
 */
fun isMarketPk(value: String): Boolean =
    value.length == MARKET_PK_LENGTH &&
        (value.startsWith("02") || value.startsWith("03")) &&
        value.all { it.isDigit() || it in 'a'..'f' || it in 'A'..'F' }

private const val MARKET_PK_LENGTH = 66

/** 8…6, the same shortening the trading UI's own header uses. */
fun shortMarketPk(pk: String): String =
    if (pk.length <= 20) pk else pk.take(8) + "…" + pk.takeLast(6)

/**
 * A market amount the way the web page printed it: shortest decimal, never
 * exponent form, so 1e-7 reads 0.0000001 and 5.0 reads 5.
 */
fun dexNumber(value: Double): String =
    if (value.isNaN() || value.isInfinite()) "" else BigDecimal(value.toString()).stripTrailingZeros().toPlainString()

/**
 * The BIP-21 style link a wallet scans to fill in address and amount, or null
 * for a coin whose scheme is not known (QrButton.jsx buildUri). The amount is
 * plain decimal with up to 8 places: a wallet rejects "1e-7".
 */
fun paymentUri(address: String, amount: Double, coin: String): String? {
    val scheme = URI_SCHEMES[coin.uppercase(Locale.ROOT)] ?: return null
    if (address.isEmpty() || amount.isNaN() || amount <= 0) return null
    val plain = String.format(Locale.US, "%.8f", amount).trimEnd('0').trimEnd('.')
    return "$scheme:$address?amount=$plain"
}

/** Skycoin and the fibercoins share the address format and the skycoin: URI. */
private val URI_SCHEMES = mapOf(
    "BTC" to "bitcoin",
    "LTC" to "litecoin",
    "DOGE" to "dogecoin",
    "DASH" to "dash",
    "BCH" to "bitcoincash",
    "SKY" to "skycoin",
)

/** A decimal typed with either separator: decimal keyboards in es/fr offer a comma. */
fun parseDecimal(text: String): Double? = text.trim().replace(',', '.').toDoubleOrNull()?.takeIf { it.isFinite() }

/** Ids and hashes: the first eight characters, as the page's tables showed them. */
fun shortId(id: String): String = if (id.length <= 12) id else id.take(8) + "…"

/** A seller or market key in a card: 8…4. */
fun shortKey(pk: String): String = if (pk.length <= 14) pk else pk.take(8) + "…" + pk.takeLast(4)

/** Buy orders worth polling get_order_status for: still waiting on the chain. */
val IN_FLIGHT_STATUSES = setOf("pending_payment", "paid", "confirmed")

/** The market's own threshold is in each order status; this is for one that omits it. */
const val DEFAULT_REQUIRED_CONFIRMATIONS = 2
