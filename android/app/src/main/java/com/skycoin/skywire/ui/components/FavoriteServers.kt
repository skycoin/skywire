package com.skycoin.skywire.ui.components

import com.skycoin.skywire.api.GeoInfo
import com.skycoin.skywire.api.ServiceEntry
import com.skycoin.skywire.core.AppPreferences
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json

/**
 * The servers the user starred, one list per service-discovery type (SkyVPN's
 * exits, SkySOCKS' proxies), kept on the phone across restarts.
 *
 * Finding a fast server is the slow part of either screen: the list is long
 * and nothing in it says which server is quick from here. And there was
 * nowhere to keep one once found, so people copied the key to the clipboard
 * and pasted it back in later. A star keeps it, and puts it at the top of the
 * list.
 *
 * A favorite is stored as the [SavedServer] it was starred from (key, country
 * and version), so it still shows where it is when discovery does not list it
 * at the moment: a server that is down, or a list that has not loaded yet.
 */
class FavoriteServers(private val prefs: AppPreferences) {

    private val json = Json { ignoreUnknownKeys = true }
    private val serializer = ListSerializer(SavedServer.serializer())

    /** Read-modify-write on one preference: two quick taps must not lose one. */
    private val lock = Mutex()

    fun flow(type: String): Flow<List<SavedServer>> =
        prefs.string(key(type)).map { stored ->
            stored?.let { runCatching { json.decodeFromString(serializer, it) }.getOrNull() }.orEmpty()
        }

    /** Star [server] if it is not a favorite, unstar it if it is. Returns whether it now is. */
    suspend fun toggle(type: String, server: SavedServer): Boolean = lock.withLock {
        val current = flow(type).first()
        val starring = current.none { it.pk == server.pk }
        val next = if (starring) current + server else current.filterNot { it.pk == server.pk }
        prefs.putString(key(type), json.encodeToString(serializer, next))
        starring
    }

    private fun key(type: String) = "favorites_$type"
}

/**
 * One row of the favorites section. [listed] is false when discovery does not
 * list the server right now; the row is then drawn from what was saved.
 */
data class FavoriteRow(val entry: ServiceEntry, val listed: Boolean)

/**
 * The favorites to show, in the order they were starred: each as discovery
 * lists it now, since that has the current location and version, or as it was
 * saved when it is not listed. The search box filters them like the rest of the
 * list.
 */
fun favoriteRows(
    favorites: List<SavedServer>,
    servers: List<ServiceEntry>,
    query: String,
): List<FavoriteRow> = favorites
    .map { fav ->
        servers.firstOrNull { it.pk == fav.pk }?.let { FavoriteRow(it, listed = true) }
            ?: FavoriteRow(
                ServiceEntry(address = fav.pk, geo = GeoInfo(country = fav.country), version = fav.version),
                listed = false,
            )
    }
    .filter { it.entry.matches(query) }

/** The search box's rule, shared by the full list and the favorites. */
fun ServiceEntry.matches(query: String): Boolean =
    query.isBlank() ||
        pk.contains(query, true) ||
        geo?.country.orEmpty().contains(query, true) ||
        geo?.region.orEmpty().contains(query, true) ||
        version.contains(query, true)
