package com.skycoin.skywire.ui.settings

import android.app.Application
import android.net.Uri
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.R
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.core.CoreLogLevel
import com.skycoin.skywire.core.CoreServiceState
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.DiagnosticsExport
import com.skycoin.skywire.core.SkychatProfile
import com.skycoin.skywire.core.SkydexProfile
import com.skycoin.skywire.core.SkywireCoreService
import com.skycoin.skywire.ui.socks.SocksArgs
import com.skycoin.skywire.ui.vpn.VpnArgs
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

data class DiagnosticsUiState(
    val coreState: CoreState = CoreState.Stopped,
    val logLevel: String = CoreLogLevel.DEFAULT,
    /** An export is being written to the document the user picked. */
    val exporting: Boolean = false,
    val message: String? = null,
) {
    /** The level change needs a restart, and a restart in flight is not a moment to ask for another. */
    val coreCycling: Boolean
        get() = coreState is CoreState.Starting ||
            coreState is CoreState.Stopping ||
            coreState is CoreState.Restarting
}

/**
 * Logs & diagnostics: every source in one place, the whole set as one file,
 * and the one setting that decides how much of it there is.
 */
class DiagnosticsViewModel(app: Application) : AndroidViewModel(app) {

    private val prefs = AppPreferences(app)
    private val mutable = MutableStateFlow(DiagnosticsUiState())
    val uiState: StateFlow<DiagnosticsUiState> = mutable.asStateFlow()

    private var actionJob: Job? = null

    /**
     * The four client apps, in the order the hub lists them. Named from each
     * screen's own constant rather than a second list here — a diagnostics
     * bundle that collects a log for an app name nothing else uses would be
     * silently empty.
     */
    val apps = listOf(
        SkychatProfile.APP,
        SocksArgs.APP,
        VpnArgs.APP,
        SkydexProfile.APP,
    )

    init {
        viewModelScope.launch {
            CoreServiceState.state.collectLatest { core ->
                mutable.update { it.copy(coreState = core) }
            }
        }
        viewModelScope.launch {
            prefs.string(CoreLogLevel.PREF_KEY).collectLatest { stored ->
                mutable.update { it.copy(logLevel = CoreLogLevel.sanitize(stored)) }
            }
        }
    }

    /**
     * Set how much the visor logs. Saved first so the choice survives a core
     * that is down; when it is up, the restart is what applies it — the visor
     * reads `log_level` once, while it builds its module graph.
     */
    fun setLogLevel(level: String) = action {
        prefs.putString(CoreLogLevel.PREF_KEY, CoreLogLevel.sanitize(level))
        val core = mutable.value.coreState
        if (core is CoreState.Stopped || core is CoreState.Failed) return@action
        // Detached on purpose: leaving this screen must not cancel a restart
        // halfway and leave the phone with no core.
        SkywireCoreService.restart(getApplication())
    }

    /** Write the whole bundle into the document the user picked. */
    fun export(uri: Uri) = action {
        mutable.update { it.copy(exporting = true) }
        try {
            withContext(Dispatchers.IO) {
                val resolver = getApplication<Application>().contentResolver
                resolver.openOutputStream(uri, "wt")?.use { out ->
                    DiagnosticsExport.writeTo(getApplication(), out, apps)
                } ?: error(
                    getApplication<Application>().getString(R.string.diag_export_unwritable),
                )
            }
            mutable.update {
                it.copy(message = getApplication<Application>().getString(R.string.diag_export_done))
            }
        } finally {
            mutable.update { it.copy(exporting = false) }
        }
    }

    fun messageShown() {
        mutable.update { it.copy(message = null) }
    }

    private fun action(block: suspend () -> Unit) {
        actionJob?.cancel()
        actionJob = viewModelScope.launch {
            try {
                block()
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                mutable.update { it.copy(message = e.message ?: e::class.java.simpleName) }
            }
        }
    }
}
