package com.skycoin.skywire

import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.view.WindowManager
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.lifecycleScope
import com.skycoin.skywire.core.AppLocale
import com.skycoin.skywire.core.AppLock
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.core.AppVisibility
import com.skycoin.skywire.core.DeepLinks
import com.skycoin.skywire.core.VoiceCallWatcher
import com.skycoin.skywire.core.VoiceCallReceiver
import com.skycoin.skywire.core.ThemeMode
import com.skycoin.skywire.core.VoiceCalls
import com.skycoin.skywire.ui.SkywireApp
import com.skycoin.skywire.ui.components.BiometricGate
import com.skycoin.skywire.ui.theme.SkywireTheme
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch

/**
 * The one Activity: splash → [BiometricGate] → scaffold + NavHost.
 *
 * A [FragmentActivity] rather than a plain ComponentActivity because
 * `BiometricPrompt` hosts itself in a fragment and takes nothing less. It
 * changes nothing else — FragmentActivity *is* a ComponentActivity, so the
 * splash, edge-to-edge and `setContent` are the same calls as before, and the
 * theme stays ours (only AppCompatActivity would demand an AppCompat one).
 *
 * It is also where a link another app opened us for lands. Both arrival
 * paths matter and neither can be skipped: [onCreate] is a cold start, and
 * [onNewIntent] is the far more common warm one — `singleTask` delivers the
 * link to the Activity that is already running rather than building a second
 * one. Handling it is [DeepLinks]' job; taking it is this one's.
 */
class MainActivity : FragmentActivity() {

    /**
     * The chosen interface language, applied before a single resource is read.
     * Below API 33 this is the only thing that applies it — see [AppLocale].
     */
    override fun attachBaseContext(newBase: Context) {
        super.attachBaseContext(AppLocale.wrap(newBase))
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        val splash = installSplashScreen()
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        DeepLinks.offer(intent)
        offerCallAnswer(intent)
        // Permission to cross the lock screen lasts exactly as long as the
        // call that needs it. See the manifest for what declaring it there
        // instead did: one Activity means one grant for the entire app, so
        // a locked phone kept showing whatever screen was open, still
        // working under the user's finger.
        //
        // Driven from here and NOT from Compose. With the screen off this
        // Activity is stopped, and Compose's frame clock pauses with it, so
        // a LaunchedEffect keyed on the call state never ran for a call that
        // arrived then — which is the one call these flags exist for. The
        // phone rang with the screen dark, because the Activity the
        // full-screen intent raised was not yet allowed over the keyguard,
        // and could not become so until it was shown. This scope runs while
        // stopped and dies with the Activity.
        showOverKeyguard(VoiceCalls.state.value.busy)
        lifecycleScope.launch {
            VoiceCalls.state.map { it.busy }.distinctUntilChanged().collect { showOverKeyguard(it) }
        }
        // Short fade from the logo splash into Home.
        splash.setOnExitAnimationListener { provider ->
            provider.view.animate()
                .alpha(0f)
                .setDuration(250L)
                .withEndAction { provider.remove() }
                .start()
        }
        setContent {
            val prefs = remember { AppPreferences(this) }
            val theme by prefs.string(ThemeMode.PREF_KEY).collectAsState(initial = null)
            val lockEnabled by prefs.boolean(AppLock.PREF_KEY, AppLock.DEFAULT)
                .collectAsState(initial = AppLock.DEFAULT)

            // The half of the app lock an overlay cannot do. The recents
            // snapshot is taken as the app leaves — before it is locked, and
            // with the last screen still on it — so blocking it has to be a
            // window flag that was already set. Screenshots go with it, which
            // is the same promise stated the other way round.
            LaunchedEffect(lockEnabled) {
                if (lockEnabled) {
                    window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
                } else {
                    window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
                }
            }

            SkywireTheme(darkTheme = ThemeMode.of(theme).isDark(isSystemInDarkTheme())) {
                BiometricGate {
                    SkywireApp()
                }
            }
        }
    }

    /**
     * Show this Activity over the lock screen, wake the screen for it, and
     * hold the display on — the three things a ringing or connected call
     * needs, granted together and dropped together.
     *
     * Dropped together matters as much as granted: the moment the call ends,
     * the keyguard has to take the screen back. Driven by the call state
     * rather than by a screen being open, so no navigation, crash or missed
     * teardown can leave the app parked in front of a locked phone.
     *
     * It has to be in place BEFORE the full-screen intent raises this
     * Activity: the platform decides whether an Activity may show over the
     * keyguard when it brings it forward, so flags set afterwards keep it
     * behind the lock screen until the user unlocks by hand. The watcher
     * publishes the call to [VoiceCalls] before it posts the notification,
     * and the collector in [onCreate] sets the flags on that change.
     *
     * FLAG_KEEP_SCREEN_ON rides along because a call is the one time the user
     * is plainly using the phone without touching it — the timeout otherwise
     * blanks the screen mid-conversation. It is scoped to the call for the
     * same reason as the rest: holding the display on for the whole app would
     * be someone else's battery.
     *
     * setShowWhenLocked/setTurnScreenOn are API 27; on 26 the same behaviour
     * is the deprecated window flags, which is why both paths are here.
     */
    private fun showOverKeyguard(busy: Boolean) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O_MR1) {
            setShowWhenLocked(busy)
            setTurnScreenOn(busy)
        } else {
            val keyguard = WindowManager.LayoutParams.FLAG_SHOW_WHEN_LOCKED or
                WindowManager.LayoutParams.FLAG_TURN_SCREEN_ON
            if (busy) window.addFlags(keyguard) else window.clearFlags(keyguard)
        }
        if (busy) {
            window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        } else {
            window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        }
    }

    override fun onStart() {
        super.onStart()
        AppVisibility.set(true)
        // Re-locks unless the user has only been gone a moment; the gate is
        // what decides whether that matters (see AppLock).
        AppLock.onForeground()
    }

    override fun onResume() {
        super.onResume()
        AppVisibility.onResumed()
    }

    override fun onStop() {
        AppVisibility.set(false)
        AppLock.onBackground()
        super.onStop()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        // setIntent so anything reading getIntent() later sees the link that
        // actually brought the app forward, not the one it was launched with.
        setIntent(intent)
        // The full-screen intent lands here on a running Activity; make sure
        // the grant is current before it is shown.
        showOverKeyguard(VoiceCalls.state.value.busy)
        DeepLinks.offer(intent)
        offerCallAnswer(intent)
    }

    /**
     * Answer tapped on the call notification. The id travels in the intent
     * because the notification may be older than this process — the app can
     * be started by that tap — so the screen cannot be assumed to know which
     * call it is for.
     */
    private fun offerCallAnswer(intent: Intent) {
        if (intent.action != VoiceCallWatcher.ACTION_ANSWER_CALL) return
        intent.getStringExtra(VoiceCallReceiver.EXTRA_CALL_ID)
            ?.takeIf { it.isNotEmpty() }
            ?.let(VoiceCalls::requestAnswer)
    }
}
