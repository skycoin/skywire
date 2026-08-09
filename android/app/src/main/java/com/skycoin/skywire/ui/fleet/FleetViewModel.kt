package com.skycoin.skywire.ui.fleet

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.R
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.api.VisorSummary
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.core.CoreServiceState
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.Fleet
import com.skycoin.skywire.core.SkywireCoreService
import com.skycoin.skywire.core.VisorNames
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** Everything the Fleet screen renders. */
data class FleetUiState(
    val coreState: CoreState = CoreState.Stopped,
    /** The local API answered — without it nothing on this screen works. */
    val apiUp: Boolean = false,
    /** The opt-in itself, as stored on the phone. */
    val enabled: Boolean = Fleet.DEFAULT,
    /** This phone's key — what goes into a remote visor's config. */
    val localPk: String = "",
    /** Remote visors only; this phone is filtered out of the API's list. */
    val visors: List<VisorSummary> = emptyList(),
    /** Public key → the name the user gave that visor, on this phone. */
    val names: Map<String, String> = emptyMap(),
    /** True until the first list lands, so an empty list isn't shown as "none". */
    val loading: Boolean = false,
    /** Key of the visor whose restart is in flight. */
    val restarting: String? = null,
    /** A standing problem with the screen itself — the toggle, or the list. */
    val error: String? = null,
    /**
     * One-shot feedback for an action, for the snackbar. A restart is fired
     * from a card that can be anywhere in a scrolling list, and its outcome
     * arrives seconds later — putting that in the card would say it where the
     * user is no longer looking, or not at all.
     */
    val message: String? = null,
) {
    val coreReady: Boolean get() = coreState is CoreState.Running && apiUp

    /**
     * The core is between lives — the toggle just restarted it, or it
     * crash-looped. Not a state to act in, but not an error either.
     */
    val coreCycling: Boolean
        get() = coreState is CoreState.Starting ||
            coreState is CoreState.Stopping ||
            coreState is CoreState.Restarting ||
            (coreState is CoreState.Running && !apiUp)
}

/**
 * Drives the Fleet screen: the opt-in toggle, and — once it is on — the list
 * of visors that have connected in.
 *
 * The toggle is the whole feature. It writes one preference, which
 * `ConfigManager` turns into `hypervisor.dmsg_ingest` on the next launch, and
 * restarts the core, because the visor reads that field once while it builds
 * its module graph. Nothing here can be applied live.
 *
 * Reads only, with one exception: Restart. Everything else the hypervisor API
 * could do to these visors is deliberately not wired.
 */
class FleetViewModel(app: Application) : AndroidViewModel(app) {

    private val api = VisorApi.get(app)
    private val prefs = AppPreferences(app)
    private val visorNames = VisorNames(app)

    private val mutable = MutableStateFlow(FleetUiState())
    val uiState: StateFlow<FleetUiState> = mutable.asStateFlow()

    private var actionJob: Job? = null

    init {
        viewModelScope.launch {
            prefs.boolean(Fleet.PREF_KEY, Fleet.DEFAULT).collectLatest { enabled ->
                mutable.update {
                    it.copy(enabled = enabled, visors = if (enabled) it.visors else emptyList())
                }
            }
        }
        viewModelScope.launch {
            visorNames.names().collectLatest { names ->
                mutable.update { it.copy(names = names) }
            }
        }
        viewModelScope.launch {
            CoreServiceState.state.collectLatest { core ->
                mutable.update {
                    it.copy(coreState = core, apiUp = false, visors = emptyList())
                }
                if (core !is CoreState.Running) return@collectLatest
                while (!api.ping()) delay(PING_INTERVAL_MS)
                mutable.update { it.copy(apiUp = true) }
                // The key is worth showing whether or not Fleet is on — it is
                // what the user has to put into the other visor's config to
                // make turning it on mean anything.
                runCatching { api.localPk() }.onSuccess { pk ->
                    mutable.update { it.copy(localPk = pk) }
                }
                pollVisors()
            }
        }
    }

