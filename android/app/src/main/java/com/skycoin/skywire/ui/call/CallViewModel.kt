package com.skycoin.skywire.ui.call

import android.app.Application
import android.media.AudioAttributes
import android.media.AudioManager
import android.media.MediaPlayer
import android.media.Ringtone
import android.media.RingtoneManager
import android.media.ToneGenerator
import android.os.Build
import android.os.SystemClock
import android.util.Log
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.api.DialState
import com.skycoin.skywire.api.OutgoingCall
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.core.VoiceCalls
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File

/** What the call screen draws. */
data class CallUiState(
    /** Short form of the other side's key; null when there is no call. */
    val peer: String? = null,
    val callId: String? = null,
    val connected: Boolean = false,
    /** Placing a call, waiting for the other side to pick up. */
    val dialing: Boolean = false,
    /**
     * How the call being placed is going — or, once it ended unanswered, why
     * (offline, declined, no answer…). Null when this phone is not calling.
     */
    val dialState: DialState? = null,
    /** The other side's key, for Call again. */
    val peerPk: String? = null,
    val micMuted: Boolean = false,
    val speakerphone: Boolean = false,
    /** `m:ss`, counted from when this device saw the call connect. */
    val elapsed: String = "0:00",
)

/**
 * Drives the full-screen call UI off [VoiceCalls] — the same list of calls the
 * visor answers with, so this screen and the chat page's own panel can never
 * disagree about whether there is a call.
 *
 * The elapsed time is counted here rather than read from the visor: the visor
 * does not publish a start time, and a timer that begins when THIS phone saw
 * the call connect is the honest thing to show anyway.
 */
class CallViewModel(app: Application) : AndroidViewModel(app) {

    private val api = VisorApi.get(app)
    private val audioManager =
        app.getSystemService(Application.AUDIO_SERVICE) as AudioManager

    private val mutable = MutableStateFlow(CallUiState())
    val uiState: StateFlow<CallUiState> = mutable.asStateFlow()

    private var ringtone: Ringtone? = null
    private var connectedAt: Long = 0

    // What the caller hears while the other side rings: its own ringback
    // tone once that has arrived, the network's ordinary ring until then.
    private var ringbackTone: ToneGenerator? = null
    private var ringbackPlayer: MediaPlayer? = null
    private var ringbackJob: Job? = null
    private var ringbackFor: String? = null
    private var endedSignalFor: String? = null

    init {
        // Answer tapped on the notification. Waits for the call to be in the
        // state before acting: the tap can start the app, and the first poll
        // that knows about the call may still be in flight.
        viewModelScope.launch {
            VoiceCalls.answers.collect { callId ->
                // The wait is bounded and the request is retired either way.
                // Both matter: collect is sequential, so a request that never
                // resolves holds every later one behind it, and the replay
                // cache would hand the same dead id to the next view model —
                // one stale tap would end answering from the notification
                // until the process restarted. See VoiceCalls.awaitRinging.
                if (VoiceCalls.awaitRinging(callId)) {
                    runCatching { api.voiceAnswer(callId) }
                        .onFailure { Log.w(TAG, "answer from the notification failed", it) }
                } else {
                    Log.i(TAG, "answer request for a call that never arrived")
                }
                VoiceCalls.answerHandled()
            }
        }
        viewModelScope.launch {
            VoiceCalls.state.collect { calls ->
                val invite = calls.invite
                val outgoing = calls.outgoing
                val active = calls.activeIds.firstOrNull()
                val connected = active != null
                if (connected && connectedAt == 0L) connectedAt = SystemClock.elapsedRealtime()
                if (!connected) connectedAt = 0

                mutable.update {
                    it.copy(
                        // Once connected the peer is kept from whichever side
                        // named it: the active list carries ids only, so
                        // re-deriving it every poll would blank the name the
                        // moment the call was answered.
                        peer = when {
                            connected -> it.peer
                                ?: invite?.fromPk?.let(::short)
                                ?: outgoing?.peerPk?.let(::short)
                            invite != null -> short(invite.fromPk)
                            outgoing != null -> short(outgoing.peerPk)
                            else -> null
                        },
                        peerPk = when {
                            connected -> it.peerPk ?: invite?.fromPk ?: outgoing?.peerPk
                            invite != null -> invite.fromPk
                            else -> outgoing?.peerPk
                        },
                        callId = active ?: invite?.callId ?: outgoing?.callId,
                        connected = connected,
                        dialing = !connected && outgoing != null,
                        dialState = if (!connected) outgoing?.state else null,
                        // A call that ended takes its mute state with it.
                        micMuted = if (calls.busy) it.micMuted else false,
                    )
                }
                // Ring only for a call coming IN, and only here: the point of
                // the full-screen UI is that it, not a notification, is the
                // call. A call we are placing rings at the other end.
                if (invite != null && !connected) startRinging() else stopRinging()
                syncRingback(if (!connected && invite == null) outgoing else null)
            }
        }
        viewModelScope.launch {
            while (true) {
                if (connectedAt != 0L) {
                    val seconds = (SystemClock.elapsedRealtime() - connectedAt) / 1000
                    mutable.update { it.copy(elapsed = "%d:%02d".format(seconds / 60, seconds % 60)) }
                }
                delay(500)
            }
        }
    }

