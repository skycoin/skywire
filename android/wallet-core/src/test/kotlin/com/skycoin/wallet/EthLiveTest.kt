package com.skycoin.wallet

import com.skycoin.wallet.eth.EthWalletCore
import kotlinx.coroutines.runBlocking
import okhttp3.OkHttpClient
import org.junit.Assume.assumeTrue
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertTrue

/**
 * Live checks against the shipped Ethereum endpoints — parsing production
 * JSON-RPC and Blockscout answers, not a mock's idea of them. Off by
 * default (CI must not depend on the internet):
 * run with SKYWIRE_NET_TESTS=1 ./gradlew :wallet-core:test
 */
class EthLiveTest {

    // Publicly known, funded, with deep history on both APIs.
    private val richAddress = "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045"
    private val usdtContract = "0xdAC17F958D2ee523a2206206994597C13D831ec7"

    private val client = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build()

    private fun netTestsEnabled() = System.getenv("SKYWIRE_NET_TESTS") == "1"

    @Test
    fun nativeBalanceAndHistoryParseFromProduction() {
        assumeTrue(netTestsEnabled())
        runBlocking {
            val core = EthWalletCore(
                "https://ethereum-rpc.publicnode.com",
                "https://eth.blockscout.com",
                client,
            )
            val book = AddressBook(listOf(richAddress), emptyList())
            val balance = core.balance(book)
            assertTrue(balance.confirmed > 0uL, "known-funded address reads a balance")

            val history = core.history(book)
            assertTrue(history.isNotEmpty(), "known-active address has history")
            val tx = history.first { it.confirmed }
            assertTrue(tx.txid.startsWith("0x") && tx.txid.length == 66)
            assertTrue(tx.confirmations > 0)
            assertTrue(tx.timestamp > 1_400_000_000L)
        }
    }

    @Test
    fun tokenBalanceAndTransfersParseFromProduction() {
        assumeTrue(netTestsEnabled())
        runBlocking {
            val core = EthWalletCore(
                "https://ethereum-rpc.publicnode.com",
                "https://eth.blockscout.com",
                client,
                EthWalletCore.Erc20Token(usdtContract, decimals = 6),
            )
            val book = AddressBook(listOf(richAddress), emptyList())
            // The point is that balanceOf decodes and transfers parse — what
            // the address holds today is its own business.
            core.balance(book)
            val transfers = core.history(book)
            assertTrue(transfers.isNotEmpty(), "address has USDT transfer history")
            assertTrue(transfers.first().txid.startsWith("0x"))
        }
    }
}
