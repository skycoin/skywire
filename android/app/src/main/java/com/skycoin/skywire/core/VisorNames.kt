package com.skycoin.skywire.core

import android.content.Context
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.serialization.builtins.MapSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json

/**
 * The names the user gives their own visors, so a Fleet row says "home server"
 * instead of 66 hex characters.
 *
 * Kept on the phone, not on the visor. Fleet is a read-only window onto those
 * machines — it would be a strange first exception to that for a label — and
 * the label is the phone user's private note about which box is which, not a
 * fact about the visor. The consequence is honest and worth stating: names do
 * not travel to another device.
 *
 * One JSON object under one preference key: the map is a handful of entries,
 * and a per-key preference would leave orphans behind whenever a visor is
 * renamed or retired.
 */
class VisorNames(context: Context) {

    private val prefs = AppPreferences(context.applicationContext)
    private val json = Json { ignoreUnknownKeys = true }
    private val serializer = MapSerializer(String.serializer(), String.serializer())

    /** Public key → name. Missing and blank are the same thing: unnamed. */
    fun names(): Flow<Map<String, String>> = prefs.string(KEY).map { stored ->
        if (stored.isNullOrEmpty()) {
            emptyMap()
        } else {
            runCatching { json.decodeFromString(serializer, stored) }.getOrDefault(emptyMap())
        }
    }

    /** A blank [name] clears it — that is how the dialog removes one. */
    suspend fun setName(pk: String, name: String) {
        val current = currentMap()
        val trimmed = name.trim().take(MAX_LENGTH)
        val updated = if (trimmed.isEmpty()) current - pk else current + (pk to trimmed)
        prefs.putString(KEY, json.encodeToString(serializer, updated))
    }

    private suspend fun currentMap(): Map<String, String> = names().first()

    private companion object {
        const val KEY = "fleet_visor_names"

        /** Long enough for "office rack, top shelf"; short enough for a card. */
        const val MAX_LENGTH = 40
    }
}
