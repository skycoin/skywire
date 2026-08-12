package com.skycoin.skywire.core

import android.app.Notification
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.IBinder
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import com.skycoin.skywire.MainActivity
import com.skycoin.skywire.R
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel

/**
 * Runs for exactly as long as a call is connected, and does one thing: lend
 * the visor this phone's microphone and speaker ([VoiceAudioEngine]).
 *
 * **Why a service of its own** rather than a few coroutines in the core
 * service. Recording from the background is only allowed to a foreground
 * service that declares `microphone`, and declaring that on the always-on core
 * service would claim the microphone for every minute the visor is connected —
 * true for seconds a day, and visible to the user as a permanent in-use
 * indicator. Here the claim starts and ends with the call, which is also what
 * makes the privacy indicator mean something.
 *
 * Started and stopped by [VoiceCallWatcher] off the visor's own call list, so
 * a call answered from the notification, from the chat page, or from anywhere
 * else lands here the same way.
 */
class VoiceCallService : android.app.Service() {

    /** Its call notification is the user's, so it follows the language. */
    override fun attachBaseContext(newBase: Context) {
        super.attachBaseContext(AppLocale.wrap(newBase))
    }

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var engine: VoiceAudioEngine

    override fun onCreate() {
        super.onCreate()
        VoiceCallWatcher.ensureChannels(this)
        engine = VoiceAudioEngine(this)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        // Starts as playback-only, and that is not a formality: the platform
        // REFUSES a `microphone` foreground service outright — SecurityException,
        // not a silent mute — unless RECORD_AUDIO is already granted. A call can
        // arrive before the user has ever been asked, so claiming the microphone
        // up front would crash the app on the very call that needed it.
        foreground(ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK)
        engine.start(scope, onMicrophoneReady = {
            // Promote before a single frame is recorded. Recording from the
            // background is what the type buys, and a call may well be running
            // with the app off-screen.
            foreground(ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE)
        })
        // Not sticky: a revived service with no call would hold the microphone
        // for nothing. The watcher starts us again if a call is still up.
        return START_NOT_STICKY
    }

    private fun foreground(type: Int) {
        runCatching { ServiceCompat.startForeground(this, NOTIFICATION_ID, notification(), type) }
            .onFailure { Log.w(TAG, "could not enter the foreground as type $type", it) }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        engine.stop()
        scope.cancel()
        super.onDestroy()
    }

    private fun notification(): Notification {
        val open = PendingIntent.getActivity(
            this,
            0,
            // An explicit action, or StrictMode flags it as an unsafe intent
            // launch on API 35+.
            Intent(this, MainActivity::class.java).setAction(Intent.ACTION_MAIN),
            PendingIntent.FLAG_IMMUTABLE,
        )
        return NotificationCompat.Builder(this, VoiceCallWatcher.CHANNEL_ONGOING)
            .setSmallIcon(R.drawable.skywire_logo)
            .setContentTitle(getString(R.string.call_ongoing_title))
            .setContentText(getString(R.string.call_ongoing_text))
            .setCategory(NotificationCompat.CATEGORY_CALL)
            .setContentIntent(open)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .build()
    }

    companion object {
        private const val TAG = "SkywireVoice"
        private const val NOTIFICATION_ID = 3

        fun start(context: Context) {
            ContextCompat.startForegroundService(
                context,
                Intent(context, VoiceCallService::class.java),
            )
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, VoiceCallService::class.java))
        }
    }
}