    fun answer() = act { id -> api.voiceAnswer(id) }

    fun decline() = end { id -> api.voiceDecline(id) }

    fun hangUp() = end { id -> api.voiceHangup(id) }

    /**
     * Close the screen on a call that ended unanswered. The hang-up only tells
     * the visor it can stop listing the outcome — which it does on its own a
     * few seconds later — so a failure has nothing to put back.
     */
    fun dismiss() {
        val id = mutable.value.callId ?: return
        stopRingback()
        VoiceCalls.endLocally(id)
        viewModelScope.launch { runCatching { api.voiceHangup(id) } }
    }

    /** Try the same person again, from an unanswered call's outcome. */
    fun callAgain() {
        val peer = mutable.value.peerPk ?: return
        dismiss()
        viewModelScope.launch {
            runCatching { api.voiceCall(peer) }
                .onFailure { Log.w(TAG, "calling again failed", it) }
            VoiceCalls.refresh()
        }
    }

    fun toggleMic() {
        val muted = !mutable.value.micMuted
        mutable.update { it.copy(micMuted = muted) }
        act { id -> api.voiceMute(id, mic = muted, speaker = false) }
    }

    /**
     * Earpiece ⇄ speakerphone. Routing is the phone's business, not the
     * visor's — the audio has already arrived by the time this matters.
     */
    fun toggleSpeakerphone() {
        val on = !mutable.value.speakerphone
        runCatching {
            @Suppress("DEPRECATION") // setCommunicationDevice needs a device list walk; this is one line and still honoured.
            audioManager.isSpeakerphoneOn = on
        }.onFailure { Log.w(TAG, "could not switch the call route", it) }
        mutable.update { it.copy(speakerphone = on) }
    }

    private fun act(block: suspend (String) -> Unit) {
        val id = mutable.value.callId ?: return
        viewModelScope.launch {
            runCatching { block(id) }
                .onFailure { Log.w(TAG, "call action failed", it) }
        }
    }

    /**
     * Hang up / decline: the two actions whose whole point is that the call
     * stops *now*. The call leaves the shared state before the request goes
     * out, so the screen closes on the tap rather than on the watcher's next
     * poll — which is a tick away and read as a phone ignoring the button.
     * A request that fails hands the call back, and the poll behind it puts
     * the screen up again.
     */
    private fun end(block: suspend (String) -> Unit) {
        val id = mutable.value.callId ?: return
        stopRinging()
        VoiceCalls.endLocally(id)
        viewModelScope.launch {
            runCatching { block(id) }.onFailure {
                Log.w(TAG, "call action failed", it)
                VoiceCalls.endFailed(id)
            }
        }
    }