    /**
     * Turn the ingest on or off. Saved first, so the choice survives even when
     * the core is down and there is nothing to restart; when it is up, the
     * restart is what actually applies it.
     */
    fun setEnabled(enabled: Boolean) = action {
        prefs.putBoolean(Fleet.PREF_KEY, enabled)
        mutable.update {
            it.copy(
                enabled = enabled,
                visors = if (enabled) it.visors else emptyList(),
                loading = enabled,
            )
        }
        if (mutable.value.coreState is CoreState.Stopped ||
            mutable.value.coreState is CoreState.Failed
        ) {
            return@action
        }
        // Detached on purpose: leaving this screen must not cancel a restart
        // halfway and leave the phone with no core.
        SkywireCoreService.restart(getApplication())
    }

    /**
     * Restart one visor. "Sent" is all this can honestly report: the visor
     * cannot answer a request that tears down the connection carrying it, so
     * the real outcome arrives as the row going offline and coming back.
     * [label] is what to call it in that message — its name, or its key.
     */
    fun restartVisor(pk: String, label: String) = action {
        mutable.update { it.copy(restarting = pk) }
        try {
            api.restartVisor(pk)
            mutable.update { state ->
                state.copy(
                    message = getApplication<Application>()
                        .getString(R.string.fleet_restart_sent, label),
                    // Marked offline now rather than at the next poll: the
                    // visor IS going down, and a row still reading "connected"
                    // right after the tap looks like the button did nothing.
                    visors = state.visors.map { v ->
                        if (v.overview.localPk == pk) v.copy(online = false) else v
                    },
                )
            }
        } catch (e: Exception) {
            // The server's own words — "currently disconnected (last seen 40s
            // ago) — retrying" says more than any wording here could.
            mutable.update { it.copy(message = e.message) }
        } finally {
            mutable.update { it.copy(restarting = null) }
        }
    }

    /** The snackbar has shown [FleetUiState.message]; do not show it again. */
    fun messageShown() {
        mutable.update { it.copy(message = null) }
    }

    fun refresh() {
        viewModelScope.launch { loadVisors() }
    }

    /**
     * Name a visor, or clear the name with a blank one. Its own view-model job:
     * a rename must not cancel a restart that is in flight, and vice versa.
     */
    fun rename(pk: String, name: String) {
        viewModelScope.launch { visorNames.setName(pk, name) }
    }

    // --- internals ---

    /** Runs until the core-state collector cancels it. */
    private suspend fun pollVisors() {
        while (true) {
            // Nothing to poll with the ingest off: the list would hold this
            // phone alone, which the screen does not show.
            if (mutable.value.enabled) loadVisors()
            delay(POLL_INTERVAL_MS)
        }
    }

    private suspend fun loadVisors() {
        if (!mutable.value.coreReady) return
        mutable.update { it.copy(loading = it.visors.isEmpty()) }
        try {
            val local = mutable.value.localPk
            val visors = api.visorsSummary()
                // The API's list opens with the visor serving it — this phone.
                // It is not part of anyone's fleet, and it already has a whole
                // screen of its own on the Home tab.
                .filterNot { it.isHypervisor || it.overview.localPk == local }
            mutable.update { it.copy(visors = visors, loading = false, error = null) }
        } catch (e: Exception) {
            mutable.update { it.copy(loading = false, error = e.message) }
        }
    }

    /**
     * One user action at a time, with its failure surfaced on the screen.
     * Launched on the view-model scope rather than inside the state collector,
     * which is cancelled the moment the core state changes — which flipping
     * the toggle is guaranteed to do.
     */
    private fun action(block: suspend () -> Unit) {
        actionJob?.cancel()
        actionJob = viewModelScope.launch {
            mutable.update { it.copy(error = null) }
            try {
                block()
            } catch (e: Exception) {
                mutable.update { it.copy(error = e.message) }
            }
        }
    }

    private companion object {
        const val PING_INTERVAL_MS = 700L

        /**
         * Deliberately slow. Each poll makes the visor fire a Summary RPC to
         * every remote over dmsg, and from a phone those routinely take longer
         * than the server's own 5-second budget for them. Polling faster than
         * they complete stacks calls onto one dmsg stream until it breaks:
         * measured on the emulator at a 5-second cadence, the peer cycled
         * "summary RPC slow (>5s)" → "connection is shut down" → evicted →
         * redialed, roughly once a minute, forever. While it is evicted the
         * row still renders (the server keeps serving it from a fresh cache)
         * but every action against it answers 503 — so the screen looked fine
         * and Restart did not work. Nothing here is second-by-second data.
         */
        const val POLL_INTERVAL_MS = 15_000L
    }
}
