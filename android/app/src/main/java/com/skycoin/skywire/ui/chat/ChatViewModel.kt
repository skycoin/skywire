package com.skycoin.skywire.ui.chat

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.api.AppState
import com.skycoin.skywire.api.SkychatApi
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.core.CoreServiceState
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.SkychatProfile
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** Everything the Chat screen renders. */
data class ChatUiState(
    val coreState: CoreState = CoreState.Stopped,
    /** The visor's API answered — nothing can be started before it does. */
    val apiUp: Boolean = false,
    /** Bringing skychat up: start issued, waiting for its listener. */
    val starting: Boolean = false,
    /** Set once the surface answered; the WebView loads exactly this. */
    val url: String? = null,
    /** Secret the WebView answers the password gate with — see [SkychatProfile]. */
    val password: String? = null,
    val error: String? = null,
) {
    val coreReady: Boolean get() = coreState is CoreState.Running && apiUp

    /** True while the phone is on its way to a usable chat surface. */
    val connecting: Boolean
        get() = error == null && url == null &&
            (starting || coreState is CoreState.Starting || coreState is CoreState.Restarting ||
                (coreState is CoreState.Running && !apiUp))
}

/**
 * Brings the chat surface up and hands the screen a URL to load.
 *
 * skychat autostarts nowhere on the phone (the profile turns every app's
 * autostart off), so opening the tab is what starts it: wait for the visor's
 * API, start the app, then poll its own listener until it answers. The
 * WebView is only shown after that — pointing it at a port nothing is
 * listening on would replace the chat with Chromium's error page and need a
 * manual reload.
 */
class ChatViewModel(app: Application) : AndroidViewModel(app) {

    private val visor = VisorApi.get(app)
    private val skychat = SkychatApi.get(app)

    private val mutable = MutableStateFlow(ChatUiState())
    val uiState: StateFlow<ChatUiState> = mutable.asStateFlow()

    private var bringUpJob: Job? = null

    init {
        viewModelScope.launch {
            val secret = skychat.password()
            mutable.update { it.copy(password = secret) }
        }
        viewModelScope.launch {
            CoreServiceState.state.collectLatest { core ->
                // The URL is dropped with the core: a visor restart restarts
                // the app too, and the page must be reloaded rather than left
                // talking to a listener that went away.
                mutable.update {
                    it.copy(coreState = core, apiUp = false, url = null, error = null)
                }
                if (core !is CoreState.Running) return@collectLatest
                while (!visor.ping()) delay(PING_INTERVAL_MS)
                mutable.update { it.copy(apiUp = true) }
                bringUp()
            }
        }
    }

    /** Retry after a failure, from the screen's Retry button. */
    fun retry() {
        if (!mutable.value.coreReady) return
        bringUpJob?.cancel()
        bringUpJob = viewModelScope.launch { bringUp() }
    }

    /**
     * Start skychat if it isn't up, then wait for its HTTP listener.
     *
     * The start error is held rather than raised: `status: 1` on an app the
     * visor already considers started is a 500, and the polled state can be a
     * beat behind. The probe is the real answer — the error is only surfaced
     * if nothing ever answers.
     */
    private suspend fun bringUp() {
        mutable.update { it.copy(starting = true, error = null) }
        var startError: String? = null
        val app = try {
            val current = visor.app(SkychatProfile.APP)
            if (current.status == AppState.STATUS_RUNNING ||
                current.status == AppState.STATUS_STARTING
            ) {
                current
            } else {
                runCatching { visor.updateApp(SkychatProfile.APP, status = VisorApi.APP_START) }
                    .onFailure { startError = it.message }
                    .getOrDefault(current)
            }
        } catch (e: Exception) {
            mutable.update { it.copy(starting = false, error = e.message) }
            return
        }

        val url = SkychatProfile.baseUrl(SkychatProfile.listenPort(app.args))
        var reloadedGate = false
        repeat(READY_ATTEMPTS) {
            when (skychat.probe(url)) {
                HTTP_OK -> {
                    mutable.update { it.copy(starting = false, url = url, error = null) }
                    return
                }
                // The running app is holding an older password file (the
                // stored secret rotated under it). The file is rewritten at
                // config time, so restarting the app is enough to reload it —
                // once; a second 401 is a real failure worth reporting.
                HTTP_UNAUTHORIZED -> if (!reloadedGate) {
                    reloadedGate = true
                    runCatching {
                        visor.updateApp(SkychatProfile.APP, status = VisorApi.APP_STOP)
                        visor.updateApp(SkychatProfile.APP, status = VisorApi.APP_START)
                    }.onFailure { startError = it.message }
                }
            }
            delay(READY_INTERVAL_MS)
        }
        mutable.update {
            it.copy(starting = false, error = startError ?: "SkyChat did not answer on $url")
        }
    }

    private companion object {
        const val PING_INTERVAL_MS = 700L
        const val READY_INTERVAL_MS = 500L

        /** ~15 s: an in-proc app binds its listener in well under a second. */
        const val READY_ATTEMPTS = 30

        const val HTTP_OK = 200
        const val HTTP_UNAUTHORIZED = 401
    }
}
