package com.skycoin.skywire.core

/**
 * How much the visor writes to its log — the config's top-level `log_level`.
 *
 * Written into the config by [ConfigManager] on every launch from the phone's
 * preference, for the same reason the transport order and the Fleet opt-in are:
 * the app owns the setting, and the visor persists its own copy.
 *
 * The visor reads it once, while it builds its module graph, so a change means
 * restarting the core.
 */
object CoreLogLevel {

    const val PREF_KEY = "core_log_level"

    /**
     * What `config gen` writes when nothing asks otherwise. Note the visor's
     * own fallback for an *empty* field is `debug` — this default is quieter
     * than that on purpose: debug on a phone is a lot of writing for a log
     * nobody is reading.
     */
    const val DEFAULT = "info"

    /**
     * Coarse to fine. `fatal` and `panic` parse too but are not offered: a
     * visor logging only its own death is not a diagnostic setting.
     */
    val LEVELS = listOf("error", "warn", "info", "debug", "trace")

    fun sanitize(stored: String?): String =
        stored?.lowercase()?.takeIf { it in LEVELS } ?: DEFAULT
}
