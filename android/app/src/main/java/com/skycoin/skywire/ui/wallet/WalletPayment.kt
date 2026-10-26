package com.skycoin.skywire.ui.wallet

import com.skycoin.skywire.wallet.CoinKind
import com.skycoin.skywire.wallet.CoinSpec
import com.skycoin.wallet.Amounts
import com.skycoin.wallet.PaymentUri
import java.math.BigDecimal
import java.math.BigInteger

/** A payment request as typed on Receive. [uri] is null while nothing valid is asked. */
class PaymentRequest(val uri: String?, val badAmount: Boolean, val badHours: Boolean)

/** The longest message a request carries, as skycoin-web allows. */
const val REQUEST_MESSAGE_MAX = 100

fun CoinSpec.paymentRequest(address: String, amountText: String, hoursText: String, message: String): PaymentRequest {
    val typed = amountText.trim().takeIf { it.isNotEmpty() }
    val units = typed?.let { Amounts.parse(it, exponent) }?.takeIf { it > 0uL }
    val typedHours = hoursText.trim().takeIf { it.isNotEmpty() && kind == CoinKind.SKY_FIBER }
    val hours = typedHours?.toULongOrNull()?.takeIf { it > 0uL }
    val badAmount = typed != null && units == null
    val badHours = typedHours != null && hours == null
    // EIP-681 has no message field, so the Ethereum family carries only the amount.
    val note = message.trim().take(REQUEST_MESSAGE_MAX).takeIf { it.isNotEmpty() && !isEthFamily }
    if (badAmount || badHours || (units == null && hours == null && note == null)) {
        return PaymentRequest(null, badAmount, badHours)
    }
    val plain = units?.let { Amounts.format(it, exponent, 0).replace(",", "") }
    val uri = when (kind) {
        CoinKind.SKY_FIBER -> PaymentUri.fiber(PaymentUri.schemeFor(name), address, plain, hours, note)
        CoinKind.BTC -> PaymentUri.bitcoin(address, plain, note)
        // Wei, from gwei; a token's smallest unit, from the app's capped exponent.
        CoinKind.ETH -> PaymentUri.ether(address, units?.let { it.toBig() * BigInteger.TEN.pow(18 - exponent) })
        CoinKind.ERC20 -> PaymentUri.erc20(
            contract ?: return PaymentRequest(null, false, false),
            address,
            units?.let { it.toBig() * BigInteger.TEN.pow((tokenDecimals ?: exponent) - exponent) },
        )
    }
    return PaymentRequest(uri, false, false)
}

/** What a scanned code puts into the send form, or why it puts nothing. */
class ScanFill(
    val address: String?,
    val amountText: String?,
    val hoursText: String?,
    val wrongToken: Boolean = false,
)

/**
 * Reads a scanned code for this coin. A token transfer for another contract
 * fills nothing: its first address is the contract, and coins sent there are
 * lost. An amount finer than this coin can carry is dropped rather than rounded.
 */
fun CoinSpec.readScan(raw: String): ScanFill {
    val p = PaymentUri.parse(raw)
    if (p.contract != null) {
        val mine = kind == CoinKind.ERC20 && contract.equals(p.contract, ignoreCase = true)
        if (!mine) return ScanFill(null, null, null, wrongToken = true)
        val amount = p.tokenUnits?.toBigDecimal()?.movePointLeft(tokenDecimals ?: exponent)
        return ScanFill(p.address, amount?.let(::amountFor), null)
    }
    // An ether amount on a token's form would be read as tokens.
    val amount = p.amount.takeUnless { kind == CoinKind.ERC20 && p.scheme == "ethereum" }
    return ScanFill(
        address = p.address,
        amountText = amount?.let(::amountFor),
        hoursText = p.hours?.takeIf { kind == CoinKind.SKY_FIBER }?.toString(),
    )
}

private fun CoinSpec.amountFor(v: BigDecimal): String? {
    val plain = v.stripTrailingZeros()
    if (plain.signum() <= 0 || plain.scale() > exponent) return null
    return plain.toPlainString()
}

private fun ULong.toBig(): BigInteger = BigInteger(toString())
