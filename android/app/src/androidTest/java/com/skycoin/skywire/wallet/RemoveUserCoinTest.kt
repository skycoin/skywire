package com.skycoin.skywire.wallet

import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Removing a coin from the wallet's list — the feature a tester asked for
 * ("How do I delete a coin from the list of coins?") and the two refusals
 * that come with it.
 *
 * The refusal that matters is the one for a coin that still has wallets. The
 * obvious implementation cascades into removeWallet, and removeWallet deletes
 * a sealed seed that exists on this phone and nowhere else — so a mis-tap
 * while tidying a list would be indistinguishable, right up to the point
 * where the coins are unreachable. Nothing here may quietly acquire that
 * power, which is why it is asserted rather than left to review.
 *
 * On-device because the store is DataStore over a real file and the seed
 * store is AndroidKeyStore-sealed; neither has a JVM stand-in. The tests
 * clean up the coins they add.
 */
@RunWith(AndroidJUnit4::class)
class RemoveUserCoinTest {

    private val repo: WalletRepository
        get() = WalletRepository.get(InstrumentationRegistry.getInstrumentation().targetContext)

    private val added = mutableListOf<String>()

    @After
    fun cleanUp() = runBlocking {
        added.forEach { runCatching { repo.removeUserCoin(it) } }
        repo.setSelectedCoin(CoinSpec.SKY.id)
    }

    private suspend fun addCoin(name: String): CoinSpec =
        repo.addFiberCoin(name, "TST", "http://127.0.0.1:6420").also { added += it.id }

    @Test
    fun removesAUserAddedCoin() = runBlocking {
        val coin = addCoin("Removable")
        assertNotNull("the coin should be in the list after adding", repo.coin(coin.id))

        val gone = repo.removeUserCoin(coin.id)

        assertEquals("the removed spec is returned so the caller can name it", coin.id, gone?.id)
        assertNull("the coin is still listed after removal", repo.coin(coin.id))
        assertTrue(
            "removal must not disturb the built-ins",
            repo.coins().first().any { it.id == CoinSpec.SKY.id },
        )
    }

    /** The selection cannot be left pointing at something no longer listed. */
    @Test
    fun removingTheSelectedCoinFallsBackToSky() = runBlocking {
        val coin = addCoin("Selected")
        repo.setSelectedCoin(coin.id)
        assertEquals(coin.id, repo.selectedCoinId().first())

        repo.removeUserCoin(coin.id)

        assertEquals(
            "the wallet tab would open on a coin missing from its own list",
            CoinSpec.SKY.id,
            repo.selectedCoinId().first(),
        )
    }

    @Test
    fun refusesToRemoveABuiltIn() = runBlocking {
        for (builtIn in listOf(CoinSpec.SKY, CoinSpec.BTC, CoinSpec.ETH, CoinSpec.USDT)) {
            val failure = runCatching { repo.removeUserCoin(builtIn.id) }.exceptionOrNull()
            assertTrue(
                "${builtIn.id} was removable — it has no add path to restore it, " +
                    "and SKY is what the selection falls back to",
                failure is IllegalArgumentException,
            )
            assertNotNull("${builtIn.id} left the list", repo.coin(builtIn.id))
        }
    }

    /**
     * A coin holding wallets is refused, and — the part with teeth — the
     * wallet and its seed are still there afterwards.
     */
    @Test
    fun refusesWhileWalletsExistAndKeepsTheirSeeds() = runBlocking {
        val coin = addCoin("Held")
        val wallet = repo.addWallet(
            coin,
            name = "held-wallet",
            mnemonic = TEST_MNEMONIC,
            restored = false,
        )
        try {
            val failure = runCatching { repo.removeUserCoin(coin.id) }.exceptionOrNull()

            assertTrue(
                "a coin with wallets was removed — if that ever cascades, it deletes " +
                    "recovery phrases that exist nowhere else",
                failure is IllegalArgumentException,
            )
            assertNotNull("the coin left the list despite the refusal", repo.coin(coin.id))
            assertNotNull("the wallet was dropped by a refused removal", repo.wallet(wallet.id))
            assertNotNull("the sealed seed did not survive a refused removal", repo.revealSeed(wallet.id))

            // And it goes once the wallet does, so the refusal is a sequence
            // rather than a dead end.
            repo.removeWallet(wallet.id)
            assertNotNull("removing the wallet should now let the coin go", repo.removeUserCoin(coin.id))
            assertNull(repo.coin(coin.id))
        } finally {
            runCatching { repo.removeWallet(wallet.id) }
        }
    }

    private companion object {
        /** A throwaway BIP-39 phrase; this test never touches a network. */
        const val TEST_MNEMONIC =
            "abandon abandon abandon abandon abandon abandon " +
                "abandon abandon abandon abandon abandon about"
    }
}
