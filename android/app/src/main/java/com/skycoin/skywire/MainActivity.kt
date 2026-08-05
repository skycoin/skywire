package com.skycoin.skywire

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import com.skycoin.skywire.core.DeepLinks
import com.skycoin.skywire.ui.SkywireApp
import com.skycoin.skywire.ui.components.BiometricGate
import com.skycoin.skywire.ui.theme.SkywireTheme

/**
 * The one Activity: splash → [BiometricGate] → scaffold + NavHost.
 *
 * It is also where a link another app opened us for lands. Both arrival
 * paths matter and neither can be skipped: [onCreate] is a cold start, and
 * [onNewIntent] is the far more common warm one — `singleTask` delivers the
 * link to the Activity that is already running rather than building a second
 * one. Handling it is [DeepLinks]' job; taking it is this one's.
 */
class MainActivity : ComponentActivity() {
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
            SkywireTheme {
                BiometricGate {
                    SkywireApp()
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        // setIntent so anything reading getIntent() later sees the link that
        // actually brought the app forward, not the one it was launched with.
        setIntent(intent)
        DeepLinks.offer(intent)
    }
}
