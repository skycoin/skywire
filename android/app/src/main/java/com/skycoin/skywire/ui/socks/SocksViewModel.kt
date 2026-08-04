package com.skycoin.skywire.ui.socks

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.api.AppConnection
import com.skycoin.skywire.api.AppState
import com.skycoin.skywire.api.ServiceEntry
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.core.CoreServiceState
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.TransportPreference
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json

/** Everything the SkySOCKS screen renders. */
data class SocksUiState(
    val coreState: CoreState = CoreState.Stopped,
    /** The local API answered — without it nothing on this screen works. */
    val apiUp: Boolean = false,
    val app: AppState? = null,
    val connection: AppConnection? = null,
    val servers: List<ServiceEntry> = emptyList(),
    val serversLoading: Boolean = false,
    val serversError: String? = null,
    val query: String = "",
    val listenPort: Int = SocksArgs.DEFAULT_PORT,
    /** Transport type the visor tries first — see [TransportPreference]. */
    val transportPrimary: String = TransportPreference.DEFAULT,
    val lastServer: SavedServer? = null,
    /** A start/stop/settings call is in flight. */
    val busy: Boolean = false,
    val error: String? = null,
) {
    val coreReady: Boolean get() = coreState is CoreState.Running && apiUp

    /** The server the app is configured for, whether or not it runs. */
    val selectedPk: String? get() = app?.args?.let(SocksArgs::serverPk) ?: lastServer?.pk

    /**
     * Country of [selectedPk], from the discovery list when it is loaded
     * and otherwise from the saved server — but only when that is the
     * same key, so the flag can never belong to a different server than
     * the one shown next to it.
     */
    val selectedCountry: String?
        get() = selectedPk?.let { pk ->
            servers.firstOrNull { it.pk == pk }?.geo?.country
                ?: lastServer?.takeIf { it.pk == pk }?.country
        }?.takeIf { it.isNotEmpty() }

    val running: Boolean get() = app?.running == true
    val starting: Boolean get() = app?.status == AppState.STATUS_STARTING
    val errored: Boolean get() = app?.status == AppState.STATUS_ERRORED

    val listenAddress: String get() = "${SocksArgs.HOST}:$listenPort"

    val filteredServers: List<ServiceEntry>
        get() = if (query.isBlank()) {
            servers
        } else {
            servers.filter {
                it.pk.contains(query, true) ||
                    it.geo?.country.orEmpty().contains(query, true) ||
                    it.geo?.region.orEmpty().contains(query, true) ||
                    it.version.contains(query, true)
            }
        }
}

/**
 * Drives the whole SkySOCKS flow: list servers from service discovery,
 * point skysocks-client at one, start/stop it, and watch what it reports.
 *
 * Everything hangs off the core's state — the local API only exists while
 * the visor runs, so polling starts when it comes up and the screen falls
 * back to an explanatory state when it doesn't. Every write goes through
 * [update]: these loaders suspend, and a read-modify-write spanning a
 * suspension point silently drops whatever landed in the meantime.
 */
class SocksViewModel(app: Application) : AndroidViewModel(app) {

    private val api = VisorApi.get(app)
    private val prefs = AppPreferences(app)
    private val json = Json { ignoreUnknownKeys = true }

    private val mutable = MutableStateFlow(SocksUiState())
    val uiState: StateFlow<SocksUiState> = mutable.asStateFlow()

    private var actionJob: Job? = null

    init {
        viewModelScope.launch {
            val saved = readLastServer()
            mutable.update { it.copy(lastServer = saved) }
        }
        viewModelScope.launch {
            val primary = TransportPreference.sanitize(
                prefs.string(TransportPreference.PREF_KEY).first(),
            )
            mutable.update { it.copy(transportPrimary = primary) }
        }
        viewModelScope.launch {
            CoreServiceState.state.collectLatest { core ->
                mutable.update {
                    it.copy(coreState = core, apiUp = false, app = null, connection = null)
                }
                if (core is CoreState.Running) {
                    while (!api.ping()) delay(PING_INTERVAL_MS)
                    mutable.update { it.copy(apiUp = true) }
                    // The API answers well before dmsg has a session, and
                    // the server list rides dmsg — so the opening fetch
                    // gets a few attempts before it becomes the user's
                    // problem to press Retry.
                    loadServers(attempts = INITIAL_LOAD_ATTEMPTS)
                    pollAppState()
                }
            }
        }
    }

    fun setQuery(query: String) {
        mutable.update { it.copy(query = query) }
    }

    fun refreshServers() {
        viewModelScope.launch { loadServers() }
    }

    /** Point the app at [server] and make sure it runs. */
    fun connect(server: SavedServer) = action { startWith(server) }

    /**
     * The one-tap shortcut: reconnect to whatever the app is already
     * pointed at — the saved server, or (after an app reinstall that kept
     * the visor's config) the key still in its argv.
     */
    fun reconnect() {
        val state = mutable.value
        val pk = state.selectedPk ?: return
        connect(state.lastServer?.takeIf { it.pk == pk } ?: SavedServer(pk))
    }

    fun disconnect() = action {
        val updated = api.updateApp(SocksArgs.APP, status = VisorApi.APP_STOP)
        mutable.update { it.copy(app = updated, connection = null) }
    }

