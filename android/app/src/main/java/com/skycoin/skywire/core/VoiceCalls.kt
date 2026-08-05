package com.skycoin.skywire.core

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.skycoin.skywire.MainActivity
import com.skycoin.skywire.R
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.api.VoiceInvite
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlin.coroutines.coroutineContext

/** What the phone knows about calls right now. */
data class VoiceCallState(
    val ringing: List<VoiceInvite> = emptyList(),
    val dialing: List<VoiceInvite> = emptyList(),
    val activeIds: List<String> = emptyList(),
) {
    val inCall: Boolean get() = activeIds.isNotEmpty()

    /** The call to offer an answer for — one at a time is all a phone shows. */
    val invite: VoiceInvite? get() = ringing.firstOrNull()

    /** The call being placed, if any. */
    val outgoing: VoiceInvite? get() = dialing.firstOrNull()

    /** True whenever there is a call to put on screen, in either direction. */
    val busy: Boolean get() = inCall || invite != null || outgoing != null
}

/**
 * Process-wide view of the visor's calls.
 *
 * Read by the UI, written by the watcher below, which runs inside the core
 * service — so a call rings and connects whether or not any screen is looking,
 * which is the whole point of a phone.
 */
object VoiceCalls {

    private val mutable = MutableStateFlow(VoiceCallState())
    val state: StateFlow<VoiceCallState> = mutable.asStateFlow()

    internal fun set(
        ringing: List<VoiceInvite>,
        dialing: List<VoiceInvite>,
        activeIds: List<String>,
    ) {
        mutable.update { it.copy(ringing = ringing, dialing = dialing, activeIds = activeIds) }
    }

    internal fun clear() = mutable.update { VoiceCallState() }
}

/**
 * Polls the visor for ringing and connected calls, and turns the answer into
 * the two things the phone owes a call: a notification you can answer from,
 * and — while connected — the service that lends the visor the microphone and
 * speaker.
 *
 * Polling rather than a subscription because that is the surface the visor
 * offers; at a two-second tick against a loopback API the cost is noise.
 */
internal class VoiceCallWatcher(context: Context) {

    private val app = context.applicationContext
    private val api = VisorApi.get(app)
    private val notifications = NotificationManagerCompat.from(app)

    fun watch(scope: CoroutineScope): Job = scope.launch(Dispatchers.IO) {
        ensureChannels(app)
        var wasInCall = false
        try {
            while (coroutineContext.isActive) {
                val ringing = runCatching { api.voiceIncoming() }.getOrNull()
                val active = runCatching { api.voiceActive() }.getOrNull()
                val dialing = runCatching { api.voiceDialing() }.getOrNull().orEmpty()
                // A failed poll is left alone rather than reported as "no
                // calls": the visor restarting mid-call would otherwise cancel
                // a live call's notification and drop the audio service.
                if (ringing != null && active != null) {
                    VoiceCalls.set(ringing, dialing, active)
                    showRinging(ringing.firstOrNull())
                    val inCall = active.isNotEmpty()
                    if (inCall != wasInCall) {
                        if (inCall) VoiceCallService.start(app) else VoiceCallService.stop(app)
                        wasInCall = inCall
                    }
                }
                delay(POLL_MS)
            }
        } finally {
            // The core is going away, so the calls are too — leave nothing
            // ringing on screen and no service holding the microphone.
            VoiceCalls.clear()
            notifications.cancel(RINGING_NOTIFICATION_ID)
            if (wasInCall) VoiceCallService.stop(app)
        }
    }

    /**
     * Put a ringing call in front of the user — as the call SCREEN, never as
     * something to read in a shade.
     *
     * With the app on screen there is nothing to do here: the UI draws the
     * call itself the moment [VoiceCalls] changes. With the app backgrounded
     * or closed, an app may not simply start an Activity — Android has
     * forbidden background activity launches since 10 — and the sanctioned
     * way to show a call is a **full-screen intent**: a notification whose
     * only job is to carry the Activity that replaces it. On an unlocked,
     * idle device the system launches that Activity directly and the
     * notification is never seen; if it cannot (the user is mid-gesture, or
     * has revoked the permission) it degrades to a heads-up banner, which is
     * the floor, not the design.
     */
    private fun showRinging(invite: VoiceInvite?) {
        if (invite == null || AppVisibility.isForeground.value) {
            notifications.cancel(RINGING_NOTIFICATION_ID)
            return
        }
        val screen = PendingIntent.getActivity(
            app,
            0,
            Intent(app, MainActivity::class.java)
                .setAction(Intent.ACTION_MAIN)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val note = NotificationCompat.Builder(app, CHANNEL_RINGING)
            .setSmallIcon(R.drawable.skywire_logo)
            .setContentTitle(app.getString(R.string.call_incoming_title))
            .setContentText(shortPk(invite.fromPk))
            .setCategory(NotificationCompat.CATEGORY_CALL)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setContentIntent(screen)
            .setFullScreenIntent(screen, true)
            .build()
        runCatching { notifications.notify(RINGING_NOTIFICATION_ID, note) }
            .onFailure { Log.w(TAG, "cannot raise the call screen", it) }
    }

    companion object {
        private const val TAG = "SkywireVoice"
        private const val POLL_MS = 2_000L
        const val RINGING_NOTIFICATION_ID = 2

        const val CHANNEL_RINGING = "call_incoming"
        const val CHANNEL_ONGOING = "call_ongoing"

        /**
         * Two channels because they are two different interruptions: a call
         * arriving has to break through (sound, heads-up), and a call in
         * progress must not make a sound at all.
         */
        fun ensureChannels(context: Context) {
            val manager =
                context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
            manager.createNotificationChannel(
                NotificationChannel(
                    CHANNEL_RINGING,
                    context.getString(R.string.call_channel_incoming),
                    NotificationManager.IMPORTANCE_HIGH,
                ),
            )
            manager.createNotificationChannel(
                NotificationChannel(
                    CHANNEL_ONGOING,
                    context.getString(R.string.call_channel_ongoing),
                    NotificationManager.IMPORTANCE_LOW,
                ),
            )
        }

        /** `03ab…c9d1` — a 66-character key is not a caller ID. */
        fun shortPk(pk: String): String =
            if (pk.length <= 12) pk else pk.take(6) + "…" + pk.takeLast(4)
    }
}
