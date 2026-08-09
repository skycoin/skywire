package com.skycoin.skywire.ui.components

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.core.AppLock
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.core.AppVisibility
import com.skycoin.skywire.core.VoiceCalls

/**
 * The app lock: nothing is readable until the phone's own check passes.
 *
 * The lock is drawn *over* the app rather than replacing it. Composing the
 * whole navigation tree only while unlocked would tear down the back stack and
 * the embedded chat WebView on every glance at a notification, and rebuild
 * them on every return — a lock the user feels as slowness is a lock they turn
 * off. What keeps the content from leaking anyway is [android.view.WindowManager.LayoutParams.FLAG_SECURE],
 * set for the whole session while the lock is on (see MainActivity): the
 * recents preview is a black card and screenshots are refused, which is the
 * part an overlay could never cover — the system takes that snapshot as the
 * app leaves, before there is anything to overlay.
 *
 * A ringing or connected call is the one thing shown through the lock. A call
 * screen holds no secrets — a peer's key, answer, hang up — and every dialer on
 * Android surfaces one above the lock screen for the obvious reason: a phone
 * that cannot be answered until you authenticate is not answering.
 */
@Composable
fun BiometricGate(content: @Composable () -> Unit) {
    val context = LocalContext.current
    val prefs = remember(context) { AppPreferences(context) }
    // null until the store answers: covered, not exposed, while unknown.
    val enabled by prefs.boolean(AppLock.PREF_KEY, AppLock.DEFAULT)
        .collectAsState(initial = null)
    val locked by AppLock.isLocked.collectAsState()
    val foreground by AppVisibility.isForeground.collectAsState()
    val call by VoiceCalls.state.collectAsState()

    val showLock = enabled != false && locked && !call.busy

    Box(Modifier.fillMaxSize()) {
        content()
        if (showLock) {
            LockOverlay(
                // While the preference is still loading there is nothing to
                // ask about yet, so the overlay is just the splash, held.
                ready = enabled == true,
                foreground = foreground,
            )
        }
    }
}

@Composable
private fun LockOverlay(ready: Boolean, foreground: Boolean) {
    val context = LocalContext.current
    var inFlight by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    // Bumped by the Unlock button. Without it a dismissed prompt could never
    // be re-raised: the effect's other keys have not changed.
    var attempt by remember { mutableIntStateOf(0) }

    val title = stringResource(R.string.lock_prompt_title)
    val subtitle = stringResource(R.string.lock_prompt_subtitle)

    LaunchedEffect(ready, foreground, attempt) {
        if (!ready || !foreground || inFlight) return@LaunchedEffect
        val activity = context.findFragmentActivity() ?: return@LaunchedEffect
        inFlight = true
        error = null
        Biometrics.prompt(activity, title = title, subtitle = subtitle) { success, message ->
            inFlight = false
            if (success) AppLock.unlock() else error = message
        }
    }

    // Back on a lock screen puts the app away — it does not walk the
    // navigation stack of the screens underneath, which is what would happen
    // if this were left to the NavHost behind the overlay.
    BackHandler {
        context.findFragmentActivity()?.moveTaskToBack(true)
    }

    Box(
        Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
            // Swallows every gesture so nothing below is reachable.
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = {},
            ),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            modifier = Modifier.padding(32.dp),
        ) {
            Image(
                painter = painterResource(R.drawable.skywire_logo),
                contentDescription = null,
                modifier = Modifier.size(96.dp),
            )
            if (!ready) return@Column
            Spacer(Modifier.height(28.dp))
            Text(
                stringResource(R.string.lock_title),
                style = MaterialTheme.typography.titleMedium,
            )
            error?.let { message ->
                Spacer(Modifier.height(8.dp))
                Text(
                    message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    textAlign = TextAlign.Center,
                )
            }
            Spacer(Modifier.height(24.dp))
            Button(onClick = { attempt++ }, enabled = !inFlight) {
                Text(stringResource(R.string.lock_unlock))
            }
        }
    }
}
