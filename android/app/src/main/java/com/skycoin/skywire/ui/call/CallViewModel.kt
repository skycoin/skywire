package com.skycoin.skywire.ui.call

import android.app.Application
import android.media.AudioManager
import android.media.Ringtone
import android.media.RingtoneManager
import android.os.SystemClock
import android.util.Log
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.core.VoiceCallWatcher
import com.skycoin.skywire.core.VoiceCalls
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** What the call screen draws. */
data class CallUiState(
    /** Short form of the other side's key; null when there is no call. */
    val peer: String? = null,
    val callId: String? = null,
    val connected: Boolean = false,
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

    init {
        viewModelScope.launch {
            VoiceCalls.state.collect { calls ->
                val invite = calls.invite
                val active = calls.activeIds.firstOrNull()
                val connected = active != null
                if (connected && connectedAt == 0L) connectedAt = SystemClock.elapsedRealtime()
                if (!connected) connectedAt = 0

                mutable.update {
                    it.copy(
                        peer = when {
                            connected -> it.peer ?: invite?.fromPk?.let(::short)
                            invite != null -> short(invite.fromPk)
                            else -> null
                        },
                        callId = active ?: invite?.callId,
                        connected = connected,
                        // A call that ended takes its mute state with it.
                        micMuted = if (calls.inCall || invite != null) it.micMuted else false,
                    )
                }
                // Ring only while ringing, and only here: the point of the
                // full-screen UI is that it, not a notification, is the call.
                if (invite != null && !connected) startRinging() else stopRinging()
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

    fun decline() = act { id -> api.voiceDecline(id) }

    fun hangUp() = act { id -> api.voiceHangup(id) }

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

    private fun startRinging() {
        if (ringtone?.isPlaying == true) return
        runCatching {
            val uri = RingtoneManager.getDefaultUri(RingtoneManager.TYPE_RINGTONE)
            ringtone = RingtoneManager.getRingtone(getApplication(), uri).also {
                it.isLooping = true
                it.play()
            }
        }.onFailure { Log.w(TAG, "no ringtone", it) }
    }

    private fun stopRinging() {
        runCatching { ringtone?.stop() }
        ringtone = null
    }

    override fun onCleared() {
        stopRinging()
        super.onCleared()
    }

    private fun short(pk: String) = VoiceCallWatcher.shortPk(pk)

    private companion object {
        const val TAG = "SkywireVoice"
    }
}