    /**
     * Change the port the SOCKS5 listener binds on loopback. The whole
     * argv is rewritten (that is the API's only shape for it), so it is
     * read fresh here rather than from the polled snapshot.
     *
     * A running app is stopped around the change instead of leaning on the
     * server's restart-on-args-change: that restart races the old proc,
     * whose blocked `accept` returns "use of closed network connection" as
     * the app's error and leaves it stuck in Errored. Stop, rewrite, start
     * lands on the new port every time.
     */
    fun setListenPort(port: Int) = action {
        // Read the app rather than trusting the polled snapshot — this
        // decides both the new argv and whether to put the app back up.
        val current = api.app(SocksArgs.APP)
        if (current.running) runCatching { api.updateApp(SocksArgs.APP, status = VisorApi.APP_STOP) }
        var updated = api.updateApp(SocksArgs.APP, args = SocksArgs.withPort(current.args, port))
        if (current.running) updated = api.updateApp(SocksArgs.APP, status = VisorApi.APP_START)
        mutable.update {
            it.copy(app = updated, listenPort = SocksArgs.listenPort(updated.args))
        }
    }

    /**
     * Change the transport type the visor tries first. Visor-wide, not
     * SkySOCKS-only — SkyVPN will read the same value.
     *
     * Saved locally first: the phone profile writes it into the config on
     * every launch ([TransportPreference]), so the choice holds even when
     * it is made with the core down and there is nothing to PUT. When the
     * core *is* up the visor takes it live, and a running client is
     * re-dialed so the new route is actually built over the new primary —
     * the setting only steers route setup, so an established route keeps
     * whatever transport it already rides.
     */
    fun setTransportPrimary(type: String) = action {
        prefs.putString(TransportPreference.PREF_KEY, type)
        mutable.update { it.copy(transportPrimary = type) }
        if (!mutable.value.coreReady) return@action
        api.setTransportPreference(TransportPreference.order(type))
        val state = mutable.value
        if (state.running || state.starting) {
            val pk = state.selectedPk ?: return@action
            startWith(state.lastServer?.takeIf { it.pk == pk } ?: SavedServer(pk))
        }
    }

    // --- internals ---

    /**
     * Point the app at [server] and start it.
     *
     * The stop is unconditional and its failure ignored: stopping an
     * already-stopped app is the harmless half of the trade, while
     * *skipping* a needed stop is not — starting a running app is a
     * server-side error, and the polled snapshot can be a poll behind
     * what the visor actually has. With the app reliably stopped, the
     * single PUT below is valid in every case: the key just rewrites the
     * argv, then the status starts it with that key in place.
     *
     * A plain suspend function, not another [action]: the callers already
     * run inside one, and re-entering `action` from within would cancel
     * the very job making the call.
     */
    private suspend fun startWith(server: SavedServer) {
        runCatching { api.updateApp(SocksArgs.APP, status = VisorApi.APP_STOP) }
        val updated = api.updateApp(
            SocksArgs.APP,
            pk = server.pk,
            status = VisorApi.APP_START,
        )
        saveLastServer(server)
        mutable.update { it.copy(app = updated, lastServer = server) }
    }

    /**
     * One user action at a time, with its failure surfaced on the screen.
     * Launched on the view-model scope rather than inside the state
     * collector, which is cancelled the moment the core state changes.
     */
    private fun action(block: suspend () -> Unit) {
        actionJob?.cancel()
        actionJob = viewModelScope.launch {
            mutable.update { it.copy(busy = true, error = null) }
            try {
                block()
            } catch (e: Exception) {
                mutable.update { it.copy(error = e.message) }
            } finally {
                mutable.update { it.copy(busy = false) }
            }
        }
    }

    /**
     * Fetch the server list, keeping the spinner up across [attempts]
     * tries. Only the last failure is shown — an intermediate one just
     * means dmsg wasn't ready yet.
     */
    private suspend fun loadServers(attempts: Int = 1) {
        mutable.update { it.copy(serversLoading = true, serversError = null) }
        repeat(attempts) { attempt ->
            try {
                // Service discovery is reached over dmsg; the list is then
                // held in state and only refetched on demand.
                val servers = api.services(PROXY_TYPE)
                mutable.update { it.copy(servers = servers, serversLoading = false) }
                return
            } catch (e: Exception) {
                if (attempt == attempts - 1) {
                    mutable.update { it.copy(serversLoading = false, serversError = e.message) }
                } else {
                    delay(RETRY_DELAY_MS)
                }
            }
        }
    }

    /** Runs until the core-state collector cancels it. */
    private suspend fun pollAppState() {
        while (true) {
            try {
                val state = api.app(SocksArgs.APP)
                // Only a running app has connections, and that summary is
                // best-effort — never let it mask the app state itself.
                val connection = if (state.running) {
                    runCatching { api.appConnections(SocksArgs.APP) }
                        .getOrDefault(emptyList())
                        .firstOrNull()
                } else {
                    null
                }
                mutable.update {
                    it.copy(
                        app = state,
                        connection = connection,
                        listenPort = SocksArgs.listenPort(state.args),
                        error = null,
                    )
                }
            } catch (e: Exception) {
                mutable.update { it.copy(error = e.message) }
            }
            delay(POLL_INTERVAL_MS)
        }
    }

    private suspend fun readLastServer(): SavedServer? =
        prefs.string(KEY_LAST_SERVER).first()?.let { stored ->
            runCatching { json.decodeFromString(SavedServer.serializer(), stored) }.getOrNull()
        }

    private suspend fun saveLastServer(server: SavedServer) {
        prefs.putString(KEY_LAST_SERVER, json.encodeToString(SavedServer.serializer(), server))
    }

    private companion object {
        /** SD's own filter value; "proxy" and "skysocks" are the same family. */
        const val PROXY_TYPE = "proxy"
        const val KEY_LAST_SERVER = "socks_last_server"
        const val PING_INTERVAL_MS = 700L
        const val POLL_INTERVAL_MS = 2_000L
        const val INITIAL_LOAD_ATTEMPTS = 3
        const val RETRY_DELAY_MS = 5_000L
    }
}
