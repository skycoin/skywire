package com.skycoin.skywire.ui.components

import android.view.WindowManager
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.platform.LocalContext
import com.skycoin.skywire.core.AppLock
import com.skycoin.skywire.core.AppPreferences

/**
 * Blocks screenshots and the recents thumbnail for as long as this composable
 * is in the tree. Placed on every screen that can put an unrecoverable secret
 * on the display — the wallet's twelve words, and the visor's secret key.
 *
 * The two are the same kind of thing and get the same treatment: each is the
 * whole of an identity or a balance, neither can be reissued, and both are
 * read off a screen by someone who is looking at the screen. A screenshot of
 * either is the secret, and the recents thumbnail is a screenshot the user did
 * not ask for.
 *
 * On dispose the flag is cleared only if the app lock is not already holding
 * it session-wide — MainActivity sets it whenever that preference is on, and
 * clearing it here would quietly undo the lock's own promise.
 *
 * Lives in components rather than beside the wallet because it stopped being
 * a wallet concern the moment Settings grew a field you paste a secret key
 * into.
 */
@Composable
fun SecureWindow() {
    val context = LocalContext.current
    val prefs = remember(context) { AppPreferences(context) }
    // initial=true errs on the safe side: never clear a flag we might need.
    val lockEnabled by prefs.boolean(AppLock.PREF_KEY, AppLock.DEFAULT).collectAsState(initial = true)
    val lockHeld = rememberUpdatedState(lockEnabled)
    DisposableEffect(Unit) {
        val window = context.findFragmentActivity()?.window
        window?.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        onDispose {
            if (!lockHeld.value) window?.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
        }
    }
}
