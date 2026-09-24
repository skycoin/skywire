package com.skycoin.skywire.core

import com.skycoin.skywire.api.ServiceEntry
import kotlinx.coroutines.flow.first
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json

/**
 * The last server list service discovery returned for a type, kept across
 * process death.
 *
 * The list rides dmsg and takes seconds to arrive — ten and more on a slow
 * link — and a new view model per visit started from nothing, so every open of
 * SkyVPN or SkySOCKS was a spinner over an empty list, and a fetch that failed
 * left nothing to pick from. With the last list on screen at once, the fetch
 * only refreshes it, and a failed refresh costs a "last updated" rather than
 * the list.
 */
class ServerListCache(private val prefs: AppPreferences) {

    private val json = Json { ignoreUnknownKeys = true }
    private val serializer = ListSerializer(ServiceEntry.serializer())

    suspend fun read(type: String): List<ServiceEntry>? =
        prefs.string(key(type)).first()?.let { stored ->
            runCatching { json.decodeFromString(serializer, stored) }.getOrNull()
        }

    suspend fun write(type: String, servers: List<ServiceEntry>) {
        prefs.putString(key(type), json.encodeToString(serializer, servers))
    }

    private fun key(type: String) = "servers_cache_$type"
}
