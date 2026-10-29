package com.skycoin.skywire

import com.skycoin.skywire.core.MeteredFlag
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** Which metered observations the core is told about. */
class MeteredFlagTest {

    @Test
    fun theFirstKnownValueIsNews() {
        // The core starts out assuming an unmetered network, so it must hear
        // about mobile data at once, and about Wi-Fi too.
        assertTrue(MeteredFlag().observe(true))
        assertTrue(MeteredFlag().observe(false))
    }

    @Test
    fun unknownIsNeverNews() {
        val flag = MeteredFlag()
        assertFalse(flag.observe(null))
        assertTrue(flag.observe(true))
        assertFalse(flag.observe(null))
    }

    @Test
    fun repeatsAreNotNewsButAFlipIs() {
        val flag = MeteredFlag()
        flag.observe(true)
        assertFalse(flag.observe(true))
        assertTrue(flag.observe(false))
        assertFalse(flag.observe(false))
        assertTrue(flag.observe(true))
    }
}
