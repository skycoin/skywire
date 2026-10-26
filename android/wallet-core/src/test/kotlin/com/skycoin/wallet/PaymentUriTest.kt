package com.skycoin.wallet

import java.math.BigDecimal
import java.math.BigInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class PaymentUriTest {

    private val sky = "2iNNt6fm9LszSWe51693BeyNUKX34pPaLx8"
    private val eth = "0xfb6916095ca1df60bb79Ce92cE3Ea74c37c5d359"
    private val usdt = "0xdAC17F958D2ee523a2206206994597C13D831ec7"

    @Test
    fun fiberRoundTrip() {
        val uri = PaymentUri.fiber("skycoin", sky, "1.5", 20u, "for lunch & tea")
        assertEquals("skycoin:$sky?amount=1.5&hours=20&message=for%20lunch%20%26%20tea", uri)
        val back = PaymentUri.parse(uri)
        assertEquals("skycoin", back.scheme)
        assertEquals(sky, back.address)
        assertEquals(BigDecimal("1.5"), back.amount)
        assertEquals(20uL, back.hours)
        assertEquals("for lunch & tea", back.message)
    }

    @Test
    fun emptyFieldsLeaveTheBareUri() {
        assertEquals("skycoin:$sky", PaymentUri.fiber("skycoin", sky, null, null, ""))
    }

    @Test
    fun schemeFromCoinName() {
        assertEquals("skycoin", PaymentUri.schemeFor("Skycoin"))
        assertEquals("mycoin", PaymentUri.schemeFor("My Coin!"))
        assertEquals("skycoin", PaymentUri.schemeFor("123"))
    }

    @Test
    fun bareAddressAndMalformedValues() {
        val bare = PaymentUri.parse("  $sky ")
        assertNull(bare.scheme)
        assertEquals(sky, bare.address)
        assertNull(bare.amount)
        val bad = PaymentUri.parse("skycoin://$sky?amount=1,5&hours=-2")
        assertEquals(sky, bad.address)
        assertNull(bad.amount)
        assertNull(bad.hours)
    }

    // BIP 21's own examples.
    @Test
    fun bitcoinBip21() {
        val p = PaymentUri.parse("bitcoin:175tWpb8K1S7NmH4Zx6rewF9WQrcZv245W?amount=20.3&label=Luke-Jr")
        assertEquals("175tWpb8K1S7NmH4Zx6rewF9WQrcZv245W", p.address)
        assertEquals(BigDecimal("20.3"), p.amount)
        assertEquals(
            "bitcoin:175tWpb8K1S7NmH4Zx6rewF9WQrcZv245W?amount=50&message=Donation%20for%20project%20xyz",
            PaymentUri.bitcoin("175tWpb8K1S7NmH4Zx6rewF9WQrcZv245W", "50", "Donation for project xyz"),
        )
    }

    // EIP-681's own examples, value in wei with an exponent.
    @Test
    fun etherValue() {
        val p = PaymentUri.parse("ethereum:$eth@1?value=2.014e18")
        assertEquals(eth, p.address)
        assertEquals(BigDecimal("2.014"), p.amount)
        assertNull(p.contract)
        assertEquals("ethereum:$eth?value=1500000000000000000", PaymentUri.ether(eth, BigInteger("1500000000000000000")))
        assertNull(PaymentUri.parse("ethereum:$eth?value=0.5").amount)
    }

    @Test
    fun tokenTransferNamesTheRecipientNotTheContract() {
        val uri = PaymentUri.erc20(usdt, eth, BigInteger("2500000"))
        assertEquals("ethereum:$usdt/transfer?address=$eth&uint256=2500000", uri)
        val p = PaymentUri.parse("ethereum:$usdt@1/transfer?address=$eth&uint256=2.5e6")
        assertEquals(eth, p.address)
        assertEquals(usdt, p.contract)
        assertEquals(BigInteger("2500000"), p.tokenUnits)
        assertNull(p.amount)
    }
}
