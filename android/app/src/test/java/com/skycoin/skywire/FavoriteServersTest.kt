package com.skycoin.skywire

import com.skycoin.skywire.api.GeoInfo
import com.skycoin.skywire.api.ServiceEntry
import com.skycoin.skywire.ui.components.SavedServer
import com.skycoin.skywire.ui.components.favoriteRows
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** The favorites section of a server list: what it shows, and from where. */
class FavoriteServersTest {

    private val fast = "03" + "a".repeat(64)
    private val gone = "02" + "b".repeat(64)
    private val other = "03" + "c".repeat(64)

    private val listed = listOf(
        ServiceEntry(address = "$fast:44", geo = GeoInfo(country = "de", region = "Hesse"), version = "v1.3.96"),
        ServiceEntry(address = "$other:44", geo = GeoInfo(country = "us"), version = "v1.3.96"),
    )

    /** A listed favorite is drawn from the live entry — its location and version now. */
    @Test
    fun listedFavoriteUsesTheLiveEntry() {
        val rows = favoriteRows(listOf(SavedServer(fast, country = "fr", version = "v1.3.90")), listed, "")
        assertEquals(1, rows.size)
        assertTrue(rows[0].listed)
        assertEquals("$fast:44", rows[0].entry.address)
        assertEquals("de", rows[0].entry.geo?.country)
    }

    /**
     * A favorite discovery does not list right now still shows, from what was
     * saved. Dropping it would lose the one server the user asked to keep
     * the first time it went offline.
     */
    @Test
    fun unlistedFavoriteStaysFromWhatWasSaved() {
        val rows = favoriteRows(listOf(SavedServer(gone, country = "jp", version = "v1.3.95")), listed, "")
        assertEquals(1, rows.size)
        assertFalse(rows[0].listed)
        assertEquals(gone, rows[0].entry.pk)
        assertEquals("jp", rows[0].entry.geo?.country)
        assertEquals("v1.3.95", rows[0].entry.version)
    }

    @Test
    fun keepsTheOrderTheyWereStarred() {
        val rows = favoriteRows(listOf(SavedServer(gone), SavedServer(fast)), listed, "")
        assertEquals(listOf(gone, fast), rows.map { it.entry.pk })
    }

    /** The search box filters favorites like the rest of the list. */
    @Test
    fun searchFiltersFavoritesToo() {
        val favorites = listOf(SavedServer(fast), SavedServer(gone, country = "jp"))
        assertEquals(listOf(fast), favoriteRows(favorites, listed, "hesse").map { it.entry.pk })
        assertEquals(listOf(gone), favoriteRows(favorites, listed, "JP").map { it.entry.pk })
        assertTrue(favoriteRows(favorites, listed, "nowhere").isEmpty())
    }
}
