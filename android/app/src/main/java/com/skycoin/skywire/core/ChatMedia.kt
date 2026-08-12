package com.skycoin.skywire.core

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.media.MediaMetadata
import android.media.session.MediaSession
import android.media.session.PlaybackState
import android.os.Handler
import android.os.Looper
import android.util.Base64
import android.webkit.JavascriptInterface
import android.webkit.WebView
import com.skycoin.skywire.MainActivity
import com.skycoin.skywire.R
import org.json.JSONObject
import java.lang.ref.WeakReference

/**
 * Media controls in the notification shade for whatever SkyChat is playing.
 *
 * **Why this exists at all.** The chat UI is a web page, and the web already
 * has the right API for this: `navigator.mediaSession`, which on a desktop
 * browser and on Chrome for Android puts the playing clip on the lock screen
 * and under the media keys. Android **WebView implements that API and then
 * connects it to nothing** — the JS calls succeed, the metadata is accepted,
 * and no session ever reaches the platform. Measured on-device while a voice
 * message was playing from the embedded page: `dumpsys media_session` reported
 * `Sessions Stack - have 0 sessions` at the same moment `Audio playback` named
 * this package. The bridging code lives in Chrome-the-browser, not in WebView.
 *
 * So the page reports what it is playing across a JS interface, and this posts
 * the session and the notification that WebView will not. The page keeps its
 * `navigator.mediaSession` calls either way — they are what works when the same
 * UI is opened in a real browser, and they cost nothing here.
 *
 * No service of its own: [SkywireCoreService] is already a foreground service
 * whenever there is a chat to play from, so the process is foreground and its
 * audio is allowed to keep going. A media service on top would be a second
 * always-on notification for something that only exists while a clip plays.
 */
object ChatMedia {

    private const val CHANNEL_ID = "chat-media"
    private const val NOTIFICATION_ID = 3

    /**
     * What the notification's skip buttons move by. The seconds are also what
     * their labels say, so a step changed here changes both.
     */
    private const val SEEK_STEP_SECONDS = 10
    private const val SEEK_STEP_MS = SEEK_STEP_SECONDS * 1000L

    private const val ACTION_PLAY = "com.skycoin.skywire.media.PLAY"
    private const val ACTION_PAUSE = "com.skycoin.skywire.media.PAUSE"
    private const val ACTION_BACK = "com.skycoin.skywire.media.BACK"
    private const val ACTION_FORWARD = "com.skycoin.skywire.media.FORWARD"
    private const val ACTION_STOP = "com.skycoin.skywire.media.STOP"

    private val main = Handler(Looper.getMainLooper())

    private var session: MediaSession? = null
    private var page: WeakReference<WebView>? = null

    @Volatile
    private var showing = false

    /**
     * Give the chat page a way to report what it is playing, and this a way to
     * drive it back. Called once per WebView; [detach] undoes it.
     */
    fun attach(view: WebView) {
        page = WeakReference(view)
        view.addJavascriptInterface(Bridge(view.context.applicationContext), "SkywireMedia")
    }

    /**
     * The page is going away. The notification goes with it: the WebView is
     * the player, so a shade full of controls for a destroyed page would be
     * buttons that do nothing.
     */
    fun detach(view: WebView) {
        if (page?.get() === view) page = null
        runCatching { view.removeJavascriptInterface("SkywireMedia") }
        clear(view.context.applicationContext)
    }

    /** Route a notification button back into the page. */
    fun onAction(context: Context, action: String) {
        when (action) {
            ACTION_PLAY -> call("play")
            ACTION_PAUSE -> call("pause")
            ACTION_BACK -> call("seekby", (-SEEK_STEP_MS / 1000).toString())
            ACTION_FORWARD -> call("seekby", (SEEK_STEP_MS / 1000).toString())
            ACTION_STOP -> {
                call("stop")
                clear(context)
            }
        }
    }

    // --- the page's side ---

