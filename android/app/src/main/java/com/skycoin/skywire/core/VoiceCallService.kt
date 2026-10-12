package com.skycoin.skywire.core

import android.Manifest
import android.app.Notification
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
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
import kotlinx.coroutines.launch
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Runs from the moment a call is placed until it ends, and does one thing:
 * lend the visor this phone's microphone and speaker ([VoiceAudioEngine])
 * while the call is connected.
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

    /** The foreground service types held now, so a type is never asked for twice. */
    @Volatile private var types = 0

    override fun onCreate() {
        super.onCreate()
        VoiceCallWatcher.ensureChannels(this)
        engine = VoiceAudioEngine(this)
        scope.launch {
            AppLocale.changes.collect {
                VoiceCallWatcher.ensureChannels(this@VoiceCallService)
                if (inForeground.get()) {
                    getSystemService(NotificationManager::class.java).notify(NOTIFICATION_ID, notification())
                }
            }
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        // Playback is always allowed; the microphone only with RECORD_AUDIO
        // granted (SecurityException otherwise) and only while the app is on
        // screen. A call placed from the screen is claimed here, before the
        // user can lock the phone, or a call answered after that records
        // silence.
        val claimed = ContextCompat.checkSelfPermission(this, Manifest.permission.RECORD_AUDIO) ==
            PackageManager.PERMISSION_GRANTED &&
            foreground(ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE)
        if (!claimed && !foreground(ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK)) {
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
        if (audio.get()) {
            engine.start(scope, onMicrophoneReady = {
                // The permission may have arrived mid-call. Promote before a
                // single frame is recorded.
                foreground(ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE)
            })
        }
        // Not sticky: a revived service with no call would hold the microphone
        // for nothing. The watcher starts us again if a call is still up.
        return START_NOT_STICKY
    }

    /** Add [type] to the types this service holds; a type already held is not asked for again. */
    private fun foreground(type: Int): Boolean {
        if ((types and type) == type) return true
        val next = types or type or ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK
        return runCatching { ServiceCompat.startForeground(this, NOTIFICATION_ID, notification(), next) }
            .onSuccess { types = next }
            .onFailure { Log.w(TAG, "could not enter the foreground as type $next", it) }
            .isSuccess
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        inForeground.set(false)
        engine.stop()
        scope.cancel()
        super.onDestroy()
    }

    private fun notification(): Notification {
        val text = AppLocale.localized(this)
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
            .setContentTitle(text.getString(R.string.call_ongoing_title))
            .setContentText(text.getString(R.string.call_ongoing_text))
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

        /** Whether the call is connected, so the microphone and speaker are lent to it. */
        private val audio = AtomicBoolean(false)

        /** Set once startForeground has been honoured, cleared on destroy. */
        private val inForeground = AtomicBoolean(false)

        /**
         * Bring the service up for a call being placed ([connected] false) or
         * one in progress. Started while the call is still being placed so the
         * microphone is claimed while the user is on screen; see onStartCommand.
         */
        fun start(context: Context, connected: Boolean) {
            wanted.set(true)
            audio.set(connected)
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
            audio.set(false)
            if (inForeground.get()) {
                context.stopService(Intent(context, VoiceCallService::class.java))
            }
        }
    }
}
