package com.skycoin.skywire.ui.settings

import android.app.Application
import android.net.Uri
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.R
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.core.AppLock
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.core.AppVisibility
import com.skycoin.skywire.core.BatteryOptimization
import com.skycoin.skywire.core.ConfigManager
import com.skycoin.skywire.core.ConfigVault
import com.skycoin.skywire.core.CoreServiceState
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.PublicAutoconnect
import com.skycoin.skywire.core.SecretStore
import com.skycoin.skywire.core.SkywireCoreService
import com.skycoin.skywire.core.SkywirePaths
import com.skycoin.skywire.core.ThemeMode
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Everything the Settings tab renders. */
data class SettingsUiState(
    val coreState: CoreState = CoreState.Stopped,
    /** This visor's key, from the config on disk — shown with the core down too. */
    val publicKey: String = "",
    val appLockEnabled: Boolean = AppLock.DEFAULT,
    /** Automatic transports to public visors — see [PublicAutoconnect]. */
    val publicAutoconnect: Boolean = PublicAutoconnect.DEFAULT,
    /** False when the phone has no screen lock and no enrolled biometric. */
    val biometricsAvailable: Boolean = true,
    val themeMode: ThemeMode = ThemeMode.SYSTEM,
    /** The visor config is encrypted at rest while the core is down. */
    val configEncrypted: Boolean = ConfigVault.DEFAULT,
    /** Whether Doze has been told to leave this app's network alone. */
    val batteryExempt: Boolean = true,
    /** The user has already declined the exemption once; stop offering. */
    val batteryPromptDismissed: Boolean = false,
    val appVersion: String = "",
    /** From the running visor's own summary; empty while the core is down. */
    val coreVersion: String = "",
    /** An identity operation is in flight; the core is coming down and back. */
    val busy: Boolean = false,
    /** One-shot feedback for the snackbar. */
    val message: String? = null,
) {
    /** No config yet — the core has never successfully generated one. */
    val hasIdentity: Boolean get() = publicKey.isNotEmpty()
}

/**
 * Settings: identity, config export, the app lock, and the small preferences
 * that had nowhere else to live.
 *
 * Every identity operation runs the same way — stop the core, change the
 * config, start it again — and every one of them runs through
 * [SkywireCoreService.restart], on the service's own process-scoped job.
 * That is not a convenience: replacing a key while the visor holds the config
 * open would have it write its own copy back over the new one, and doing the
 * work on this view model's scope would let a back press cancel it between the
 * stop and the start, leaving the phone with no core at all.
 */
class SettingsViewModel(app: Application) : AndroidViewModel(app) {

    private val prefs = AppPreferences(app)
    private val paths = SkywirePaths(app)
    private val config = ConfigManager(paths, SecretStore(app))
    private val vault = ConfigVault(paths)
    private val api = VisorApi.get(app)

    private val mutable = MutableStateFlow(SettingsUiState())
    val uiState: StateFlow<SettingsUiState> = mutable.asStateFlow()

    private var actionJob: Job? = null

    init {
        viewModelScope.launch {
            CoreServiceState.state.collectLatest { core ->
                mutable.update { it.copy(coreState = core) }
                // The key is read from disk on every core transition rather
                // than once: an identity operation ends in one of these, and
                // this is what makes the new key appear without a refresh.
                loadIdentity()
                if (core is CoreState.Running) loadCoreVersion()
            }
        }
        viewModelScope.launch {
            prefs.boolean(AppLock.PREF_KEY, AppLock.DEFAULT).collectLatest { enabled ->
                mutable.update { it.copy(appLockEnabled = enabled) }
            }
        }
        viewModelScope.launch {
            prefs.boolean(PublicAutoconnect.PREF_KEY, PublicAutoconnect.DEFAULT)
                .collectLatest { enabled ->
                    mutable.update { it.copy(publicAutoconnect = enabled) }
                }
        }
        viewModelScope.launch {
            prefs.string(ThemeMode.PREF_KEY).collectLatest { stored ->
                mutable.update { it.copy(themeMode = ThemeMode.of(stored)) }
            }
        }
        viewModelScope.launch {
            prefs.boolean(ConfigVault.PREF_KEY, ConfigVault.DEFAULT).collectLatest { on ->
                mutable.update { it.copy(configEncrypted = on) }
            }
        }
        viewModelScope.launch {
            prefs.boolean(BatteryOptimization.PREF_DISMISSED, false).collectLatest { dismissed ->
                mutable.update { it.copy(batteryPromptDismissed = dismissed) }
            }
        }
        // The exemption is granted in a system screen, not in this app, so the
        // only reliable moment to re-read it is when the user comes back from
        // there. That has to be every resume, not every return to the
        // foreground: the system's own dialog covers the Activity without
        // stopping it, so a grant made there never changes the foreground
        // flag and the card would keep offering until the app restarts.
        viewModelScope.launch {
            AppVisibility.resumes.collectLatest { refreshBatteryExemption() }
        }
        refreshBatteryExemption()
        viewModelScope.launch { loadVersions() }
    }

