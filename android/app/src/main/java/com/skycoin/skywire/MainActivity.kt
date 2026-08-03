package com.skycoin.skywire

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import com.skycoin.skywire.ui.SkywireApp
import com.skycoin.skywire.ui.components.BiometricGate
import com.skycoin.skywire.ui.theme.SkywireTheme

/**
 * The one Activity: splash → [BiometricGate] → scaffold + NavHost.
 */
class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        val splash = installSplashScreen()
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
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
}
