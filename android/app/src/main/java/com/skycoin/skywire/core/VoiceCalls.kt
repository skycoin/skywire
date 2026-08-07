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
import com.skycoin.skywire.api.SkychatApi
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.api.VoiceInvite
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
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

    /**
     * Calls this phone has hung up or declined but the visor still reports.
     *
     * The watcher polls, so without this the call screen stayed up for the
     * rest of the tick after the red button — several seconds of a phone that
     * looks like it did not hear you. Hanging up removes the call here at
     * once and suppresses it until the visor agrees it is gone, which is the
     * order every phone does this in. Nothing is lost if the visor disagrees:
     * a hang-up that fails un-suppresses the id (see [endFailed]) and the
     * next poll puts the call back.
     */
    private val ended = MutableStateFlow<Set<String>>(emptySet())

    /**
     * Wakes the watcher out of its poll delay. Conflated and non-blocking:
     * one nudge before the next tick is all it can usefully carry.
     */
    private val nudges = MutableSharedFlow<Unit>(extraBufferCapacity = 1)
    internal val nudge: SharedFlow<Unit> = nudges.asSharedFlow()

    /** The user ended [callId] — drop it now, confirm with the visor after. */
    fun endLocally(callId: String) {
        ended.update { it + callId }
        mutable.update { it.without(callId) }
        nudges.tryEmit(Unit)
    }

    /** The end never reached the visor: let the call come back on the next poll. */
    fun endFailed(callId: String) {
        ended.update { it - callId }
        nudges.tryEmit(Unit)
    }

    /**
     * The operator's names for public keys, from skychat's address book.
     *
     * Held here rather than fetched per screen because a call screen has to
     * name its peer the instant it appears — a name that arrives a second
     * later, after the user has already read a wall of hex, is not much of an
     * improvement. Refreshed by the watcher on the same tick as the calls.
     */
    private val names = MutableStateFlow<Map<String, String>>(emptyMap())

    internal fun setNames(book: Map<String, String>) {
        names.value = book.mapKeys { (pk, _) -> pk.lowercase() }
    }

    /**
     * What to call [pk]: the operator's name for it, else the shortened key.
     * The same order skychat uses for a notification title, so the two never
     * disagree about who called.
     */
    fun displayName(pk: String): String =
        names.value[pk.lowercase()]?.takeIf { it.isNotEmpty() } ?: VoiceCallWatcher.shortPk(pk)

    internal fun set(
        ringing: List<VoiceInvite>,
        dialing: List<VoiceInvite>,
        activeIds: List<String>,
    ) {
        // Ids the visor has stopped reporting have served their purpose and
        // leave the suppression set with it — kept any longer this would grow
        // for the life of the process.
        val reported = ringing.map { it.callId } + dialing.map { it.callId } + activeIds
        ended.update { it intersect reported.toSet() }
        val hidden = ended.value
        mutable.update {
            it.copy(
                ringing = ringing.filterNot { call -> call.callId in hidden },
                dialing = dialing.filterNot { call -> call.callId in hidden },
                activeIds = activeIds.filterNot { id -> id in hidden },
            )
        }
    }

    internal fun clear() {
        ended.value = emptySet()
        mutable.update { VoiceCallState() }
    }
}

/** This state minus one call, whichever of the three lists it is in. */
private fun VoiceCallState.without(callId: String) = copy(
    ringing = ringing.filterNot { it.callId == callId },
    dialing = dialing.filterNot { it.callId == callId },
    activeIds = activeIds.filterNot { it == callId },
)

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
    private val skychat = SkychatApi.get(app)
    private val notifications = NotificationManagerCompat.from(app)
    private var namesAt = 0L

    fun watch(scope: CoroutineScope): Job = scope.launch(Dispatchers.IO) {
        ensureChannels(app)
        var wasInCall = false
        try {
            while (coroutineContext.isActive) {
                // Before the calls, so a name is already in hand when one
                // arrives — a call screen that shows hex and corrects itself a
                // moment later is barely better than one that never knew.
                refreshNames()
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
                // The tick, unless something happened that the next answer
                // will be about. Hanging up nudges this so the visor is asked
                // straight away rather than up to a tick later — the screen
                // has already closed, and this is what makes the state behind
                // it catch up while the user is still looking at the phone.
                withTimeoutOrNull(POLL_MS) { VoiceCalls.nudge.first() }
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
     * Re-read the address book, rarely. Names change when a human edits one,
     * so polling it at the call rate would be a request every two seconds for
     * a file that changes twice a month.
     */
    private suspend fun refreshNames() {
        val now = System.currentTimeMillis()
        if (now - namesAt < NAMES_REFRESH_MS) return
        namesAt = now
        val url = SkychatProfile.baseUrl(SkychatProfile.DEFAULT_PORT)
        VoiceCalls.setNames(skychat.contacts(url))
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
            .setContentText(VoiceCalls.displayName(invite.fromPk))
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
        private const val NAMES_REFRESH_MS = 30_000L
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
