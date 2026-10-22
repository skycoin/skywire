package com.skycoin.wallet

import java.math.BigDecimal
import java.math.BigInteger
import java.net.URLDecoder
import java.net.URLEncoder

/**
 * Payment request URIs, written and read.
 *
 * Fiber chains use the skycoin-web form, `skycoin:ADDR?amount=1.5&hours=10&message=…`,
 * with the coin's own name as the scheme. Bitcoin is BIP 21. Ethereum is
 * EIP-681, whose amounts are integers in the chain's smallest unit: `value`
 * in wei for ether, and a `transfer` call with `uint256` for a token.
 */
object PaymentUri {

    class Scanned(
        val scheme: String?,
        /** For a token transfer, the recipient rather than the contract the URI names first. */
        val address: String,
        /** The coin amount as a decimal number of whole coins; null when none is asked. */
        val amount: BigDecimal?,
        val hours: ULong?,
        val message: String?,
        /** EIP-681 token transfer: the token's contract. Null for every other request. */
        val contract: String? = null,
        /** EIP-681 token transfer: the amount in the token's smallest unit. */
        val tokenUnits: BigInteger? = null,
    )

    /** A scheme from a coin's name: lowercase letters, digits and `+.-`, starting with a letter. */
    fun schemeFor(coinName: String): String {
        val s = coinName.lowercase().filter { it in 'a'..'z' || it in '0'..'9' || it in "+.-" }
        return if (s.firstOrNull()?.isLetter() == true) s else "skycoin"
    }

    fun fiber(scheme: String, address: String, amount: String?, hours: ULong?, message: String?): String =
        build(scheme, address, listOf("amount" to amount, "hours" to hours?.toString(), "message" to message))

    fun bitcoin(address: String, amount: String?, message: String?): String =
        build("bitcoin", address, listOf("amount" to amount, "message" to message))

    fun ether(address: String, wei: BigInteger?): String =
        build("ethereum", address, listOf("value" to wei?.toString()))

    fun erc20(contract: String, to: String, units: BigInteger?): String =
        build("ethereum", "$contract/transfer", listOf("address" to to, "uint256" to units?.toString()))

    private fun build(scheme: String, path: String, params: List<Pair<String, String?>>): String {
        val query = params.filter { !it.second.isNullOrEmpty() }
            .joinToString("&") { (k, v) -> "$k=${encode(v!!)}" }
        return if (query.isEmpty()) "$scheme:$path" else "$scheme:$path?$query"
    }

    // URLEncoder writes a space as "+", which BIP 21 readers take literally.
    private fun encode(v: String) = URLEncoder.encode(v, "UTF-8").replace("+", "%20")

    /**
     * Reads a scanned code: a bare address, or any of the URIs above. Values
     * that do not parse are dropped rather than guessed at, so the worst a
     * malformed code does is leave a field empty.
     */
    fun parse(text: String): Scanned {
        var rest = text.trim()
        val scheme = Regex("^([a-zA-Z][a-zA-Z0-9+.-]*):").find(rest)?.groupValues?.get(1)?.lowercase()
        if (scheme != null) rest = rest.substring(scheme.length + 1).removePrefix("//")
        val path = rest.substringBefore('?')
        val query = if ('?' in rest) rest.substringAfter('?') else ""
        val params = query.split('&').filter { '=' in it }.associate {
            it.substringBefore('=').lowercase() to decode(it.substringAfter('='))
        }
        // EIP-681: ADDRESS[@chain][/function].
        val target = path.substringBefore('/').substringBefore('@')
        val function = path.substringAfter('/', "").takeIf { it.isNotEmpty() }
        if (scheme == "ethereum") {
            if (function == "transfer") {
                return Scanned(
                    scheme = scheme,
                    address = params["address"].orEmpty(),
                    amount = null,
                    hours = null,
                    message = null,
                    contract = target,
                    tokenUnits = params["uint256"]?.let(::integer),
                )
            }
            val wei = params["value"]?.let(::integer)
            return Scanned(scheme, target, wei?.toBigDecimal()?.movePointLeft(18)?.stripTrailingZeros(), null, null)
        }
        return Scanned(
            scheme = scheme,
            address = target,
            amount = params["amount"]?.let(::decimal),
            hours = params["hours"]?.toULongOrNull(),
            message = params["message"]?.takeIf { it.isNotEmpty() },
        )
    }

    private fun decode(v: String): String = runCatching { URLDecoder.decode(v, "UTF-8") }.getOrDefault(v)

    private fun decimal(v: String): BigDecimal? =
        if (Regex("^\\d*\\.?\\d+$").matches(v)) BigDecimal(v) else null

    /** EIP-681 numbers may use an exponent ("1.5e18"); only whole results are amounts. */
    private fun integer(v: String): BigInteger? = runCatching {
        BigDecimal(v).takeIf { it.signum() >= 0 }?.toBigIntegerExact()
    }.getOrNull()
}