    private fun startRinging() {
        if (ringtone?.isPlaying == true) return
        runCatching {
            val uri = RingtoneManager.getDefaultUri(RingtoneManager.TYPE_RINGTONE)
            ringtone = RingtoneManager.getRingtone(getApplication(), uri).also {
                // Looping arrived in API 28 and this app supports 26. Setting
                // it below that throws, and because the whole block is one
                // runCatching the throw landed before play() — an incoming
                // call on Android 8 rang not badly but not at all. Above 28
                // the ringtone loops itself; below it the poll above re-arms
                // it each tick, which is the isPlaying guard's other job.
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) it.isLooping = true
                it.play()
            }
        }.onFailure { Log.w(TAG, "no ringtone", it) }
    }

    private fun stopRinging() {
        runCatching { ringtone?.stop() }
        ringtone = null
    }

    /**
     * Keep what the caller hears in step with how the call is going: silence
     * while it is still connecting (there is no ringing yet to report), a ring
     * while it rings, the busy signal once when it ends unanswered.
     */
    private fun syncRingback(call: OutgoingCall?) {
        when {
            call == null || call.state == DialState.CONNECTING -> stopRingback()
            call.state.ended -> {
                stopRingback()
                if (endedSignalFor != call.callId) {
                    endedSignalFor = call.callId
                    playEndedSignal()
                }
            }
            call.ringback && ringbackFor != call.callId -> startCustomRingback(call.callId)
            ringbackPlayer == null -> startPlainRingback()
        }
    }

    /** The network's ordinary ring — what every phone plays while it waits. */
    private fun startPlainRingback() {
        if (ringbackTone != null) return
        ringbackTone = runCatching {
            ToneGenerator(AudioManager.STREAM_VOICE_CALL, RINGBACK_VOLUME).apply {
                startTone(ToneGenerator.TONE_SUP_RINGTONE)
            }
        }.onFailure { Log.w(TAG, "no ringback tone", it) }.getOrNull()
    }

    private fun stopPlainRingback() {
        ringbackTone?.let { runCatching { it.stopTone(); it.release() } }
        ringbackTone = null
    }

    /**
     * The other side's own ringback tone. The ordinary ring keeps playing
     * until it is ready to take over, and comes back if it cannot play.
     */
    private fun startCustomRingback(callId: String) {
        ringbackFor = callId
        ringbackJob?.cancel()
        ringbackJob = viewModelScope.launch {
            val bytes = runCatching { api.voiceDialRingback(callId) }.getOrNull() ?: return@launch
            val file = withContext(Dispatchers.IO) {
                File(getApplication<Application>().cacheDir, RINGBACK_FILE).apply { writeBytes(bytes) }
            }
            if (ringbackFor != callId) return@launch
            val player = MediaPlayer()
            runCatching {
                player.setAudioAttributes(
                    AudioAttributes.Builder()
                        .setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION_SIGNALLING)
                        .setContentType(AudioAttributes.CONTENT_TYPE_MUSIC)
                        .build(),
                )
                player.setDataSource(file.path)
                player.isLooping = true
                player.setOnPreparedListener {
                    if (ringbackPlayer !== it) return@setOnPreparedListener
                    stopPlainRingback()
                    it.start()
                }
                player.setOnErrorListener { mp, what, extra ->
                    Log.w(TAG, "the other side's ringback tone would not play ($what/$extra)")
                    if (ringbackPlayer === mp) {
                        ringbackPlayer = null
                        mp.release()
                        startPlainRingback()
                    }
                    true
                }
                ringbackPlayer = player
                player.prepareAsync()
            }.onFailure {
                Log.w(TAG, "cannot play the other side's ringback tone", it)
                ringbackPlayer = null
                player.release()
            }
        }
    }

    private fun stopRingback() {
        stopPlainRingback()
        ringbackJob?.cancel()
        ringbackJob = null
        ringbackFor = null
        ringbackPlayer?.let { runCatching { it.stop() }; it.release() }
        ringbackPlayer = null
    }

    /** Three bursts of the busy signal: the call did not go through. */
    private fun playEndedSignal() {
        val tone = runCatching { ToneGenerator(AudioManager.STREAM_VOICE_CALL, RINGBACK_VOLUME) }
            .getOrNull() ?: return
        runCatching { tone.startTone(ToneGenerator.TONE_SUP_BUSY, ENDED_SIGNAL_MS) }
        viewModelScope.launch {
            delay(ENDED_SIGNAL_MS + 200L)
            runCatching { tone.release() }
        }
    }

    override fun onCleared() {
        stopRinging()
        stopRingback()
        super.onCleared()
    }

    /** The operator's name for the key when there is one — see VoiceCalls. */
    private fun short(pk: String) = VoiceCalls.displayName(pk)

    private companion object {
        const val TAG = "SkywireVoice"
        const val RINGBACK_VOLUME = 80
        const val RINGBACK_FILE = "ringback.audio"
        const val ENDED_SIGNAL_MS = 1500
    }
}
