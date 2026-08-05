package com.skycoin.skywire.core

import android.content.Context
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.longPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

private val Context.settingsDataStore by preferencesDataStore(name = "settings")

/**
 * Non-secret, user-facing preferences — the app-screen state that has to
 * survive a process death (last-used server, last chosen options).
 * Deliberately separate from [SecretStore]'s store: nothing here is
 * encrypted, and nothing here is worth encrypting.
 */
class AppPreferences(context: Context) {

    private val store = context.applicationContext.settingsDataStore

    fun string(key: String): Flow<String?> =
        store.data.map { it[stringPreferencesKey(key)] }

    suspend fun putString(key: String, value: String?) {
        store.edit { prefs ->
            val pref = stringPreferencesKey(key)
            if (value == null) prefs.remove(pref) else prefs[pref] = value
        }
    }

    /** [fallback] is what an unset key reads as — there is no "absent". */
    fun boolean(key: String, fallback: Boolean = false): Flow<Boolean> =
        store.data.map { it[booleanPreferencesKey(key)] ?: fallback }

    suspend fun putBoolean(key: String, value: Boolean) {
        store.edit { it[booleanPreferencesKey(key)] = value }
    }

    fun long(key: String, fallback: Long = 0L): Flow<Long> =
        store.data.map { it[longPreferencesKey(key)] ?: fallback }

    suspend fun putLong(key: String, value: Long) {
        store.edit { it[longPreferencesKey(key)] = value }
    }
}
