package com.skycoin.skywire.ui.components

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.skycoin.skywire.R

/**
 * The visor's own English words, put into the user's language on the way to
 * the screen.
 *
 * The visor reports what an app is doing, and how its services are, as prose
 * it wrote itself — `detailed_status` is the string in
 * `pkg/app/appserver/app_state.go`, and `services_health` is the one in
 * `pkg/visor/api.go`. Both arrive as English text rather than as a code, so
 * they are matched as strings; that is what the API sends.
 *
 * Everything unrecognised is shown exactly as it arrived. A status this list
 * has never heard of — a newer visor, a state added upstream — is still worth
 * reading in English, and is certainly worth more than a blank or a guess.
 * That is also what keeps this file from being a place a release can break:
 * fall through, never fail.
 */

/**
 * The values `detailed_status` can carry. Kept here rather than next to one
 * screen because SkySOCKS, SkyVPN and the Apps hub all read the same field —
 * three copies of `"Running"` is one typo away from a status that never
 * matches.
 */
object AppStatus {
    const val STARTING = "Starting"
    const val RUNNING = "Running"
    const val CONNECTING = "Connecting"
    const val RECONNECTING = "Connection failed, reconnecting"
    const val SHUTTING_DOWN = "Shutting down"
    const val STOPPED = "Stopped"
}

/** What the visor says an app is doing, in the user's language. */
@Composable
fun appStatusText(detailedStatus: String): String = when {
    detailedStatus.equals(AppStatus.STARTING, ignoreCase = true) ->
        stringResource(R.string.app_status_starting)
    detailedStatus.equals(AppStatus.RUNNING, ignoreCase = true) ->
        stringResource(R.string.app_status_running)
    detailedStatus.equals(AppStatus.CONNECTING, ignoreCase = true) ->
        stringResource(R.string.app_status_connecting)
    detailedStatus.equals(AppStatus.RECONNECTING, ignoreCase = true) ->
        stringResource(R.string.app_status_reconnecting)
    detailedStatus.equals(AppStatus.SHUTTING_DOWN, ignoreCase = true) ->
        stringResource(R.string.app_status_shutting_down)
    detailedStatus.equals(AppStatus.STOPPED, ignoreCase = true) ->
        stringResource(R.string.app_status_stopped)
    else -> detailedStatus
}

/**
 * How a service reports itself: the aggregate `services_health` on a visor
 * card, and the per-service rows behind it. `healthy` and `connecting` are the
 * aggregate's two values; the per-service rows add `unhealthy` and `error`.
 */
@Composable
fun healthText(status: String): String = when {
    status.equals(HEALTHY, ignoreCase = true) -> stringResource(R.string.health_healthy)
    status.equals("unhealthy", ignoreCase = true) -> stringResource(R.string.health_unhealthy)
    status.equals("connecting", ignoreCase = true) -> stringResource(R.string.health_connecting)
    status.equals("error", ignoreCase = true) -> stringResource(R.string.health_error)
    else -> status
}

/** The one value that also decides a colour, so it is named twice over. */
const val HEALTHY = "healthy"
