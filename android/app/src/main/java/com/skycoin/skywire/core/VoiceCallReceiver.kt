package com.skycoin.skywire.core

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import com.skycoin.skywire.api.VisorApi
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

/**
 * Declining a call from the notification, without opening the app.
 *
 * A receiver rather than an Activity because that is the whole point of the
 * button: turning down a call you do not want should not put the app in front
 * of you to do it. Answering is the other way round — it goes to the Activity,
 * because a call you accept has to have somewhere to happen.
 */
class VoiceCallReceiver : BroadcastReceiver() {

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != ACTION_DECLINE) return
        val callId = intent.getStringExtra(EXTRA_CALL_ID)?.takeIf { it.isNotEmpty() } ?: return
        val app = context.applicationContext
        // Drop it locally first so the ring stops on the tap rather than on
        // the watcher's next poll — the same order [VoiceCalls.endLocally]
        // exists for, and the reason a declined call does not ring on for
        // another two seconds.
        VoiceCalls.endLocally(callId)
        // goAsync would hold the broadcast open for a loopback request that is
        // already fire-and-forget; the visor either hears it or the next poll
        // puts the call back (see endFailed).
        val pending = goAsync()
        CoroutineScope(SupervisorJob() + Dispatchers.IO).launch {
            runCatching { VisorApi.get(app).voiceDecline(callId) }
                .onFailure {
                    Log.w(TAG, "decline did not reach the visor", it)
                    VoiceCalls.endFailed(callId)
                }
            pending.finish()
        }
    }

    companion object {
        private const val TAG = "SkywireVoice"

        const val ACTION_DECLINE = "com.skycoin.skywire.call.DECLINE"
        const val EXTRA_CALL_ID = "call_id"
    }
}
