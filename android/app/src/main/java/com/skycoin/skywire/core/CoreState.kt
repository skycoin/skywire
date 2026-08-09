package com.skycoin.skywire.core

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

/** Lifecycle of the visor child process, as the service reports it. */
sealed interface CoreState {
    data object Stopped : CoreState

    /** Config check/generation + first spawn. */
    data class Starting(val attempt: Int) : CoreState

    data class Running(val sinceEpochMs: Long, val attempt: Int) : CoreState

    /** Visor exited unexpectedly; next spawn happens after [delayMs]. */
    data class Restarting(val nextAttempt: Int, val delayMs: Long) : CoreState

    data object Stopping : CoreState

    /** Unrecoverable (missing binary, config gen failure) — see process log. */
    data class Failed(val message: String) : CoreState
}

/**
 * In-process bridge between [SkywireCoreService] and the UI. The service and
 * the Compose tree live in the same process, so a StateFlow is enough — no
 * binder ceremony.
 */
object CoreServiceState {
    internal val mutableState = MutableStateFlow<CoreState>(CoreState.Stopped)
    val state = mutableState.asStateFlow()
}
