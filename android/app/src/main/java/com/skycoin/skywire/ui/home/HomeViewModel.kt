package com.skycoin.skywire.ui.home

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.api.AuthFailedException
import com.skycoin.skywire.api.ServiceHealthEntry
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.api.VisorSummary
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.core.AppVisibility
import com.skycoin.skywire.core.BatteryOptimization
import com.skycoin.skywire.core.ConfigManager
import com.skycoin.skywire.core.CoreServiceState
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.SecretStore
import com.skycoin.skywire.core.SkywireCoreService
import com.skycoin.skywire.core.SkywirePaths
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

/** Everything the Home tab renders. */
data class HomeUiState(
    val coreState: CoreState = CoreState.Stopped,
    /** The local API answered /api/ping. */
    val apiUp: Boolean = false,
    val summary: VisorSummary? = null,
    val serviceHealth: List<ServiceHealthEntry> = emptyList(),
    val error: String? = null,
    /** Doze will pause this app's network; the user has not been asked yet. */
    val offerBatteryExemption: Boolean = false,
) {
    val connected: Boolean get() = coreState is CoreState.Running && apiUp
}

/**
 * Drives Connect/Disconnect and, while the core runs, polls the local API
 * for the visor-info card. Polling is tied to the core state: the collector
 * restarts (and the card clears) whenever the service reports a new phase.
 */
class HomeViewModel(app: Application) : AndroidViewModel(app) {

    private val api = VisorApi.get(app)
    private val prefs = AppPreferences(app)
    private val live = MutableStateFlow(LiveData())
    private var authResetAttempted = false

    /**
     * Whether to offer the battery-optimisation exemption on this screen.
     * Re-evaluated on every return to the foreground, because the answer is
     * held by the system and can be changed in a screen that is not ours.
     */
    private val batteryOffer = MutableStateFlow(false)

    private data class LiveData(
        val apiUp: Boolean = false,
        val summary: VisorSummary? = null,
        val serviceHealth: List<ServiceHealthEntry> = emptyList(),
        val error: String? = null,
    )

    val uiState: StateFlow<HomeUiState> =
        combine(
            CoreServiceState.state,
            live.asStateFlow(),
            batteryOffer.asStateFlow(),
        ) { core, data, offerBattery ->
            HomeUiState(
                coreState = core,
                apiUp = data.apiUp,
                summary = data.summary,
                serviceHealth = data.serviceHealth,
                error = data.error,
                // Only once the core is actually up: before that the question
                // is about a background problem the user has not got yet.
                offerBatteryExemption = offerBattery && core is CoreState.Running,
            )
        }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), HomeUiState())

    init {
        // The exemption is granted in a system screen and the prompt can be
        // silenced from Settings, so neither answer is ours to cache. Both are
        // re-read on every resume — the system's grant dialog covers the
        // Activity without stopping it, so the foreground flag alone would
        // miss the moment the answer changed.
        viewModelScope.launch {
            combine(
                AppVisibility.isForeground,
                AppVisibility.resumes,
                prefs.boolean(BatteryOptimization.PREF_DISMISSED, false),
            ) { foreground, _, dismissed -> foreground && !dismissed }
                .collectLatest { askable ->
                    batteryOffer.value =
                        askable && !BatteryOptimization.isExempt(getApplication())
                }
        }
        viewModelScope.launch {
            CoreServiceState.state.collectLatest { core ->
                live.value = LiveData()
                if (core is CoreState.Running) pollWhileRunning()
            }
        }
    }

    /** "Not now" on the Home prompt — silences it here and in Settings. */
    fun dismissBatteryPrompt() {
        viewModelScope.launch { prefs.putBoolean(BatteryOptimization.PREF_DISMISSED, true) }
    }

    fun requestBatteryExemption() {
        BatteryOptimization.openRequest(getApplication())
    }

    fun connect() {
        live.value = LiveData()
        SkywireCoreService.start(getApplication())
    }

    fun disconnect() {
        SkywireCoreService.stop(getApplication())
    }

    /**
     * Runs until the collector above cancels it (core left Running). Session
     * bootstrap is implicit: the API client re-logins on any 401, so a
     * transient failure here just retries on the next tick instead of
     * disabling the card for the rest of the run.
     */
    private suspend fun pollWhileRunning() {
        while (!api.ping()) delay(PING_INTERVAL_MS)
        live.value = live.value.copy(apiUp = true)
        while (true) {
            try {
                // The dmsg-server list rides along in the summary
                // (`dmsg_servers`) — see DmsgServerInfo for why the
                // dedicated /api/dmsg route is not the source here.
                val summary = api.summary()
                val health = runCatching { api.serviceHealth() }.getOrDefault(emptyList())
                live.value = LiveData(
                    apiUp = true,
                    summary = summary,
                    serviceHealth = health,
                )
            } catch (e: AuthFailedException) {
                launchAuthRecovery(e)
                return
            } catch (e: Exception) {
                live.value = live.value.copy(error = e.message)
            }
            delay(REFRESH_INTERVAL_MS)
        }
    }

    /**
     * The stored password no longer opens the visor's account DB (device
     * keystore rotated under a surviving data dir). Recovery: stop the core,
     * drop users.db, start again — the bootstrap then re-creates the account
     * with the current password. Tried once per process.
     *
     * The restart runs on the service's own process-scoped job, so stopping
     * the core — which cancels [pollWhileRunning] and everything called from
     * it — cannot strand the phone between the stop and the start.
     */
    private fun launchAuthRecovery(cause: AuthFailedException) {
        if (authResetAttempted) {
            live.value = live.value.copy(error = cause.message)
            return
        }
        authResetAttempted = true
        val app = getApplication<Application>()
        SkywireCoreService.restart(app) {
            ConfigManager(SkywirePaths(app), SecretStore(app)).deleteUsersDb()
        }
    }

    private companion object {
        const val PING_INTERVAL_MS = 700L
        const val REFRESH_INTERVAL_MS = 4_000L
    }
}
