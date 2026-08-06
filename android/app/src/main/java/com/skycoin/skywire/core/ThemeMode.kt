package com.skycoin.skywire.core

/**
 * The user's theme override. The palette itself is brand-locked (see
 * `ui/theme/Theme.kt`) — this only chooses which of its two halves is drawn.
 */
enum class ThemeMode {
    SYSTEM,
    LIGHT,
    DARK,
    ;

    /** [systemDark] is what the phone would have chosen on its own. */
    fun isDark(systemDark: Boolean): Boolean = when (this) {
        SYSTEM -> systemDark
        LIGHT -> false
        DARK -> true
    }

    companion object {
        const val PREF_KEY = "theme_mode"

        /** Anything unrecognised — an older build's value — reads as [SYSTEM]. */
        fun of(stored: String?): ThemeMode =
            entries.firstOrNull { it.name == stored } ?: SYSTEM
    }
}
