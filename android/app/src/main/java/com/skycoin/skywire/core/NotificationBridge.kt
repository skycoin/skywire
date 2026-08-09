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

/** One notification, exactly as the visor's hub publishes it. */
@Serializable
internal data class NotifyEvent(
    /** The publishing app, stamped by the visor — an app cannot forge it. */
    @SerialName("app") val app: String = "",
    @SerialName("title") val title: String = "",
    @SerialName("body") val body: String = "",
    /** Groups related notifications so a new one REPLACES its predecessor. */
    @SerialName("tag") val tag: String = "",
)

/**
 * This phone as a sink for the visor's notification hub.
 *
 * The hub is the visor's, not any one app's: an app — or the visor itself —
 * publishes a NotifyReq and stops caring, and the hub picks the tier that can
 * actually reach the user (an attached UI, a subscribed host app, the desktop
 * notification center, or nowhere at all on a headless box). This class is that
 * subscribed host app. Everything it knows about a notification is what the hub
 * sends: an app name, a title, a body, an optional tag.
 *
 * **Nothing here is per-feature, and that is the point.** A notification that
 * does not exist yet — a market alert, a transport going down, a visor coming
 * back online — needs one Publish call on the Go side and NO change in this
 * file: an app nobody has heard of gets a channel named after itself at default
 * importance, and the user gets a real switch for it in system settings the day
 * it first appears. [CHANNELS] exists only to give the apps we DO know a better
 * label and a deliberate importance, so tuning one is a single row.
 *
 * Runs off the core service, so a notification never depends on a screen being
 * open — which is the only version of the feature worth having.
 */
internal class NotificationBridge(context: Context) {

    private val app = context.applicationContext
    private val notifications = NotificationManagerCompat.from(app)
    private val manager =
        app.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
    private val api = VisorApi.get(app)
    private val json = Json { ignoreUnknownKeys = true; isLenient = true }

    /** Channels registered this session, so each is created once. */
    private val ensured = mutableSetOf<String>()

    /** Untagged notifications stack, so each needs an id of its own. */
    private var untagged = UNTAGGED_ID_BASE

    fun watch(scope: CoroutineScope): Job = scope.launch(Dispatchers.IO) {
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
            // going quiet for the rest of the session. The hub is live-only
            // (it keeps no backlog), so anything published while this is down
            // is gone; reconnecting promptly is the whole mitigation.
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
        val channel = channelFor(event.app)
        val open = PendingIntent.getActivity(
            app,
            0,
            Intent(app, MainActivity::class.java).setAction(Intent.ACTION_MAIN),
            PendingIntent.FLAG_IMMUTABLE,
        )
        val builder = NotificationCompat.Builder(app, channel.id)
            .setSmallIcon(R.drawable.skywire_logo)
            .setContentTitle(event.title.ifEmpty { channel.label })
            .setContentText(event.body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(event.body))
            .setContentIntent(open)
            .setAutoCancel(true)
        channel.category?.let(builder::setCategory)

        // The publisher's tag is its own idea of "this again" — a conversation,
        // a market, a link. Namespacing it by app is what stops two apps that
        // both say "lifecycle" from silently replacing each other's alerts.
        // Untagged means "not the same as anything", so those stack.
        val tag = event.tag.takeIf { it.isNotEmpty() }?.let { "${event.app}:$it" }
        val id = if (tag != null) TAGGED_ID else untagged++
        runCatching { notifications.notify(tag, id, builder.build()) }
            .onFailure { Log.w(TAG, "cannot post a notification from ${event.app}", it) }
    }

    /**
     * The channel an app's notifications belong in, created on first use.
     *
     * An unknown app is not an error and is never dropped — it gets a channel
     * of its own, named after itself.
     */
    private fun channelFor(publisher: String): Channel {
        val channel = CHANNELS[publisher] ?: Channel(
            id = "app_" + publisher.ifEmpty { "visor" }.lowercase().replace(UNSAFE_ID, "_"),
            label = publisher.ifEmpty { app.getString(R.string.app_name) },
            importance = NotificationManager.IMPORTANCE_DEFAULT,
            category = null,
        )
        if (ensured.add(channel.id)) {
            manager.createNotificationChannel(
                NotificationChannel(channel.id, channel.label, channel.importance),
            )
        }
        return channel
    }

    /** How one app's notifications are presented. */
    private data class Channel(
        val id: String,
        val label: String,
        val importance: Int,
        val category: String?,
    )

    companion object {
        private const val TAG = "SkywireNotify"
        private const val DATA_PREFIX = "data: "
        private const val RETRY_MS = 2_000L

        /** One id for every tagged notification; the tag is what separates them. */
        private const val TAGGED_ID = 100
        private const val UNTAGGED_ID_BASE = 1_000

        private val UNSAFE_ID = Regex("[^a-z0-9_]")

        /**
         * Presentation for the apps we already know. Everything else is handled
         * by [channelFor]'s fallback, so this table is a nicety and never a
         * gate: adding a row changes an app's label or how loudly it
         * interrupts; adding a NOTIFICATION needs no row at all.
         */
        private val CHANNELS = mapOf(
            "skychat" to Channel(
                id = "app_skychat",
                label = "SkyChat",
                // A message from a person interrupts — that is what a chat is.
                importance = NotificationManager.IMPORTANCE_HIGH,
                category = NotificationCompat.CATEGORY_MESSAGE,
            ),
            "skydex-client" to Channel(
                id = "app_skydex",
                label = "SkyDEX",
                importance = NotificationManager.IMPORTANCE_DEFAULT,
                category = null,
            ),
            // The visor's own events — transports, reachability, updates.
            // Informational, and deliberately quieter than a message.
            "visor" to Channel(
                id = "app_visor",
                label = "Skywire",
                importance = NotificationManager.IMPORTANCE_LOW,
                category = NotificationCompat.CATEGORY_STATUS,
            ),
        )
    }
}
