package com.skycoin.skywire.core

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.util.Log
import androidx.core.content.IntentCompat
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow

/**
 * What the system installer says back about an update.
 *
 * A [PackageInstaller] session reports through a broadcast rather than an
 * Activity result, which is what makes it usable from a screen the user may
 * have left. Two of the statuses matter:
 *
 *  - **PENDING_USER_ACTION** is not a failure. It is the installer saying "I
 *    have a confirmation dialog for you", handing over the Activity to start.
 *    Nothing installs until that runs, so this is the step that turns a
 *    downloaded file into an install prompt.
 *  - **SUCCESS** mostly does not arrive at all: installing *this* app
 *    replaces the process that would receive it. It is handled anyway for the
 *    case where it does, but nothing may depend on it — the cached APK is
 *    cleared on the next check for exactly that reason, because measured on
 *    an emulator the 55 MB stayed put after a successful update.
 *
 * Everything else is a failure with a reason, and the reason is what the card
 * shows — "the install was cancelled" and "signed with a different key" are
 * very different things to be told.
 */
class UpdateInstallReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != ACTION_INSTALL_STATUS) return
        val app = context.applicationContext
        val status = intent.getIntExtra(
            PackageInstaller.EXTRA_STATUS,
            PackageInstaller.STATUS_FAILURE,
        )
        val message = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE).orEmpty()
        when (status) {
            PackageInstaller.STATUS_PENDING_USER_ACTION -> {
                val confirm = IntentCompat.getParcelableExtra(
                    intent, Intent.EXTRA_INTENT, Intent::class.java,
                )
                if (confirm == null) {
                    Log.w(TAG, "installer wanted confirmation but sent no intent")
                    events.tryEmit(InstallEvent.Failed(""))
                    return
                }
                // Started from a receiver, which has no task of its own —
                // without NEW_TASK this throws instead of showing the dialog.
                confirm.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                runCatching { app.startActivity(confirm) }.onFailure {
                    Log.w(TAG, "could not show the install confirmation", it)
                    events.tryEmit(InstallEvent.Failed(""))
                }
            }

            PackageInstaller.STATUS_SUCCESS -> {
                Log.i(TAG, "update installed")
                AppUpdates.clearDownloads(app)
                events.tryEmit(InstallEvent.Succeeded)
            }

            else -> {
                Log.w(TAG, "install failed: status=$status $message")
                events.tryEmit(InstallEvent.Failed(message))
            }
        }
    }

    /** What the installer did, for whatever screen is still around to care. */
    sealed interface InstallEvent {
        data object Succeeded : InstallEvent

        /** [message] is the installer's own words, and is often empty. */
        data class Failed(val message: String) : InstallEvent
    }

    companion object {
        private const val TAG = "SkywireUpdates"

        const val ACTION_INSTALL_STATUS = "com.skycoin.skywire.update.INSTALL_STATUS"

        // No replay: the only status a user can act on is a failure, and the
        // confirmation dialog it comes from sits on top of this app, so
        // Settings is still there collecting. Replaying it instead would
        // re-announce a months-old failure the next time Settings opened.
        private val events = MutableSharedFlow<InstallEvent>(extraBufferCapacity = 4)

        val installEvents: SharedFlow<InstallEvent> = events.asSharedFlow()
    }
}