    // --- config at rest ---

    /**
     * Turn encryption of the config on or off.
     *
     * Turning it **on** with the core running only records the choice: the
     * visor holds that file open and rewrites it, so the sealing happens when
     * it next exits (see [SkywireCoreService]). Turning it **off** unseals
     * straight away — a user who just switched encryption off should not be
     * left with an encrypted config until the next disconnect.
     */
    fun setConfigEncrypted(enabled: Boolean) = action {
        val running = CoreServiceState.state.value is CoreState.Running
        vault.applyPreference(enabled, running).getOrThrow()
        prefs.putBoolean(ConfigVault.PREF_KEY, enabled)
        val message = when {
            !enabled -> R.string.settings_encrypt_off_done
            running -> R.string.settings_encrypt_on_pending
            else -> R.string.settings_encrypt_on_done
        }
        mutable.update { it.copy(message = getApplication<Application>().getString(message)) }
    }

    // --- battery ---

    fun refreshBatteryExemption() {
        val exempt = BatteryOptimization.isExempt(getApplication())
        mutable.update { it.copy(batteryExempt = exempt) }
    }

    /**
     * Hand the user to the system's own dialog. Nothing is recorded here: the
     * answer lives in the platform, and [refreshBatteryExemption] reads it back
     * when they return.
     */
    fun requestBatteryExemption() {
        val context = getApplication<Application>()
        if (!BatteryOptimization.openRequest(context)) {
            report(context.getString(R.string.settings_battery_no_screen))
        }
    }

    /** "Not now" — stop offering, in Settings and on Home. */
    fun dismissBatteryPrompt() {
        viewModelScope.launch { prefs.putBoolean(BatteryOptimization.PREF_DISMISSED, true) }
    }

    // --- identity ---

    /**
     * Install [secretKey] as this visor's identity. The key is validated (and
     * its public half derived) by the core binary before anything is touched —
     * see [ConfigManager.derivePublicKey] — so the confirmation the user saw
     * already knew the answer.
     */
    fun replaceSecretKey(secretKey: String) = identityAction(R.string.settings_sk_replaced) {
        config.replaceSecretKey(secretKey).getOrThrow()
    }

    /** Throw the identity away and let the first-run pipeline make a new one. */
    fun newIdentity() = identityAction(R.string.settings_identity_reset) {
        config.resetIdentity()
        ""
    }

    /**
     * Derive [secretKey]'s public key, for the confirmation dialog. Returns
     * null with the failure in [SettingsUiState.message] when the core will
     * not read it.
     */
    suspend fun publicKeyOf(secretKey: String): String? =
        config.derivePublicKey(secretKey)
            .onFailure { e -> mutable.update { it.copy(message = e.message) } }
            .getOrNull()

    /** The pasted key is the one already installed — nothing to do. */
    fun isCurrentKey(publicKey: String): Boolean =
        publicKey.isNotEmpty() && publicKey == mutable.value.publicKey

    // --- export ---

    /**
     * Write the complete config — secret key included — to the document the
     * user picked. Deliberately the whole file: a config that has had anything
     * removed is not a backup, and this is the only way the key leaves the
     * phone at all.
     */
    fun exportConfig(uri: Uri) = action {
        withContext(Dispatchers.IO) {
            val json = config.configJson()
            val resolver = getApplication<Application>().contentResolver
            // "wt" truncates: the picker will happily hand back an existing
            // document, and a shorter config written into a longer one leaves
            // the tail of the old file behind.
            resolver.openOutputStream(uri, "wt")?.use { out ->
                out.write(json.toByteArray(Charsets.UTF_8))
            } ?: error("could not open the chosen file for writing")
        }
        mutable.update {
            it.copy(message = getApplication<Application>().getString(R.string.settings_export_done))
        }
    }

    // --- preferences ---

