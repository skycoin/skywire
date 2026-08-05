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
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.BufferedReader
import java.io.IOException
import kotlin.coroutines.coroutineContext

/** One notification the visor's apps published. */
@Serializable
internal data class NotifyEvent(
    @SerialName("app") val app: String = "",
    @SerialName("title") val title: String = "",
    @SerialName("body") val body: String = "",
    @SerialName("tag") val tag: String = "",
)

/**
 * Turns what the visor's apps publish into Android notifications.
 *
 * The visor already collects them: skychat hands every inbound message — text,
 * a file, a voice or video clip, a group message, a join request — to the
 * visor's notification hub, which streams them at `/api/notifications/stream`.
 * All that is missing on a phone is a consumer, and this is it.
 *
 * skychat suppresses the publish while a UI that can notify for itself is
 * attached, so this does NOT double up with the chat page's own alerts: the
 * embedded page has no notification permission of its own, reports itself
 * incapable, and the messages come here instead.
 *
 * Runs off the core service, so a message notification does not depend on any
 * screen being open — which is the only version of the feature worth having.
 */
internal class MessageNotifications(context: Context) {

    private val app = context.applicationContext
    private val api = VisorApi.get(app)
    private val notifications = NotificationManagerCompat.from(app)
    private val json = Json { ignoreUnknownKeys = true; isLenient = true }

    fun watch(scope: CoroutineScope): Job = scope.launch(Dispatchers.IO) {
        ensureChannel(app)
        while (coroutineContext.isActive) {
            try {
                api.notificationStream().use { resp ->
                    if (!resp.isSuccessful) {
                        Log.w(TAG, "notification stream rejected: ${resp.code}")
                        return@use
                    }
                    consume(resp.body.charStream().buffered())
                }
            } catch (e: IOException) {
                Log.d(TAG, "notification stream ended: ${e.message}")
            }
            // The visor restarting, the phone waking — reconnect rather than
            // going quiet for the rest of the session.
            coroutineContext.ensureActive()
            delay(RETRY_MS)
        }
    }

    /** Minimal SSE: `data: {json}` lines, blank-line separated, `:` = ping. */
    private suspend fun consume(reader: BufferedReader) {
        while (coroutineContext.isActive) {
            val line = reader.readLine() ?: return
            val payload = line.removePrefix(DATA_PREFIX).takeIf { it != line } ?: continue
            val event = runCatching { json.decodeFromString(NotifyEvent.serializer(), payload) }
                .getOrNull() ?: continue
            if (event.title.isEmpty() && event.body.isEmpty()) continue
            show(event)
        }
    }

    private fun show(event: NotifyEvent) {
        val open = PendingIntent.getActivity(
            app,
            0,
            Intent(app, MainActivity::class.java).setAction(Intent.ACTION_MAIN),
            PendingIntent.FLAG_IMMUTABLE,
        )
        val note = NotificationCompat.Builder(app, CHANNEL_MESSAGES)
            .setSmallIcon(R.drawable.skywire_logo)
            .setContentTitle(event.title.ifEmpty { app.getString(R.string.app_skychat) })
            .setContentText(event.body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(event.body))
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setContentIntent(open)
            .setAutoCancel(true)
            .build()
        // The publisher's tag is the conversation: notifying under it replaces
        // that conversation's previous alert instead of stacking one per
        // message. Without a tag every message is its own notification, which
        // is the right fallback for something that isn't a conversation.
        val tag = event.tag.ifEmpty { null }
        val id = if (tag != null) MESSAGE_NOTIFICATION_ID else event.hashCode()
        runCatching { notifications.notify(tag, id, note) }
            .onFailure { Log.w(TAG, "cannot post a message notification", it) }
    }

    companion object {
        private const val TAG = "SkywireNotify"
        private const val DATA_PREFIX = "data: "
        private const val RETRY_MS = 2_000L
        private const val MESSAGE_NOTIFICATION_ID = 100
        const val CHANNEL_MESSAGES = "messages"

        fun ensureChannel(context: Context) {
            val manager =
                context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
            manager.createNotificationChannel(
                NotificationChannel(
                    CHANNEL_MESSAGES,
                    context.getString(R.string.message_channel),
                    NotificationManager.IMPORTANCE_HIGH,
                ).apply { description = context.getString(R.string.message_channel_description) },
            )
        }
    }
}