    /**
     * The JS interface the page talks to. Every method is called on a WebView
     * worker thread, so everything it touches hops to the main thread first —
     * a JavascriptInterface method that touched the WebView directly would
     * crash.
     */
    private class Bridge(private val context: Context) {

        @JavascriptInterface
        fun update(json: String) {
            val state = runCatching { JSONObject(json) }.getOrNull() ?: return
            main.post { show(context, state) }
        }

        @JavascriptInterface
        fun clear() {
            main.post { clear(context) }
        }
    }

    private fun call(action: String, value: String? = null) {
        main.post {
            val view = page?.get() ?: return@post
            val arg = if (value == null) "" else ", ${JSONObject.quote(value)}"
            view.evaluateJavascript(
                "window.app && app.mediaAction && app.mediaAction(${JSONObject.quote(action)}$arg)",
                null,
            )
        }
    }

    // --- the shade's side ---

    private fun show(context: Context, state: JSONObject) {
        val playing = state.optBoolean("playing", false)
        val positionMs = (state.optDouble("position", 0.0) * 1000).toLong()
        val durationMs = (state.optDouble("duration", 0.0) * 1000).toLong()
        // The rate matters to more than the audio: the shade's own scrubber
        // advances at whatever speed the session reports, so a clip sped up in
        // the page and left at 1x here would drift visibly against itself.
        val rate = state.optDouble("rate", 1.0).toFloat().takeIf { it > 0f } ?: 1f

        val session = session(context)
        session.setMetadata(
            MediaMetadata.Builder()
                .putString(
                    MediaMetadata.METADATA_KEY_TITLE,
                    state.optString("title", context.getString(R.string.chat_media_default_title)),
                )
                .putString(MediaMetadata.METADATA_KEY_ARTIST, state.optString("artist", "SkyChat"))
                .putString(MediaMetadata.METADATA_KEY_ALBUM, "SkyChat")
                .apply {
                    // A duration of -1 tells the shade "unknown", which draws no
                    // scrubber at all — better than a bar that cannot move. A
                    // clip recorded in the composer has no duration in its
                    // header until the page has scanned it.
                    putLong(
                        MediaMetadata.METADATA_KEY_DURATION,
                        if (durationMs > 0) durationMs else -1L,
                    )
                    artwork(state.optString("artwork"))?.let {
                        putBitmap(MediaMetadata.METADATA_KEY_ALBUM_ART, it)
                    }
                }
                .build(),
        )
        session.setPlaybackState(
            PlaybackState.Builder()
                .setActions(
                    PlaybackState.ACTION_PLAY or PlaybackState.ACTION_PAUSE or
                        PlaybackState.ACTION_PLAY_PAUSE or PlaybackState.ACTION_SEEK_TO or
                        PlaybackState.ACTION_STOP or PlaybackState.ACTION_REWIND or
                        PlaybackState.ACTION_FAST_FORWARD,
                )
                // Position plus rate, not position alone: the system
                // extrapolates the scrubber from these two, which is why the
                // page does not have to report a position four times a second.
                .setState(
                    if (playing) PlaybackState.STATE_PLAYING else PlaybackState.STATE_PAUSED,
                    positionMs,
                    if (playing) rate else 0f,
                )
                .build(),
        )
        session.isActive = true

        notificationManager(context).notify(
            NOTIFICATION_ID,
            notification(context, session, state, playing),
        )
        showing = true
    }

    private fun clear(context: Context) {
        if (!showing && session == null) return
        showing = false
        notificationManager(context).cancel(NOTIFICATION_ID)
        session?.let { live ->
            live.isActive = false
            live.release()
        }
        session = null
    }

