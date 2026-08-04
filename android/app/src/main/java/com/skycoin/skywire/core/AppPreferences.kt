package com.skycoin.skywire.core

import android.content.Context
import androidx.datastore.preferences.core.edit
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
}
