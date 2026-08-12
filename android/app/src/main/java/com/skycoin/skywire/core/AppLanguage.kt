package com.skycoin.skywire.core

import java.util.Locale

/**
 * The language the interface is drawn in. [SYSTEM] is whatever the phone is
 * set to; every other entry names a translation this app actually ships, as
 * `res/values-<tag>/strings.xml`.
 *
 * Adding a language is three edits and nothing else: drop in the values folder,
 * add a constant here with its BCP-47 tag, and list the tag in
 * `res/xml/locales_config.xml` — that file is what Android 13+ reads to offer
 * the app in Settings ▸ Apps ▸ Skywire ▸ Language. The picker in Settings
 * builds itself from [entries].
 *
 * Only the app's own interface follows this. Logs, the visor's own output and
 * anything the network reports stay in the language they were written in —
 * they are read alongside a `skywire cli` on a desktop, and a translated log
 * line is a log line nobody can search for.
 */
enum class AppLanguage(val tag: String) {
    /** No tag: the platform picks from the phone's language list. */
    SYSTEM(""),
    ENGLISH("en"),
    CHINESE_SIMPLIFIED("zh-CN"),
    ;

    companion object {
        const val PREF_KEY = "app_language"

        /** Anything unrecognised — an older build's value — reads as [SYSTEM]. */
        fun of(stored: String?): AppLanguage =
            entries.firstOrNull { it.name == stored } ?: SYSTEM

        /**
         * The entry a BCP-47 tag list means, as `LocaleManager` hands it back.
         *
         * Matched on the language subtag alone, because the platform is free to
         * canonicalise: ask it for `zh-CN` and a later read can return
         * `zh-Hans-CN`. One translation per language is shipped here, so the
         * subtag is enough to find it, and a tag for a language that is not
         * shipped is the same situation as no tag at all — [SYSTEM].
         */
        fun ofTags(tags: String?): AppLanguage {
            val first = tags?.split(',')?.firstOrNull()?.trim().orEmpty()
            if (first.isEmpty()) return SYSTEM
            val language = Locale.forLanguageTag(first).language
            if (language.isEmpty()) return SYSTEM
            return entries.firstOrNull {
                it != SYSTEM && Locale.forLanguageTag(it.tag).language == language
            } ?: SYSTEM
        }
    }
}
