package com.skycoin.skywire

import com.skycoin.skywire.core.AppUpdates
import com.skycoin.skywire.core.UpdateNotice
import com.skycoin.skywire.core.UpdateNotice.Source
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * Where the update banner sends the user, and what F-Droid is offering.
 *
 * A wrong source sends a store install to a self-update that cannot install
 * over it, or a GitHub install to a store that never had it.
 */
class UpdateNoticeTest {

    @Test
    fun theInstallerDecidesWhereUpdatesComeFrom() {
        assertEquals(Source.PLAY, UpdateNotice.sourceOf("com.android.vending", selfUpdate = true))
        assertEquals(Source.FDROID, UpdateNotice.sourceOf("org.fdroid.fdroid", selfUpdate = true))
        assertEquals(Source.FDROID, UpdateNotice.sourceOf("com.looker.droidify", selfUpdate = true))
        // A browser download, adb, or a previous self-update.
        assertEquals(Source.DIRECT, UpdateNotice.sourceOf("com.google.android.packageinstaller", selfUpdate = true))
        assertEquals(Source.DIRECT, UpdateNotice.sourceOf(null, selfUpdate = true))
        assertEquals(Source.DIRECT, UpdateNotice.sourceOf("com.skycoin.skywire", selfUpdate = true))
    }

    @Test
    fun theFdroidBuildGoesToFdroidWhoeverInstalledIt() {
        // It has no installer of its own, so a sideloaded copy still updates from F-Droid.
        assertEquals(Source.FDROID, UpdateNotice.sourceOf(null, selfUpdate = false))
        assertEquals(Source.FDROID, UpdateNotice.sourceOf("com.google.android.packageinstaller", selfUpdate = false))
    }

    @Test
    fun fdroidOffersItsSuggestedVersionOnlyWhenNewer() {
        assertEquals("0.0.6", AppUpdates.selectFdroidUpdate(FDROID, installedCode = 5))
        assertNull(AppUpdates.selectFdroidUpdate(FDROID, installedCode = 6))
        assertNull(AppUpdates.selectFdroidUpdate(FDROID, installedCode = 7))
    }

    @Test
    fun fdroidOffersTheSuggestedVersionNotTheNewestListed() {
        // F-Droid lists builds above the suggested one, and its client does not offer them.
        assertEquals("0.0.6", AppUpdates.selectFdroidUpdate(FDROID_AHEAD, installedCode = 5))
    }

    private companion object {
        val FDROID = """
            {"packageName":"com.skycoin.skywire","suggestedVersionCode":6,
             "packages":[{"versionName":"0.0.6","versionCode":6},{"versionName":"0.0.5","versionCode":5}]}
        """.trimIndent()

        val FDROID_AHEAD = """
            {"packageName":"com.skycoin.skywire","suggestedVersionCode":6,
             "packages":[{"versionName":"0.0.7","versionCode":7},{"versionName":"0.0.6","versionCode":6}]}
        """.trimIndent()
    }
}
