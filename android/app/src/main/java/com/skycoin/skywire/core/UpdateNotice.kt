package com.skycoin.skywire.core

import android.content.Context
import android.content.Intent
import android.os.Build
import androidx.core.net.toUri
import com.skycoin.skywire.BuildConfig
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import java.util.concurrent.atomic.AtomicBoolean

/**
 * The banner that says, once, that a newer version is out.
 *
 * Asked once per process, after the app starts. Update and Later both mark
 * the version as seen, so the banner never comes back for it. A newer
 * release is offered again.
 *
 * Where Update leads depends on who installed the app. F-Droid and Google
 * Play deliver their own updates, so the banner opens the app's page there.
 * A copy from GitHub or a direct download updates itself, so the banner sends
 * the user to Settings and starts the download there.
 */
object UpdateNotice {

    /** Who delivers this phone's updates. */
    enum class Source { FDROID, PLAY, DIRECT }

    /** A newer version to show. [release] is what Settings downloads, set for [Source.DIRECT] only. */
    data class Offer(
        val version: String,
        val source: Source,
        val release: AppUpdates.Release? = null,
    )

    /** The version the banner was last answered for. */
    private const val PREF_SEEN = "updates_notice_seen"

    private const val PLAY_STORE = "com.android.vending"

    /** F-Droid's client, its privileged extension, and the common clients that read its repos. */
    private val FDROID_CLIENTS = setOf(
        "org.fdroid.fdroid",
        "org.fdroid.basic",
        "org.fdroid.fdroid.privileged",
        "com.looker.droidify",
        "com.machiav3lli.fdroid",
    )

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val asked = AtomicBoolean(false)

    private val current = MutableStateFlow<Offer?>(null)

    /** What the banner shows, null when there is nothing to say. */
    val offer: StateFlow<Offer?> = current.asStateFlow()

    private val download = MutableStateFlow<AppUpdates.Release?>(null)

    /** A release the banner sent the user to Settings for, until Settings starts it. */
    val pendingDownload: StateFlow<AppUpdates.Release?> = download.asStateFlow()

    /** Ask once per process. A failed check stays quiet, as the one in Settings does. */
    suspend fun checkOnce(context: Context) {
        if (!asked.compareAndSet(false, true)) return
        val app = context.applicationContext
        val source = sourceOf(installer(app), BuildConfig.SELF_UPDATE)
        val found = when (source) {
            Source.FDROID -> AppUpdates.checkFdroid(app).getOrNull()?.let { Offer(it, source) }
            else -> AppUpdates.check(app).getOrNull()?.let { Offer(it.version.toString(), source, it) }
        } ?: return
        if (AppPreferences(app).string(PREF_SEEN).first() == found.version) return
        current.value = found
    }

    /** Update or Later: the banner goes and this version is not offered again. */
    fun dismiss(context: Context, offer: Offer) {
        current.compareAndSet(offer, null)
        val app = context.applicationContext
        scope.launch { AppPreferences(app).putString(PREF_SEEN, offer.version) }
    }

    fun requestDownload(release: AppUpdates.Release) {
        download.value = release
    }

    fun downloadTaken(release: AppUpdates.Release) {
        download.compareAndSet(release, null)
    }

    /**
     * The app's page in the store that installed it, or that store's web page
     * when no app answers. False when nothing could open either.
     */
    fun openStore(context: Context, source: Source): Boolean {
        val pkg = context.packageName
        val (storeApp, web) = when (source) {
            Source.PLAY -> PLAY_STORE to "https://play.google.com/store/apps/details?id=$pkg"
            else -> installer(context)?.takeIf { it in FDROID_CLIENTS } to
                "https://f-droid.org/packages/$pkg/"
        }
        // Google Play and the F-Droid clients all answer market://details.
        val inStore = storeApp?.let {
            Intent(Intent.ACTION_VIEW, "market://details?id=$pkg".toUri()).setPackage(it)
        }
        return listOfNotNull(inStore, Intent(Intent.ACTION_VIEW, web.toUri())).any { intent ->
            runCatching {
                context.startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            }.isSuccess
        }
    }

    /** The F-Droid build cannot update itself, whoever installed it. */
    internal fun sourceOf(installer: String?, selfUpdate: Boolean): Source = when {
        installer == PLAY_STORE -> Source.PLAY
        installer in FDROID_CLIENTS || !selfUpdate -> Source.FDROID
        else -> Source.DIRECT
    }

    private fun installer(context: Context): String? = runCatching {
        val pm = context.packageManager
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            pm.getInstallSourceInfo(context.packageName).installingPackageName
        } else {
            @Suppress("DEPRECATION")
            pm.getInstallerPackageName(context.packageName)
        }
    }.getOrNull()
}
