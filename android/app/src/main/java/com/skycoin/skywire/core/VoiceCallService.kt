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
import java.util.concurrent.atomic.AtomicBoolean

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
        if (!foreground(ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK)) {
            // Never in the foreground, so the platform's clock on the start is
            // still running; stopping now is what stops it.
            stopSelf()
            return START_NOT_STICKY
        }
        inForeground.set(true)
        if (!wanted.get()) {
            // The call ended before this got going — see stop(). Now that the
            // start has been honoured, the stop can be.
            stopSelf()
            return START_NOT_STICKY
        }
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

    private fun foreground(type: Int): Boolean =
        runCatching { ServiceCompat.startForeground(this, NOTIFICATION_ID, notification(), type) }
            .onFailure { Log.w(TAG, "could not enter the foreground as type $type", it) }
            .isSuccess

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        inForeground.set(false)
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

        /** Whether a call currently wants this service up: set by [start], cleared by [stop]. */
        private val wanted = AtomicBoolean(false)

        /** Set once startForeground has been honoured, cleared on destroy. */
        private val inForeground = AtomicBoolean(false)

        fun start(context: Context) {
            wanted.set(true)
            ContextCompat.startForegroundService(
                context,
                Intent(context, VoiceCallService::class.java),
            )
        }

        /**
         * Stop the service — but never from outside while it is still starting.
         *
         * `startForegroundService` starts a clock: the service must reach
         * `startForeground` within seconds or the platform crashes the whole
         * app, and it does so even when the service was stopped in between. A
         * call that dropped eight seconds after connecting, on a phone whose
         * main thread was busy tearing the chat page down for the call screen,
         * had this service stopped before its `onStartCommand` had run —
         * "Bringing down service while still waiting for start foreground",
         * then ForegroundServiceDidNotStartInTimeException took the process.
         * So while the start is pending, only the wish is recorded, and the
         * service stops itself the moment it has entered the foreground.
         */
        fun stop(context: Context) {
            wanted.set(false)
            if (inForeground.get()) {
                context.stopService(Intent(context, VoiceCallService::class.java))
            }
        }
    }
}
