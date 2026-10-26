package com.skycoin.skywire

import com.skycoin.skywire.ui.wallet.paymentRequest
import com.skycoin.skywire.ui.wallet.readScan
import com.skycoin.skywire.wallet.CoinSpec
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** Payment requests on Receive, and what a scanned or pasted code fills on Send. */
class WalletPaymentTest {

    private val sky = "2iNNt6fm9LszSWe51693BeyNUKX34pPaLx8"
    private val eth = "0xfb6916095ca1df60bb79Ce92cE3Ea74c37c5d359"
    private val usdt = CoinSpec.USDT.contract!!

    @Test
    fun skyRequestCarriesAmountHoursAndMessage() {
        val r = CoinSpec.SKY.paymentRequest(sky, "1.5", "20", "lunch")
        assertEquals("skycoin:$sky?amount=1.5&hours=20&message=lunch", r.uri)
    }

    @Test
    fun nothingAskedIsTheBareAddress() {
        assertNull(CoinSpec.SKY.paymentRequest(sky, "", " ", "").uri)
    }

    @Test
    fun badValuesShowNoRequest() {
        // SKY carries six decimals; seven is not an amount on this chain.
        val r = CoinSpec.SKY.paymentRequest(sky, "1.0000001", "", "")
        assertTrue(r.badAmount)
        assertNull(r.uri)
        assertTrue(CoinSpec.SKY.paymentRequest(sky, "1", "2.5", "").badHours)
    }

    @Test
    fun etherAmountsAreWei() {
        assertEquals("ethereum:$eth?value=1500000000000000000", CoinSpec.ETH.paymentRequest(eth, "1.5", "", "x").uri)
    }

    @Test
    fun tokenRequestIsATransferToTheWallet() {
        assertEquals(
            "ethereum:$usdt/transfer?address=$eth&uint256=2500000",
            CoinSpec.USDT.paymentRequest(eth, "2.5", "", "").uri,
        )
    }

    @Test
    fun scanFillsAddressAmountAndHours() {
        val f = CoinSpec.SKY.readScan("skycoin:$sky?amount=1.250&hours=7")
        assertEquals(sky, f.address)
        assertEquals("1.25", f.amountText)
        assertEquals("7", f.hoursText)
    }

    @Test
    fun scanDropsAnAmountTooFineForTheCoin() {
        assertNull(CoinSpec.SKY.readScan("skycoin:$sky?amount=0.0000001").amountText)
    }

    @Test
    fun tokenTransferForThisTokenFillsTheRecipient() {
        val f = CoinSpec.USDT.readScan("ethereum:$usdt@1/transfer?address=$eth&uint256=2.5e6")
        assertEquals(eth, f.address)
        assertEquals("2.5", f.amountText)
    }

    // The contract is the URI's first address; filling it as the recipient loses the coins.
    @Test
    fun tokenTransferForAnotherCoinFillsNothing() {
        val other = "0x6B175474E89094C44Da98b954EedeAC495271d0F"
        val f = CoinSpec.USDT.readScan("ethereum:$other/transfer?address=$eth&uint256=1")
        assertTrue(f.wrongToken)
        assertNull(f.address)
        assertTrue(CoinSpec.ETH.readScan("ethereum:$usdt/transfer?address=$eth&uint256=1").wrongToken)
    }

    @Test
    fun etherAmountIsNotReadAsTokens() {
        val f = CoinSpec.USDT.readScan("ethereum:$eth?value=1e18")
        assertEquals(eth, f.address)
        assertNull(f.amountText)
        assertEquals("1", CoinSpec.ETH.readScan("ethereum:$eth?value=1e18").amountText)
    }
}