    private fun notification(
        context: Context,
        session: MediaSession,
        state: JSONObject,
        playing: Boolean,
    ): Notification {
        channel(context)
        val open = PendingIntent.getActivity(
            context,
            0,
            Intent(context, MainActivity::class.java).setAction(Intent.ACTION_MAIN),
            PendingIntent.FLAG_IMMUTABLE,
        )
        return Notification.Builder(context, CHANNEL_ID)
            .setSmallIcon(R.drawable.skywire_logo)
            .setContentTitle(
                state.optString("title", context.getString(R.string.chat_media_default_title)),
            )
            .setContentText(state.optString("artist", "SkyChat"))
            .setContentIntent(open)
            .setDeleteIntent(button(context, ACTION_STOP))
            .setVisibility(Notification.VISIBILITY_PUBLIC)
            .setOnlyAlertOnce(true)
            .setOngoing(playing)
            .addAction(
                action(
                    context,
                    R.drawable.ic_media_back,
                    context.getString(R.string.chat_media_back, SEEK_STEP_SECONDS),
                    ACTION_BACK,
                ),
            )
            .addAction(
                if (playing) {
                    action(
                        context,
                        R.drawable.ic_media_pause,
                        context.getString(R.string.chat_media_pause),
                        ACTION_PAUSE,
                    )
                } else {
                    action(
                        context,
                        R.drawable.ic_media_play,
                        context.getString(R.string.chat_media_play),
                        ACTION_PLAY,
                    )
                },
            )
            .addAction(
                action(
                    context,
                    R.drawable.ic_media_forward,
                    context.getString(R.string.chat_media_forward, SEEK_STEP_SECONDS),
                    ACTION_FORWARD,
                ),
            )
            .setStyle(
                Notification.MediaStyle()
                    .setMediaSession(session.sessionToken)
                    // All three in the collapsed row: skipping is most of what
                    // a voice message needs, and hiding it behind an expand
                    // makes it slower than opening the app.
                    .setShowActionsInCompactView(0, 1, 2),
            )
            .build()
    }

    private fun action(context: Context, icon: Int, title: String, act: String): Notification.Action =
        Notification.Action.Builder(
            android.graphics.drawable.Icon.createWithResource(context, icon),
            title,
            button(context, act),
        ).build()

    private fun button(context: Context, act: String): PendingIntent = PendingIntent.getBroadcast(
        context,
        act.hashCode(),
        Intent(context, ChatMediaReceiver::class.java).setAction(act),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
    )

    private fun session(context: Context): MediaSession =
        session ?: MediaSession(context, "SkyChat").apply {
            setCallback(object : MediaSession.Callback() {
                override fun onPlay() = call("play")
                override fun onPause() = call("pause")
                override fun onStop() {
                    call("stop")
                    clear(context)
                }

                override fun onRewind() = call("seekby", (-SEEK_STEP_MS / 1000).toString())
                override fun onFastForward() = call("seekby", (SEEK_STEP_MS / 1000).toString())

                /** The shade's scrubber, dragged. */
                override fun onSeekTo(pos: Long) = call("seekto", (pos / 1000.0).toString())
            })
        }.also { session = it }

    /**
     * The peer avatar the page sent, as a bitmap. It arrives as whatever the
     * page had on screen: a `data:` URL for a picture the user set, or a URL
     * for the default. Only the first is decoded — fetching over HTTP from
     * here would need the chat's own credential, and a missing thumbnail is
     * not worth that.
     */
    private fun artwork(src: String?): Bitmap? {
        val payload = src?.substringAfter("base64,", "")?.takeIf { it.isNotEmpty() } ?: return null
        return runCatching {
            val bytes = Base64.decode(payload, Base64.DEFAULT)
            BitmapFactory.decodeByteArray(bytes, 0, bytes.size)
        }.getOrNull()
    }

    private fun notificationManager(context: Context): NotificationManager =
        context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager

    private fun channel(context: Context) {
        val channel = NotificationChannel(
            CHANNEL_ID,
            context.getString(R.string.chat_media_channel_name),
            // Transport controls, not an alert: this appears because the user
            // pressed play, so it must never make a sound of its own.
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = context.getString(R.string.chat_media_channel_description)
            setShowBadge(false)
        }
        notificationManager(context).createNotificationChannel(channel)
    }
}
