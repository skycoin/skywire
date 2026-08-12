package com.skycoin.skywire.core

import android.app.LocaleManager
import android.content.Context
import android.content.res.Configuration
import android.os.Build
import android.os.LocaleList
import androidx.annotation.RequiresApi
import androidx.core.content.edit
import java.util.Locale

/**
 * Where the chosen interface language is kept, and how it reaches the strings.
 *
 * Two mechanisms behind one door, because the platform grew its own halfway
 * through the range this app supports:
 *
 *  - **API 33 and up** owns per-app language. [set] hands the choice to
 *    `LocaleManager`; the system persists it, restarts the activities and
 *    lists the app in Settings ▸ Apps ▸ Skywire ▸ Language. From then on the
 *    platform is the source of truth, which is why [current] asks it rather
 *    than our own store — a change made in system settings has to show up on
 *    our screen too, or the two disagree about what the app is running in.
 *  - **API 26–32** has nothing to hand it to. The choice lives in the prefs
 *    below and every component picks it up by wrapping its base context with
 *    [wrap]. Nothing recreates the Activity on its own there, so [set] says so
 *    in its return value.
 *
 * The one honest caveat, and only below 33: a service that is *already*
 * running keeps the language it was created with, because its resources were
 * resolved then. In practice that is the core service's notification text
 * until the visor is next stopped and started. Activities are recreated on the
 * spot and so read correctly straight away.
 *
 * Deliberately its own tiny SharedPreferences file rather than a key in
 * [AppPreferences]: this is read from `attachBaseContext`, before anything is
 * on screen and on the main thread, and DataStore is asynchronous by
 * construction. One synchronous string is what that moment can afford.
 */
object AppLocale {

    private const val PREFS = "locale"

    /** What the interface is currently drawn in. */
    fun current(context: Context): AppLanguage =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            AppLanguage.ofTags(localeManager(context)?.applicationLocales?.toLanguageTags())
        } else {
            AppLanguage.of(prefs(context).getString(AppLanguage.PREF_KEY, null))
        }

    /**
     * Persist [language] and apply it.
     *
     * Returns true when the caller still has to call `Activity.recreate()` —
     * below API 33 nothing else will. On 33+ the platform restarts the
     * activities itself and a second recreate would only throw the screen
     * away twice.
     */
    fun set(context: Context, language: AppLanguage): Boolean {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            localeManager(context)?.applicationLocales = when (language) {
                AppLanguage.SYSTEM -> LocaleList.getEmptyLocaleList()
                else -> LocaleList.forLanguageTags(language.tag)
            }
            return false
        }
        prefs(context).edit { putString(AppLanguage.PREF_KEY, language.name) }
        return true
    }

    /**
     * The context a component should run on, for `attachBaseContext`.
     *
     * A no-op on API 33+, where the platform has already resolved resources
     * against the per-app locale before this is reached — wrapping again would
     * pin a stale choice over the system's current one.
     */
    fun wrap(base: Context): Context {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) return base
        val language = current(base)
        if (language == AppLanguage.SYSTEM) return base
        val locale = Locale.forLanguageTag(language.tag)
        // Not only the resources: dates, and anything else formatted without a
        // Context in hand, read the process default.
        Locale.setDefault(locale)
        val config = Configuration(base.resources.configuration)
        config.setLocale(locale)
        config.setLayoutDirection(locale)
        return base.createConfigurationContext(config)
    }

    @RequiresApi(Build.VERSION_CODES.TIRAMISU)
    private fun localeManager(context: Context): LocaleManager? =
        context.getSystemService(LocaleManager::class.java)

    private fun prefs(context: Context) =
        context.applicationContext.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
}
