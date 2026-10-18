package com.skycoin.wallet

import com.skycoin.wallet.skycoin.SkycoinCreate
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/**
 * Manual hours selection: the destination receives exactly the hours the
 * caller named, the half share and the full-share retry stay out of it, and
 * every hour that burns is in the fee the review screen shows.
 */
class SkycoinCreateManualHoursTest {

    // Real addresses from the golden fixtures; pushOutput checks the checksum.
    private val holder = "cfb24jWcms9pZtvEx3UjUuLPdT6rbx6r9M"
    private val dest = "nJUPS5CrLV2iBdMvdMzMiP7iA2dzumaWGc"
    private val change = "UxnG3PoFvKtRuLu5nyFdb6eu6pu1SZadkR"

    private fun ux(coins: ULong, hours: ULong, id: Int = 0) = SkycoinCreate.UxBalance(
        hash = ByteArray(32) { (it + id).toByte() },
        hashHex = "00",
        bkSeq = 1u,
        address = holder,
        coins = coins,
        initialHours = 0u,
        hours = hours,
    )

    @Test
    fun manualHoursGoExactlyToTheDestination() {
        val created = SkycoinCreate.create(
            listOf(ux(10_000_000u, 100u)),
            listOf(SkycoinCreate.Destination(dest, 4_000_000u, 30u)),
            change,
            burnFactor = 2u,
        )
        assertEquals(50uL, created.feeHours) // ceil(100 / 2)
        assertEquals(30uL, created.hoursToDestinations)
        assertEquals(20uL, created.changeHours) // 100 - 50 - 30
        assertEquals(6_000_000uL, created.changeCoins)
    }

    @Test
    fun hoursBeyondWhatTheInputsHoldAreRefused() {
        // 100 input hours burn 50, so 60 to the destination cannot be met.
        assertFailsWith<SkycoinCreate.InsufficientHours> {
            SkycoinCreate.create(
                listOf(ux(10_000_000u, 100u)),
                listOf(SkycoinCreate.Destination(dest, 1_000_000u, 60u)),
                change,
                burnFactor = 2u,
            )
        }
    }

    @Test
    fun zeroManualHoursSendsCoinsWithoutHours() {
        val created = SkycoinCreate.create(
            listOf(ux(10_000_000u, 100u)),
            listOf(SkycoinCreate.Destination(dest, 4_000_000u, 0u)),
            change,
            burnFactor = 2u,
        )
        assertEquals(0uL, created.hoursToDestinations)
        assertEquals(50uL, created.changeHours)
    }

    @Test
    fun hoursWithNoChangeToCarryThemShowAsBurned() {
        // Everything leaves as coins, 1 hour goes along, and the other 999 burn.
        val created = SkycoinCreate.create(
            listOf(ux(10_000_000u, 1000u)),
            listOf(SkycoinCreate.Destination(dest, 10_000_000u, 1u)),
            change,
            burnFactor = 10u,
        )
        assertEquals(999uL, created.feeHours)
        assertEquals(0uL, created.changeCoins)
        assertEquals(0uL, created.changeHours)
    }

    @Test
    fun anotherOutputRescuesTheHoursAsInGo() {
        // The extra input costs 1 hour of fee and carries 899 change hours home.
        val created = SkycoinCreate.create(
            listOf(ux(10_000_000u, 1000u, id = 0), ux(1_000_000u, 10u, id = 1)),
            listOf(SkycoinCreate.Destination(dest, 10_000_000u, 1u)),
            change,
            burnFactor = 10u,
        )
        assertEquals(2, created.spends.size)
        assertEquals(101uL, created.feeHours) // ceil(1010 / 10)
        assertEquals(1_000_000uL, created.changeCoins)
        assertEquals(908uL, created.changeHours) // 1010 - 101 - 1
    }
}
