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
    private val resumeCount = MutableStateFlow(0)

    val isForeground: StateFlow<Boolean> = foreground.asStateFlow()

    /**
     * Bumped on every Activity resume. [isForeground] cannot carry this:
     * a system dialog over the app (the battery-exemption request, a
     * permission prompt) only pauses the Activity, so start/stop — and
     * with it the foreground flag — never changes, and anything that
     * needs to re-read system state "when the user comes back" would
     * never run. Collect this instead for that.
     */
    val resumes: StateFlow<Int> = resumeCount.asStateFlow()

    fun set(visible: Boolean) {
        foreground.value = visible
    }

    fun onResumed() {
        resumeCount.value += 1
    }
}
