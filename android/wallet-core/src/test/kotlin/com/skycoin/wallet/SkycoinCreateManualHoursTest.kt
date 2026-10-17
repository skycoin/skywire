package com.skycoin.wallet

import com.skycoin.wallet.skycoin.SkycoinCreate
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/**
 * Manual hours selection: the destination receives exactly the hours the
 * caller named, and the automatic rules — the half share, the extra input
 * for orphaned change hours, the full-share retry — stay out of it.
 */
class SkycoinCreateManualHoursTest {

    // Real addresses from the golden fixtures; pushOutput checks the checksum.
    private val holder = "cfb24jWcms9pZtvEx3UjUuLPdT6rbx6r9M"
    private val dest = "nJUPS5CrLV2iBdMvdMzMiP7iA2dzumaWGc"
    private val change = "UxnG3PoFvKtRuLu5nyFdb6eu6pu1SZadkR"

    private fun ux(coins: ULong, hours: ULong) = SkycoinCreate.UxBalance(
        hash = ByteArray(32) { it.toByte() },
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
}
