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
import com.skycoin.skywire.core.AppLocale
import com.skycoin.skywire.core.AppLock
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.core.AppVisibility
import com.skycoin.skywire.core.DeepLinks
import com.skycoin.skywire.core.ThemeMode
import com.skycoin.skywire.core.VoiceCalls
import com.skycoin.skywire.ui.SkywireApp
import com.skycoin.skywire.ui.components.BiometricGate
import com.skycoin.skywire.ui.theme.SkywireTheme

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

            // Permission to cross the lock screen lasts exactly as long as the
            // call that needs it. See the manifest for what declaring it there
            // instead did: one Activity means one grant for the entire app, so
            // a locked phone kept showing whatever screen was open, still
            // working under the user's finger.
            val calls by VoiceCalls.state.collectAsState()
            LaunchedEffect(calls.busy) { showOverKeyguard(calls.busy) }

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
        DeepLinks.offer(intent)
    }
}
