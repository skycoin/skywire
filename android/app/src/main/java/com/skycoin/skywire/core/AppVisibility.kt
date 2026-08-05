package com.skycoin.skywire.core

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Whether the user is looking at this app right now.
 *
 * It decides how a call announces itself, and the two answers are genuinely
 * different UIs: on screen, the app draws the call itself; off screen, the only
 * way to put a call in front of someone is to ask the system to bring the
 * Activity up (see the full-screen intent in [VoiceCallWatcher]).
 */
object AppVisibility {

    private val foreground = MutableStateFlow(false)

    val isForeground: StateFlow<Boolean> = foreground.asStateFlow()

    fun set(visible: Boolean) {
        foreground.value = visible
    }
}