    /**
     * The lock is turned on only after a check has passed (the screen asks
     * first), so the app is unlocked by definition at that moment — say so,
     * rather than dropping the user onto a lock screen they just satisfied.
     */
    fun setAppLock(enabled: Boolean) {
        viewModelScope.launch {
            prefs.putBoolean(AppLock.PREF_KEY, enabled)
            if (enabled) AppLock.unlock()
        }
    }

    /**
     * Turn automatic transports to public visors on or off.
     *
     * Stored first so the choice holds with the core down, and applied by the
     * config rewrite on the next core start — the visor reads this when it
     * builds its transport manager, so a running core keeps its old answer
     * until it is restarted. Same deal as [Fleet].
     */
    fun setPublicAutoconnect(enabled: Boolean) {
        viewModelScope.launch { prefs.putBoolean(PublicAutoconnect.PREF_KEY, enabled) }
    }

    fun setBiometricsAvailable(available: Boolean) {
        mutable.update { it.copy(biometricsAvailable = available) }
    }

    fun setThemeMode(mode: ThemeMode) {
        viewModelScope.launch { prefs.putString(ThemeMode.PREF_KEY, mode.name) }
    }

    fun messageShown() {
        mutable.update { it.copy(message = null) }
    }

    fun report(message: String) {
        mutable.update { it.copy(message = message) }
    }

    // --- internals ---

    private suspend fun loadIdentity() {
        val pk = withContext(Dispatchers.IO) { config.publicKey().orEmpty() }
        mutable.update { it.copy(publicKey = pk) }
    }

    private suspend fun loadVersions() {
        val app = getApplication<Application>()
        val appVersion = withContext(Dispatchers.IO) {
            runCatching {
                app.packageManager.getPackageInfo(app.packageName, 0).versionName.orEmpty()
            }.getOrDefault("")
        }
        mutable.update { it.copy(appVersion = appVersion) }
    }

    /**
     * The core's version, asked of the running visor rather than of the binary.
     *
     * `libskywire-mobile.so --version` would answer with the core down too,
     * which is tempting — but the CLI writes to stdout before any command
     * runs, so scraping it means parsing whatever else happened to be printed
     * that launch. The visor's summary is the same binary reporting itself,
     * over a typed API. With the core down the row simply reads "—".
     */
    private suspend fun loadCoreVersion() {
        // The API is not up the instant the process is. Waiting here is safe:
        // this runs inside the core-state collector, which cancels it the
        // moment the core moves on.
        while (!api.ping()) delay(PING_INTERVAL_MS)
        val version = runCatching {
            api.summary().let { summary ->
                listOfNotNull(
                    summary.overview.buildInfo?.version?.takeIf { it.isNotEmpty() },
                    summary.buildTag.takeIf { it.isNotEmpty() },
                ).joinToString(" · ")
            }
        }.getOrDefault("")
        mutable.update { it.copy(coreVersion = version) }
    }

    /**
     * The shape every identity change takes: the work happens with the core
     * down, and the caller follows it through [CoreServiceState] like any other
     * lifecycle change.
     *
     * The result comes back through a [CompletableDeferred] rather than being
     * returned, because [SkywireCoreService.restart] runs [block] on a
     * process-scoped job on purpose. Awaiting it here is safe in the other
     * direction too: if this view model is cleared mid-restart the await is
     * cancelled, but the work is not — only the message is lost.
     */
    private fun identityAction(successMessage: Int, block: suspend () -> String) = action {
        mutable.update { it.copy(busy = true) }
        try {
            val outcome = CompletableDeferred<Result<String>>()
            val running = mutable.value.coreState !is CoreState.Stopped &&
                mutable.value.coreState !is CoreState.Failed
            if (running) {
                SkywireCoreService.restart(getApplication()) {
                    outcome.complete(runCatching { block() })
                }
            } else {
                // Nothing to restart. The next Connect picks the new identity
                // up through the ordinary first-run path.
                outcome.complete(runCatching { withContext(Dispatchers.IO) { block() } })
            }
            outcome.await().getOrThrow()
            // The API client caches this visor's key for the life of the
            // process, and it is now the key of a visor that no longer exists.
            api.forgetIdentity()
            loadIdentity()
            mutable.update {
                it.copy(message = getApplication<Application>().getString(successMessage))
            }
        } finally {
            mutable.update { it.copy(busy = false) }
        }
    }

    private companion object {
        const val PING_INTERVAL_MS = 700L
    }

    /** One user action at a time, with its failure surfaced on the screen. */
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
