package com.skycoin.skywire.core

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.media.MediaMetadata
import android.media.session.MediaSession
import android.media.session.PlaybackState
import com.skycoin.skywire.MainActivity
import com.skycoin.skywire.R
import com.skycoin.skywire.chat.AudioPlayback
import com.skycoin.skywire.chat.ChatAudioPlayer

/**
 * Media controls in the notification shade (and media keys, lock screen) for
 * the voice message or audio clip SkyChat is playing, so a long one can be
 * paused or skipped without opening the app.
 *
 * No service of its own: [SkywireCoreService] is already a foreground service
 * whenever there is a chat to play from, so the process is foreground and its
 * audio may keep going. A media service on top would be a second always-on
 * notification for something that only exists while a clip plays.
 */
object ChatMedia {

    private const val CHANNEL_ID = "chat-media"
    private const val NOTIFICATION_ID = 4

    /** What the skip buttons move by; their labels say the same number. */
    private const val SEEK_STEP_SECONDS = 10
    private const val SEEK_STEP_MS = SEEK_STEP_SECONDS * 1000L

    private const val ACTION_PLAY = "com.skycoin.skywire.media.PLAY"
    private const val ACTION_PAUSE = "com.skycoin.skywire.media.PAUSE"
    private const val ACTION_BACK = "com.skycoin.skywire.media.BACK"
    private const val ACTION_FORWARD = "com.skycoin.skywire.media.FORWARD"
    private const val ACTION_STOP = "com.skycoin.skywire.media.STOP"

    private var session: MediaSession? = null

    /** A notification button, routed back into the player. */
    fun onAction(context: Context, action: String) {
        when (action) {
            ACTION_PLAY -> ChatAudioPlayer.resume()
            ACTION_PAUSE -> ChatAudioPlayer.pause()
            ACTION_BACK -> ChatAudioPlayer.seekBy(-SEEK_STEP_MS)
            ACTION_FORWARD -> ChatAudioPlayer.seekBy(SEEK_STEP_MS)
            ACTION_STOP -> {
                ChatAudioPlayer.stop()
                clear(context)
            }
        }
    }

    /** Post or refresh the session and notification for [state]. */
    fun show(context: Context, state: AudioPlayback, title: String, artist: String) {
        val localized = AppLocale.localized(context)
        val session = session(context)
        session.setMetadata(
            MediaMetadata.Builder()
                .putString(MediaMetadata.METADATA_KEY_TITLE, title.ifEmpty { localized.getString(R.string.chat_media_default_title) })
                .putString(MediaMetadata.METADATA_KEY_ARTIST, artist.ifEmpty { "SkyChat" })
                .putString(MediaMetadata.METADATA_KEY_ALBUM, "SkyChat")
                .putLong(MediaMetadata.METADATA_KEY_DURATION, if (state.durationMs > 0) state.durationMs else -1L)
                .build(),
        )
        // Position plus rate: the system extrapolates the scrubber from these two.
        session.setPlaybackState(
            PlaybackState.Builder()
                .setActions(
                    PlaybackState.ACTION_PLAY or PlaybackState.ACTION_PAUSE or
                        PlaybackState.ACTION_PLAY_PAUSE or PlaybackState.ACTION_SEEK_TO or
                        PlaybackState.ACTION_STOP or PlaybackState.ACTION_REWIND or
                        PlaybackState.ACTION_FAST_FORWARD,
                )
                .setState(
                    if (state.playing) PlaybackState.STATE_PLAYING else PlaybackState.STATE_PAUSED,
                    state.positionMs,
                    if (state.playing) state.speed else 0f,
                )
                .build(),
        )
        session.isActive = true
        notificationManager(context).notify(NOTIFICATION_ID, notification(localized, session, title, artist, state.playing))
    }

    fun clear(context: Context) {
        notificationManager(context).cancel(NOTIFICATION_ID)
        session?.let { live ->
            live.isActive = false
            live.release()
        }
        session = null
    }

    private fun notification(context: Context, session: MediaSession, title: String, artist: String, playing: Boolean): Notification {
        channel(context)
        val open = PendingIntent.getActivity(
            context,
            0,
            Intent(context, MainActivity::class.java).setAction(Intent.ACTION_MAIN),
            PendingIntent.FLAG_IMMUTABLE,
        )
        return Notification.Builder(context, CHANNEL_ID)
            .setSmallIcon(R.drawable.skywire_logo)
            .setContentTitle(title.ifEmpty { context.getString(R.string.chat_media_default_title) })
            .setContentText(artist.ifEmpty { "SkyChat" })
            .setContentIntent(open)
            .setDeleteIntent(button(context, ACTION_STOP))
            .setVisibility(Notification.VISIBILITY_PUBLIC)
            .setOnlyAlertOnce(true)
            .setOngoing(playing)
            .addAction(action(context, R.drawable.ic_media_back, context.getString(R.string.chat_media_back, SEEK_STEP_SECONDS), ACTION_BACK))
            .addAction(
                if (playing) {
                    action(context, R.drawable.ic_media_pause, context.getString(R.string.chat_media_pause), ACTION_PAUSE)
                } else {
                    action(context, R.drawable.ic_media_play, context.getString(R.string.chat_media_play), ACTION_PLAY)
                },
            )
            .addAction(action(context, R.drawable.ic_media_forward, context.getString(R.string.chat_media_forward, SEEK_STEP_SECONDS), ACTION_FORWARD))
            .setStyle(Notification.MediaStyle().setMediaSession(session.sessionToken).setShowActionsInCompactView(0, 1, 2))
            .build()
    }

    private fun action(context: Context, icon: Int, title: String, act: String): Notification.Action =
        Notification.Action.Builder(android.graphics.drawable.Icon.createWithResource(context, icon), title, button(context, act)).build()

    private fun button(context: Context, act: String): PendingIntent = PendingIntent.getBroadcast(
        context,
        act.hashCode(),
        Intent(context, ChatMediaReceiver::class.java).setAction(act),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
    )

    private fun session(context: Context): MediaSession =
        session ?: MediaSession(context, "SkyChat").apply {
            setCallback(object : MediaSession.Callback() {
                override fun onPlay() = ChatAudioPlayer.resume()
                override fun onPause() = ChatAudioPlayer.pause()
                override fun onStop() {
                    ChatAudioPlayer.stop()
                    clear(context)
                }

                override fun onRewind() = ChatAudioPlayer.seekBy(-SEEK_STEP_MS)
                override fun onFastForward() = ChatAudioPlayer.seekBy(SEEK_STEP_MS)
                override fun onSeekTo(pos: Long) = ChatAudioPlayer.seekTo(pos)
            })
        }.also { session = it }

    private fun notificationManager(context: Context): NotificationManager =
        context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager

    private fun channel(context: Context) {
        val channel = NotificationChannel(
            CHANNEL_ID,
            context.getString(R.string.chat_media_channel_name),
            // Transport controls, not an alert: it appears because play was pressed.
            NotificationManager.IMPORTANCE_LOW,
        ).apply {
            description = context.getString(R.string.chat_media_channel_description)
            setShowBadge(false)
        }
        notificationManager(context).createNotificationChannel(channel)
    }
}
