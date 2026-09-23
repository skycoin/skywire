package com.skycoin.skywire.ui.settings

import android.app.Application
import android.net.Uri
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.BuildConfig
import com.skycoin.skywire.R
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.core.AppLanguage
import com.skycoin.skywire.core.AppLocale
import com.skycoin.skywire.core.AppLock
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.core.AppUpdates
import com.skycoin.skywire.core.AppVisibility
import com.skycoin.skywire.core.BatteryOptimization
import com.skycoin.skywire.core.FullScreenCalls
import com.skycoin.skywire.core.ConfigManager
import com.skycoin.skywire.core.ConfigVault
import com.skycoin.skywire.core.CoreServiceState
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.PublicAutoconnect
import com.skycoin.skywire.core.RemoteManagement
import com.skycoin.skywire.core.SecretStore
import com.skycoin.skywire.core.SkywireCoreService
import com.skycoin.skywire.core.SkywirePaths
import com.skycoin.skywire.core.ThemeMode
import com.skycoin.skywire.core.UpdateInstallReceiver
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File

/**
 * How far the update in Settings has got.
 *
 * One value rather than a handful of booleans because the states genuinely
 * exclude each other — a release cannot be downloading and up to date — and
 * the card is a different card in each of them. [Idle] is "nothing asked yet",
 * which is what a screen opened offline stays at, and is why "check" is a
 * button and not only something that happens to you.
 */
sealed interface UpdateStatus {
    data object Idle : UpdateStatus
    data object Checking : UpdateStatus

    /** Checked, and this build is the newest published one. */
    data object UpToDate : UpdateStatus

    data class Available(val release: AppUpdates.Release) : UpdateStatus

    /** [progress] is 0f..1f, or -1f when the server sent no length. */
    data class Downloading(
        val release: AppUpdates.Release,
        val progress: Float,
    ) : UpdateStatus

    /** Downloaded and verified; the installer has not been called yet. */
    data class Ready(val release: AppUpdates.Release, val apk: File) : UpdateStatus

    /** [reason] is already translated — it is shown as it is. */
    data class Failed(val reason: String) : UpdateStatus
}

