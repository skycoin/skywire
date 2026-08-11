package com.skycoin.skywire.ui.vpn

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.api.AppConnection
import com.skycoin.skywire.api.AppState
import com.skycoin.skywire.api.Overview
import com.skycoin.skywire.api.ServiceEntry
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.core.CoreServiceState
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.SkyVpnService
import com.skycoin.skywire.core.TransportPreference
import com.skycoin.skywire.core.VpnTunnel
import com.skycoin.skywire.core.VpnTunnelState
import com.skycoin.skywire.ui.components.SavedServer
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

/** Everything the SkyVPN screen renders. */
data class VpnUiState(
    val coreState: CoreState = CoreState.Stopped,
    /** The local API answered — without it nothing on this screen works. */
    val apiUp: Boolean = false,
    val app: AppState? = null,
    val connection: AppConnection? = null,
    /** What [SkyVpnService] has done with the phone's network interface. */
    val tunnel: VpnTunnelState = VpnTunnelState(),
    val servers: List<ServiceEntry> = emptyList(),
    val serversLoading: Boolean = false,
    val serversError: String? = null,
    val query: String = "",
    /** Transport type the visor tries first — see [TransportPreference]. */
    val transportPrimary: String = TransportPreference.DEFAULT,
    /**
     * Minimum route hops. Unlike [transportPrimary], which the phone owns and
     * re-pins into the config on every launch, this one is the visor's: it is
     * read from and written to the router settings, the visor persists it, and
     * the phone profile's routing edit preserves whatever it finds. 0 means
     * "not read yet" — the visor's own floor is 1.
     */
    val minHops: Int = 0,
    val killswitch: Boolean = false,
    /** Last summary overview — the device's own address comes off this. */
    val overview: Overview? = null,
    val lastServer: SavedServer? = null,
    /** Bytes this phone has moved through SkyVPN, across all sessions. */
    val lifetimeBytes: Long = 0,
    /** A start/stop/settings call is in flight. */
    val busy: Boolean = false,
    val error: String? = null,
    /**
     * What the visor logged while the last connection attempt was running —
     * the same events `skywire cli proxy start --verbose` prints, scoped to
     * this attempt.
     *
     * A failed connection used to leave the user with a coloured line and no
     * way to find out more, which is where every "it doesn't work" report
     * started and stopped. Collected always, shown on request: the reason
     * belongs on screen, the transcript belongs one tap away.
     */
    val attemptLog: List<String> = emptyList(),
    /** Still collecting — the attempt has not settled either way yet. */
    val attemptLive: Boolean = false,
) {
    val coreReady: Boolean get() = coreState is CoreState.Running && apiUp

    /** The server the app is configured for, whether or not it runs. */
    val selectedPk: String? get() = app?.args?.let(VpnArgs::serverPk) ?: lastServer?.pk

    /**
     * Country of [selectedPk], from the discovery list when it is loaded and
     * otherwise from the saved server — but only when that is the same key,
     * so the flag can never belong to a different exit than the one shown.
     */
    val selectedCountry: String?
        get() = selectedPk?.let { pk ->
            servers.firstOrNull { it.pk == pk }?.geo?.country
                ?: lastServer?.takeIf { it.pk == pk }?.country
        }?.takeIf { it.isNotEmpty() }

    val running: Boolean get() = app?.running == true
    val starting: Boolean get() = app?.status == AppState.STATUS_STARTING
    val errored: Boolean get() = app?.status == AppState.STATUS_ERRORED

    /** The tunnel is up and carrying: the visor says so and traffic is in it. */
    val carrying: Boolean
        get() = running && tunnel.established &&
            app?.detailedStatus != VpnStatus.RECONNECTING

    /**
     * The interface is in place with nothing carrying traffic through it —
     * the killswitch doing its job. Every other app on the phone is offline
     * until the tunnel comes back or the user disconnects.
     */
    val blocked: Boolean get() = tunnel.established && !carrying

    val sessionBytes: Long
        get() = connection?.let { it.bandwidthSent + it.bandwidthReceived } ?: 0

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
 * Drives SkyVPN: list exits from service discovery, put the phone's network
 * interface in place, point vpn-client at an exit and watch what it carries.
 *
 * Two things run in step here and neither works alone. [SkyVpnService] owns
 * the interface — it is the only thing on Android allowed to — and vpn-client
 * owns the tunnel the interface drains into. So the service goes up first
 * (the core asks it for a descriptor mid-handshake and must find it
 * listening) and comes down last.
 */
class VpnViewModel(app: Application) : AndroidViewModel(app) {

    private val api = VisorApi.get(app)
    private val prefs = AppPreferences(app)
    private val json = Json { ignoreUnknownKeys = true }

    private val mutable = MutableStateFlow(VpnUiState())
    val uiState: StateFlow<VpnUiState> = mutable.asStateFlow()

    private var actionJob: Job? = null
    private var attemptLogJob: Job? = null

    /** Session total at the last accrual, to turn the counter into a delta. */
    private var lastSessionBytes = 0L

    /** Accrued but not yet written to disk — see [accrue]. */
    private var unsavedBytes = 0L

    init {
        viewModelScope.launch {
            mutable.update {
                it.copy(
                    lastServer = readLastServer(),
                    killswitch = prefs.boolean(VpnArgs.PREF_KILLSWITCH).first(),
                    lifetimeBytes = prefs.long(KEY_LIFETIME_BYTES).first(),
                    transportPrimary = TransportPreference.sanitize(
                        prefs.string(TransportPreference.PREF_KEY).first(),
                    ),
                )
            }
        }
        viewModelScope.launch {
            VpnTunnel.state.collect { tunnel -> mutable.update { it.copy(tunnel = tunnel) } }
        }
        viewModelScope.launch {
            CoreServiceState.state.collectLatest { core ->
                mutable.update {
                    it.copy(coreState = core, apiUp = false, app = null, connection = null)
                }
                if (core is CoreState.Running) {
                    while (!api.ping()) delay(PING_INTERVAL_MS)
                    mutable.update { it.copy(apiUp = true) }
                    applyStoredKillswitch()
                    runCatching { api.routerSettings().minHops }.getOrNull()?.let { hops ->
                        mutable.update { it.copy(minHops = hops) }
                    }
                    // Once, not per poll: public_ip is the visor's startup
                    // STUN result and STUN is not re-run in steady state, so
                    // re-reading it every two seconds would cost a summary
                    // call to watch a value that cannot change.
                    runCatching { api.summary().overview }.getOrNull()?.let { ov ->
                        mutable.update { it.copy(overview = ov) }
                    }
                    // The API answers well before dmsg has a session, and the
                    // exit list rides dmsg — so the opening fetch gets a few
                    // attempts before it becomes the user's problem.
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

    /** Route the phone through [server]. Consent is the screen's business. */
    fun connect(server: SavedServer) = action { startWith(server) }

    /** Reconnect to whatever the app is already pointed at. */
    fun reconnect() {
        val state = mutable.value
        val pk = state.selectedPk ?: return
        connect(state.lastServer?.takeIf { it.pk == pk } ?: SavedServer(pk))
    }

    /**
     * Stop carrying, then take the interface away. Deliberate, so the
     * killswitch does not apply: it exists to survive a dropped tunnel, not
     * to trap the phone after the user asked to be let out.
     */
    fun disconnect() = action {
        // The user ended the attempt, so there is nothing left to follow. The
        // transcript itself is kept — someone who disconnects a stuck attempt
        // still wants to see why it was stuck.
        attemptLogJob?.cancel()
        mutable.update { it.copy(attemptLive = false) }
        val stopped = runCatching {
            api.updateApp(VpnArgs.APP, status = VisorApi.APP_STOP)
        }.getOrNull()
        SkyVpnService.stop(getApplication())
        flushLifetime()
        lastSessionBytes = 0
        mutable.update { it.copy(app = stopped ?: it.app, connection = null) }
    }

    /**
     * Turn the killswitch on or off.
     *
     * Stored on the phone first: it is the phone's setting, applied to the
     * visor's config whenever the core comes up ([applyStoredKillswitch]), so
     * the choice holds even when it is made with nothing running. Both halves
     * then need it — vpn-client stops restoring traffic between reconnect
     * attempts, and the service stops releasing the interface when the core
     * goes away without a word.
     *
     * A running tunnel is re-dialed rather than PUT in place: the visor
     * restarts the app itself on a killswitch change, and that restart races
     * the old proc exactly as it does on SkySOCKS' port change.
     */
    fun setKillswitch(on: Boolean) = action {
        prefs.putBoolean(VpnArgs.PREF_KILLSWITCH, on)
        mutable.update { it.copy(killswitch = on) }
        if (mutable.value.tunnel.serviceUp) {
            SkyVpnService.setKillswitch(getApplication(), on)
        }
        if (!mutable.value.coreReady) return@action

        val state = mutable.value
        if (!state.running && !state.starting) {
            api.updateApp(VpnArgs.APP, killswitch = on)
            return@action
        }
        val pk = state.selectedPk ?: return@action
        startWith(state.lastServer?.takeIf { it.pk == pk } ?: SavedServer(pk))
    }

    /**
     * Change the minimum number of route hops. Visor-wide, like the transport
     * preference, and applied to the running tunnel by re-dialling it: the
     * setting only takes effect when a route is built, so an established
     * tunnel would otherwise keep the hop count it was dialled with and the
     * screen would claim a privacy property the traffic does not have.
     */
    fun setMinHops(hops: Int) = action {
        val settings = api.setMinHops(hops)
        mutable.update { it.copy(minHops = settings.minHops) }
        val state = mutable.value
        if (state.running || state.starting) {
            val pk = state.selectedPk ?: return@action
            startWith(state.lastServer?.takeIf { it.pk == pk } ?: SavedServer(pk))
        }
    }

    /**
     * Change the transport type the visor tries first. Visor-wide, not
     * SkyVPN-only — SkySOCKS shows and changes the same value.
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

    /** Surface a failure the screen caused (a denied consent, say). */
    fun fail(message: String) {
        mutable.update { it.copy(error = message) }
    }

    override fun onCleared() {
        // Whatever accrued since the last write would otherwise be lost when
        // the screen is left with the tunnel still up.
        viewModelScope.launch { flushLifetime() }
        super.onCleared()
    }

    // --- internals ---

    /**
     * Put the interface in place, point the app at [server], start it.
     *
     * The service goes first and unconditionally: vpn-client asks it for a
     * descriptor while it sets the TUN up, which is a moment after the
     * handshake with the exit — if nothing is listening by then, the dial
     * fails and the retrier backs off for nothing.
     *
     * The stop is unconditional and its failure ignored, for the reason
     * SkySOCKS documents: stopping a stopped app is harmless, while skipping
     * a needed stop is not.
     */
    private suspend fun startWith(server: SavedServer) {
        val killswitch = mutable.value.killswitch
        // Before the app is told to start, or the first events are already
        // gone. The CLI opens its stream ahead of StartAppWithMode for the
        // same reason (see proxy.go) — route setup begins immediately and the
        // interesting part is over in under a second.
        val from = logCursor()
        SkyVpnService.start(getApplication(), killswitch)
        runCatching { api.updateApp(VpnArgs.APP, status = VisorApi.APP_STOP) }
        awaitStopped()
        val updated = api.updateApp(
            VpnArgs.APP,
            pk = server.pk,
            killswitch = killswitch,
            status = VisorApi.APP_START,
        )
        saveLastServer(server)
        lastSessionBytes = 0
        mutable.update {
            it.copy(app = updated, lastServer = server, attemptLog = emptyList(), attemptLive = true)
        }
        collectAttemptLog(from)
    }

    /**
     * Wait for the app to actually be gone before starting it again.
     *
     * Stopping does not wait: the visor pops the process and signals it
     * (procManager.Stop), so the RPC returns while the old vpn-client is still
     * winding down and still holding what it holds. Starting into that window
     * failed with an address already in use, and the user's own fix was to
     * wait about ten seconds and press it again — which is precisely this,
     * minus having to know it.
     *
     * Bounded and best-effort: after [STOP_WAIT_MS] it starts anyway rather
     * than refusing to connect, so the worst case is the behaviour we already
     * had. A failing status read also proceeds — an unreachable visor is not
     * evidence that the old app is still up.
     */
    private suspend fun awaitStopped() {
        val deadline = System.currentTimeMillis() + STOP_WAIT_MS
        while (System.currentTimeMillis() < deadline) {
            val state = runCatching { api.app(VpnArgs.APP) }.getOrNull() ?: return
            if (!state.running && state.status != AppState.STATUS_STARTING) return
            delay(STOP_POLL_MS)
        }
    }

    /**
     * The visor's runtime-log position right now, so the collector can ask for
     * everything after it. Costs one pass of the ring buffer, which is why it
     * is taken once per attempt and not per poll.
     *
     * Failure is not fatal: a missing transcript must never stop a connection
     * from being attempted, so this returns null and the collector stands down.
     */
    private suspend fun logCursor(): Long? =
        runCatching { api.runtimeLogs(since = 0).latest }.getOrNull()

    /**
     * Follow the visor's log from [from] until the attempt settles, and keep
     * what it said.
     *
     * Bounded three ways — the app reaching running or errored, [ATTEMPT_LOG_
     * WINDOW_MS], and [ATTEMPT_LOG_MAX_LINES] — because this runs on a phone
     * and a connection that neither succeeds nor fails is exactly the case
     * worth having a transcript for.
     */
    private fun collectAttemptLog(from: Long?) {
        attemptLogJob?.cancel()
        // Typed non-null, because a smart cast does not reach inside the
        // coroutine below: no cursor means no transcript, and the connection
        // attempt itself carries on regardless.
        val start: Long = from ?: run {
            mutable.update { it.copy(attemptLive = false) }
            return
        }
        attemptLogJob = viewModelScope.launch {
            var since = start
            val deadline = System.currentTimeMillis() + ATTEMPT_LOG_WINDOW_MS
            try {
                while (System.currentTimeMillis() < deadline) {
                    delay(ATTEMPT_LOG_POLL_MS)
                    val delta = runCatching { api.runtimeLogs(since = since) }.getOrNull()
                    if (delta != null) {
                        since = delta.latest
                        val lines = delta.entries.orEmpty()
                        if (lines.isNotEmpty()) {
                            mutable.update {
                                it.copy(attemptLog = (it.attemptLog + lines).takeLast(ATTEMPT_LOG_MAX_LINES))
                            }
                        }
                    }
                    // Settled: running is the happy end, errored is the one
                    // the transcript exists for. Both stop the collection.
                    val app = mutable.value.app
                    if (app?.running == true || app?.status == AppState.STATUS_ERRORED) break
                }
            } finally {
                mutable.update { it.copy(attemptLive = false) }
            }
        }
    }

    /**
     * The phone's killswitch preference is authoritative over whatever the
     * visor's config carries — the same deal [TransportPreference] has. The
     * app is not running yet at this point, so this only rewrites config.
     */
    private suspend fun applyStoredKillswitch() {
        val wanted = mutable.value.killswitch
        runCatching {
            val current = api.app(VpnArgs.APP)
            if (VpnArgs.killswitch(current.args) != wanted) {
                api.updateApp(VpnArgs.APP, killswitch = wanted)
            }
        }
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
     * Fetch the exit list, keeping the spinner up across [attempts] tries.
     * Only the last failure is shown — an intermediate one just means dmsg
     * wasn't ready yet.
     */
    private suspend fun loadServers(attempts: Int = 1) {
        mutable.update { it.copy(serversLoading = true, serversError = null) }
        repeat(attempts) { attempt ->
            try {
                val servers = api.services(VPN_TYPE)
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
                val state = api.app(VpnArgs.APP)
                // Only a running app has connections or stats, and both are
                // best-effort — never let them mask the app state itself.
                val connection = if (state.running) {
                    runCatching { api.appConnections(VpnArgs.APP) }
                        .getOrDefault(emptyList())
                        .firstOrNull()
                } else {
                    null
                }
                mutable.update {
                    it.copy(
                        app = state,
                        connection = connection,
                        // The killswitch is deliberately NOT read back from
                        // the visor's args. The phone owns that preference and
                        // pushes it down (applyStoredKillswitch); reading it
                        // back made this poll overwrite the user's choice with
                        // the visor's not-yet-updated copy, so a freshly
                        // flipped switch flipped itself back within two
                        // seconds and then flipped again once the write
                        // landed. Truth flows one way.
                        //
                        // The error is not cleared here either: a failure
                        // raised by a user's action would be wiped by the very
                        // next poll, which is a two-second window to read why
                        // a connection did not happen. action() clears it when
                        // the next action starts, which is the right moment.
                    )
                }
                accrue(connection)
            } catch (e: Exception) {
                mutable.update { it.copy(error = e.message) }
            }
            delay(POLL_INTERVAL_MS)
        }
    }

    /**
     * Fold this poll's traffic into the lifetime counter.
     *
     * The app's counters restart with every connection, so what is added is
     * the rise since the last poll; a value that went *down* means a new
     * connection started and the whole of it is new. Written to disk in
     * chunks rather than every two seconds — the number is a curiosity, not
     * an accounting record, and losing the last megabyte to a kill is a
     * better trade than a DataStore write per poll for as long as the phone
     * is connected.
     */
    private suspend fun accrue(connection: AppConnection?) {
        if (connection == null) {
            flushLifetime()
            lastSessionBytes = 0
            return
        }
        val session = connection.bandwidthSent + connection.bandwidthReceived
        val delta = if (session >= lastSessionBytes) session - lastSessionBytes else session
        lastSessionBytes = session
        if (delta <= 0) return

        unsavedBytes += delta
        val total = mutable.value.lifetimeBytes + delta
        mutable.update { it.copy(lifetimeBytes = total) }
        if (unsavedBytes >= PERSIST_EVERY_BYTES) flushLifetime()
    }

    private suspend fun flushLifetime() {
        if (unsavedBytes <= 0) return
        unsavedBytes = 0
        runCatching { prefs.putLong(KEY_LIFETIME_BYTES, mutable.value.lifetimeBytes) }
    }

    private suspend fun readLastServer(): SavedServer? =
        prefs.string(VpnArgs.PREF_LAST_SERVER).first()?.let { stored ->
            runCatching { json.decodeFromString(SavedServer.serializer(), stored) }.getOrNull()
        }

    private suspend fun saveLastServer(server: SavedServer) {
        prefs.putString(
            VpnArgs.PREF_LAST_SERVER,
            json.encodeToString(SavedServer.serializer(), server),
        )
    }

    private companion object {
        /** SD's own filter value for the SkyVPN server family. */
        const val VPN_TYPE = "vpn"
        const val KEY_LIFETIME_BYTES = "vpn_lifetime_bytes"
        const val PING_INTERVAL_MS = 700L
        const val POLL_INTERVAL_MS = 2_000L
        const val INITIAL_LOAD_ATTEMPTS = 3
        const val RETRY_DELAY_MS = 5_000L
        const val PERSIST_EVERY_BYTES = 8L * 1024 * 1024

        /**
         * How long to keep following the log after a connect. Route setup is
         * seconds; this covers a retry or two and then gives up, so a dial
         * that hangs still leaves a transcript instead of an endless poll.
         */
        const val ATTEMPT_LOG_WINDOW_MS = 45_000L
        const val ATTEMPT_LOG_POLL_MS = 700L

        /** Enough for a full setup sequence with retries, bounded for a phone. */
        const val ATTEMPT_LOG_MAX_LINES = 400

        /**
         * How long to let the previous vpn-client finish stopping. Ten
         * seconds because that is what the workaround needed; teardown is
         * normally well under one, and the poll below leaves as soon as the
         * app reports stopped.
         */
        const val STOP_WAIT_MS = 10_000L
        const val STOP_POLL_MS = 250L
    }
}
