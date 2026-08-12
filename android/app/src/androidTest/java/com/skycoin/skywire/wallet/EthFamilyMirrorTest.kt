package com.skycoin.skywire.wallet

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * One Ethereum account, every Ethereum-family coin.
 *
 * ETH and every ERC-20 are the same key at the same address on the same
 * chain. The store keys a wallet by coin, so setting up ETH used to leave
 * USDT empty and ask for the identical recovery phrase a second time to see
 * the balance already sitting at the address just added — and a third time
 * for the next token. These pin that one entry is enough, in both
 * directions and for tokens added afterwards.
 *
 * The addresses must be identical, not merely both valid: they are the same
 * account, so a mirror that derived differently would quietly point at money
 * that is not there.
 */
@RunWith(AndroidJUnit4::class)
class EthFamilyMirrorTest {

    private val repo: WalletRepository
        get() = WalletRepository.get(InstrumentationRegistry.getInstrumentation().targetContext)

    private val addedCoins = mutableListOf<String>()
    private val addedWallets = mutableListOf<String>()

    @After
    fun cleanUp() = runBlocking {
        addedWallets.forEach { runCatching { repo.removeWallet(it) } }
        // Mirrors are not in addedWallets — they were never returned to the
        // caller — so anything left holding the test account goes too.
        repo.wallets().first()
            .filter { it.receiveAddresses.firstOrNull() == null || it.name.startsWith(TEST_NAME) }
            .forEach { runCatching { repo.removeWallet(it.id) } }
        addedCoins.forEach { runCatching { repo.removeUserCoin(it) } }
        repo.setSelectedCoin(CoinSpec.SKY.id)
    }

    private suspend fun addWallet(coin: CoinSpec): WalletMeta =
        repo.addWallet(coin, TEST_NAME, TEST_MNEMONIC, restored = false).also { addedWallets += it.id }

    private suspend fun walletsFor(coinId: String): List<WalletMeta> =
        repo.wallets().first().filter { it.coinId == coinId && it.name.startsWith(TEST_NAME) }

    @Test
    fun addingEthGivesUsdtTheSameWallet() = runBlocking {
        val eth = addWallet(CoinSpec.ETH)

        val usdt = walletsFor(CoinSpec.USDT.id)
        assertEquals("adding ETH should give USDT the same account, once", 1, usdt.size)
        assertEquals(
            "the mirrored wallet must hold the identical address — it is the same account",
            eth.receiveAddresses,
            usdt.single().receiveAddresses,
        )
        assertNotNull(
            "the mirror has no usable seed, so it could never sign",
            repo.revealSeed(usdt.single().id),
        )
    }

    @Test
    fun addingUsdtGivesEthTheSameWallet() = runBlocking {
        val usdt = addWallet(CoinSpec.USDT)

        val eth = walletsFor(CoinSpec.ETH.id)
        assertEquals("adding USDT should give ETH the same account, once", 1, eth.size)
        assertEquals(usdt.receiveAddresses, eth.single().receiveAddresses)
    }

    /** Entering the phrase for the second coin anyway must not double it up. */
    @Test
    fun restoringTheSamePhraseUnderTheSiblingAddsNothing() = runBlocking {
        addWallet(CoinSpec.ETH)
        assertEquals(1, walletsFor(CoinSpec.USDT.id).size)

        val again = repo.addWallet(CoinSpec.USDT, TEST_NAME, TEST_MNEMONIC, restored = false)
        addedWallets += again.id

        assertEquals(
            "the same account under USDT twice — the mirror should have been recognised",
            1,
            walletsFor(CoinSpec.USDT.id).size,
        )
    }

    /** A token added later reaches the account that was set up before it. */
    @Test
    fun aTokenAddedLaterInheritsTheAccount() = runBlocking {
        val eth = addWallet(CoinSpec.ETH)

        val token = repo.addErc20Token(
            name = "Test Token",
            ticker = "TTK",
            contract = USDC_CONTRACT,
            decimals = 6,
        ).also { addedCoins += it.id }

        val inherited = walletsFor(token.id)
        assertEquals("a token added later should arrive holding the account", 1, inherited.size)
        assertEquals(eth.receiveAddresses, inherited.single().receiveAddresses)
    }

    /** Bitcoin is a different chain: nothing may leak across. */
    @Test
    fun doesNotMirrorOutsideTheEthereumFamily() = runBlocking {
        addWallet(CoinSpec.ETH)

        assertTrue(
            "an Ethereum wallet appeared under Bitcoin",
            walletsFor(CoinSpec.BTC.id).isEmpty(),
        )
        assertTrue(
            "an Ethereum wallet appeared under Skycoin",
            walletsFor(CoinSpec.SKY.id).isEmpty(),
        )
    }

    private companion object {
        const val TEST_NAME = "mirror-test"

        /** A throwaway BIP-39 phrase; these tests never touch a network. */
        const val TEST_MNEMONIC =
            "abandon abandon abandon abandon abandon abandon " +
                "abandon abandon abandon abandon abandon about"

        /** Real USDC, used only as a checksum-valid contract address. */
        const val USDC_CONTRACT = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"
    }
}