/** Everything the Settings tab renders. */
data class SettingsUiState(
    val coreState: CoreState = CoreState.Stopped,
    /** This visor's key, from the config on disk — shown with the core down too. */
    val publicKey: String = "",
    val appLockEnabled: Boolean = AppLock.DEFAULT,
    /** Automatic transports to public visors — see [PublicAutoconnect]. */
    val publicAutoconnect: Boolean = PublicAutoconnect.DEFAULT,
    /** The remote-management grant — empty means nothing granted. */
    val remoteManagementPk: String = "",
    /** False when the phone has no screen lock and no enrolled biometric. */
    val biometricsAvailable: Boolean = true,
    val themeMode: ThemeMode = ThemeMode.SYSTEM,
    /** The interface language — the phone's own until the user picks one. */
    val language: AppLanguage = AppLanguage.SYSTEM,
    /** The visor config is encrypted at rest while the core is down. */
    val configEncrypted: Boolean = ConfigVault.DEFAULT,
    /** Whether Doze has been told to leave this app's network alone. */
    val batteryExempt: Boolean = true,
    /** The user has already declined the exemption once; stop offering. */
    val batteryPromptDismissed: Boolean = false,
    /** Whether a ringing call may take the screen — see [FullScreenCalls]. */
    val fullScreenCalls: Boolean = true,
    val fullScreenCallsDismissed: Boolean = false,
    val appVersion: String = "",
    /** Where the in-app update has got to — see [UpdateStatus]. */
    val update: UpdateStatus = UpdateStatus.Idle,
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
    private val config = ConfigManager(paths, SecretStore(app), app)
    private val vault = ConfigVault(paths, app)
    private val api = VisorApi.get(app)

    private val mutable = MutableStateFlow(SettingsUiState())
    val uiState: StateFlow<SettingsUiState> = mutable.asStateFlow()

    private var actionJob: Job? = null
    private var downloadJob: Job? = null

    init {
        // Not a flow like the rest: on API 33+ the platform holds this one, and
        // changing it recreates everything that could be observing it anyway.
        mutable.update { it.copy(language = AppLocale.current(app)) }
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
            prefs.string(RemoteManagement.PREF_KEY).collectLatest { stored ->
                mutable.update {
                    it.copy(remoteManagementPk = RemoteManagement.sanitize(stored).orEmpty())
                }
            }
        }
        viewModelScope.launch {
            prefs.string(ThemeMode.PREF_KEY).collectLatest { stored ->
                mutable.update { it.copy(themeMode = ThemeMode.of(stored)) }
            }
        }
        viewModelScope.launch {
            // A success never arrives — installing this app replaces the
            // process that would receive it. A failure does, and it is the
            // only word the user gets about why the dialog closed with
            // nothing installed.
            UpdateInstallReceiver.installEvents.collect { event ->
                if (event is UpdateInstallReceiver.InstallEvent.Failed) {
                    val app = getApplication<Application>()
                    val reason = event.message.ifBlank {
                        app.getString(R.string.settings_update_install_failed)
                    }
                    mutable.update { it.copy(update = UpdateStatus.Failed(reason)) }
                }
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
        viewModelScope.launch {
            prefs.boolean(FullScreenCalls.PREF_DISMISSED, false).collectLatest { dismissed ->
                mutable.update { it.copy(fullScreenCallsDismissed = dismissed) }
            }
        }
        // The exemption is granted in a system screen, not in this app, so the
        // only reliable moment to re-read it is when the user comes back from
        // there. That has to be every resume, not every return to the
        // foreground: the system's own dialog covers the Activity without
        // stopping it, so a grant made there never changes the foreground
        // flag and the card would keep offering until the app restarts.
        // Both of these are granted in a system screen, not in this app, so
        // the only reliable moment to re-read them is a resume.
        viewModelScope.launch {
            AppVisibility.resumes.collectLatest {
                refreshBatteryExemption()
                refreshFullScreenCalls()
            }
        }
        refreshBatteryExemption()
        refreshFullScreenCalls()
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

    // --- calls taking the screen ---

    fun refreshFullScreenCalls() {
        mutable.update { it.copy(fullScreenCalls = FullScreenCalls.isGranted(getApplication())) }
    }

    /**
     * Hand the user to the system switch. Nothing is recorded here: the answer
     * lives in the platform and [refreshFullScreenCalls] reads it back on the
     * way home.
     */
    fun requestFullScreenCalls() {
        val context = getApplication<Application>()
        if (!FullScreenCalls.openRequest(context)) {
            report(context.getString(R.string.settings_calls_no_screen))
        }
    }

    fun dismissFullScreenCallsPrompt() {
        viewModelScope.launch { prefs.putBoolean(FullScreenCalls.PREF_DISMISSED, true) }
    }

    // --- updating the app ---

    /**
     * Ask GitHub whether there is a newer build.
     *
     * [manual] is the difference between the button and the screen opening.
     * The button always asks and always says what it found; the automatic one
     * is throttled to [AppUpdates.CHECK_INTERVAL_MS] and stays quiet about a
     * failure, because a phone with no connection should not greet every
     * visit to Settings with an error about a check nobody asked for.
     */
    fun checkForUpdate(manual: Boolean) {
        // The F-Droid build: F-Droid delivers the updates, and its APKs carry
        // F-Droid's signature, so a GitHub build could not install over one.
        if (!BuildConfig.SELF_UPDATE) return
        val current = mutable.value.update
        // A download in flight is the answer to "is there an update" — asking
        // again would replace what the user is watching with a fresh Checking.
        if (current is UpdateStatus.Checking ||
            current is UpdateStatus.Downloading ||
            current is UpdateStatus.Ready
        ) {
            return
        }
        viewModelScope.launch {
            val now = System.currentTimeMillis()
            if (!manual) {
                val last = prefs.long(AppUpdates.PREF_LAST_CHECK).first()
                if (now - last < AppUpdates.CHECK_INTERVAL_MS) return@launch
                // Only the automatic check writes the stamp: throttling the
                // button would make it a button that sometimes does nothing.
                prefs.putLong(AppUpdates.PREF_LAST_CHECK, now)
            }
            mutable.update { it.copy(update = UpdateStatus.Checking) }
            val app = getApplication<Application>()
            // Anything left in the download cache is from a previous run of
            // the app, which makes it either an abandoned download or — the
            // case that matters — the 55 MB that was just installed. The
            // receiver's SUCCESS branch cannot clear that one: installing
            // this app replaces the process that would have handled it, so
            // measured on the emulator the file simply stayed. Here it is
            // safe, because a download in flight never reaches this line.
            AppUpdates.clearDownloads(app)
            AppUpdates.check(app)
                .onSuccess { release ->
                    mutable.update {
                        it.copy(
                            update = when {
                                release != null -> UpdateStatus.Available(release)
                                else -> UpdateStatus.UpToDate
                            },
                        )
                    }
                }
                .onFailure {
                    mutable.update {
                        it.copy(
                            update = if (manual) {
                                UpdateStatus.Failed(app.getString(R.string.settings_update_check_failed))
                            } else {
                                UpdateStatus.Idle
                            },
                        )
                    }
                }
        }
    }

    /** Fetch the APK, then sit at [UpdateStatus.Ready] until the user installs. */
    fun downloadUpdate() {
        val release = when (val state = mutable.value.update) {
            is UpdateStatus.Available -> state.release
            is UpdateStatus.Ready -> state.release
            else -> return
        }
        downloadJob?.cancel()
        downloadJob = viewModelScope.launch {
            val app = getApplication<Application>()
            mutable.update { it.copy(update = UpdateStatus.Downloading(release, 0f)) }
            AppUpdates.download(app, release) { fraction ->
                // Only while this is still the download being watched: a
                // cancelled job can emit one last chunk on its way out.
                mutable.update { state ->
                    if (state.update is UpdateStatus.Downloading) {
                        state.copy(update = UpdateStatus.Downloading(release, fraction))
                    } else {
                        state
                    }
                }
            }
                .onSuccess { apk -> mutable.update { it.copy(update = UpdateStatus.Ready(release, apk)) } }
                .onFailure {
                    mutable.update {
                        it.copy(
                            update = UpdateStatus.Failed(
                                app.getString(R.string.settings_update_download_failed),
                            ),
                        )
                    }
                }
        }
    }

    /** Stop a download and put the card back where it was. */
    fun cancelDownload() {
        val state = mutable.value.update
        downloadJob?.cancel()
        downloadJob = null
        AppUpdates.clearDownloads(getApplication())
        if (state is UpdateStatus.Downloading) {
            mutable.update { it.copy(update = UpdateStatus.Available(state.release)) }
        }
    }

    /**
     * Hand the downloaded APK to the system installer.
     *
     * The permission check comes first and does not install anything: without
     * "install unknown apps" the session commits and then fails with a status
     * the user cannot act on, so the switch is offered instead of the error.
     */
    fun installUpdate() {
        val state = mutable.value.update as? UpdateStatus.Ready ?: return
        val app = getApplication<Application>()
        if (!AppUpdates.canInstall(app)) {
            if (!AppUpdates.openInstallPermission(app)) {
                report(app.getString(R.string.settings_update_no_permission_screen))
            }
            return
        }
        viewModelScope.launch {
            AppUpdates.install(app, state.apk).onFailure {
                mutable.update {
                    it.copy(
                        update = UpdateStatus.Failed(
                            app.getString(R.string.settings_update_install_failed),
                        ),
                    )
                }
            }
        }
    }

    /** The releases page in a browser — the way out when installing will not work. */
    fun openReleasePage() {
        val app = getApplication<Application>()
        val url = when (val state = mutable.value.update) {
            is UpdateStatus.Available -> state.release.pageUrl
            is UpdateStatus.Ready -> state.release.pageUrl
            else -> AppUpdates.RELEASES_PAGE
        }
        if (!AppUpdates.openReleasePage(app, url)) {
            report(app.getString(R.string.settings_update_no_browser))
        }
    }

    /** The app's source, which the AGPL it is licensed under promises. */
    fun openSourceCode() {
        val app = getApplication<Application>()
        if (!AppUpdates.openReleasePage(app, SOURCE_PAGE)) {
            report(app.getString(R.string.settings_update_no_browser))
        }
    }

    /** Clear a failure so the card offers the check again. */
    fun dismissUpdateFailure() {
        if (mutable.value.update is UpdateStatus.Failed) {
            mutable.update { it.copy(update = UpdateStatus.Idle) }
        }
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
            } ?: error(
                getApplication<Application>().getString(R.string.settings_export_unwritable),
            )
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

    /**
     * Grant remote management to the visor whose public key is [raw], or
     * refuse it on shape grounds with the reason on the snackbar. Stored
     * first and applied by the config rewrite on the next core start, like
     * [setPublicAutoconnect] — the trust list is read when the visor boots.
     */
    fun grantRemoteManagement(raw: String) {
        viewModelScope.launch {
            val pk = RemoteManagement.sanitize(raw)
            if (pk == null) {
                report(getApplication<Application>().getString(R.string.settings_remote_invalid))
                return@launch
            }
            prefs.putString(RemoteManagement.PREF_KEY, pk)
            report(getApplication<Application>().getString(R.string.settings_remote_granted))
        }
    }

    /** Withdraw the grant. Takes effect when the core next starts. */
    fun revokeRemoteManagement() {
        viewModelScope.launch {
            prefs.putString(RemoteManagement.PREF_KEY, null)
            report(getApplication<Application>().getString(R.string.settings_remote_revoked))
        }
    }

    fun setBiometricsAvailable(available: Boolean) {
        mutable.update { it.copy(biometricsAvailable = available) }
    }

    fun setThemeMode(mode: ThemeMode) {
        viewModelScope.launch { prefs.putString(ThemeMode.PREF_KEY, mode.name) }
    }

    /**
     * Written straight through rather than through [prefs], because the very
     * next thing that happens is the Activity being thrown away and rebuilt on
     * the new language: an asynchronous write would still be in flight while
     * the new one reads.
     *
     * True back means the caller has to recreate the Activity itself — see
     * [AppLocale.set].
     */
    fun setLanguage(language: AppLanguage): Boolean {
        val recreate = AppLocale.set(getApplication(), language)
        mutable.update { it.copy(language = language) }
        return recreate
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
        const val SOURCE_PAGE = "https://github.com/skycoin/skywire"
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
