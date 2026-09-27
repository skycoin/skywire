package com.skycoin.skywire.ui.chat

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.content.pm.ActivityInfo
import android.graphics.Color
import android.view.View
import android.view.ViewGroup
import android.webkit.WebChromeClient
import android.widget.FrameLayout
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat

/**
 * Where the chat page's full-screen video goes.
 *
 * A WebView cannot go full screen by itself. When the page asks — the video
 * player's own full-screen button, or skychat's video viewer calling
 * `requestFullscreen()` — WebView hands the app a view that renders the
 * full-screen element, and it is the app's job to put it on screen. Nothing
 * did, so the button did nothing and a video sent in a chat could only be
 * watched at bubble size.
 *
 * The view goes over everything in the window, with the system bars hidden
 * (a swipe brings them back for a moment) and the screen kept on. Rotation is
 * handed to the sensor for as long as it is up: a video is the one thing in
 * the app worth turning the phone for, including on a phone whose rotation is
 * locked. The Activity must not be recreated by that rotation — it would take
 * the WebView, and the video, with it — which is what the manifest's
 * `configChanges` on MainActivity is for.
 */
internal class FullscreenVideo(private val activity: Activity) {

    private var view: View? = null
    private var callback: WebChromeClient.CustomViewCallback? = null
    private var savedOrientation = ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED

    /** Compose state, so a BackHandler can be enabled exactly while it is up. */
    var showing by mutableStateOf(false)
        private set

    /** WebView's onShowCustomView: the page went full screen. */
    fun show(content: View, onHidden: WebChromeClient.CustomViewCallback) {
        if (view != null) {
            // One at a time. A second request while one is up is refused the
            // only way WebView offers: telling it the view went away.
            onHidden.onCustomViewHidden()
            return
        }
        val decor = activity.window.decorView as ViewGroup
        content.setBackgroundColor(Color.BLACK)
        content.keepScreenOn = true
        decor.addView(
            content,
            FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT),
        )
        WindowCompat.getInsetsController(activity.window, decor).apply {
            systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            hide(WindowInsetsCompat.Type.systemBars())
        }
        savedOrientation = activity.requestedOrientation
        activity.requestedOrientation = ActivityInfo.SCREEN_ORIENTATION_SENSOR
        view = content
        callback = onHidden
        showing = true
    }

    /**
     * WebView's onHideCustomView: the page left full screen. Also the
     * teardown when the page itself goes away. Safe to call when nothing is
     * showing.
     */
    fun hide() {
        val content = view ?: return
        view = null
        callback = null
        showing = false
        (content.parent as? ViewGroup)?.removeView(content)
        WindowCompat.getInsetsController(activity.window, activity.window.decorView)
            .show(WindowInsetsCompat.Type.systemBars())
        activity.requestedOrientation = savedOrientation
    }

    /**
     * The user left full screen (back). The page is told, which ends its
     * full screen — and skychat's viewer closes with it.
     */
    fun exit() {
        val onHidden = callback
        hide()
        onHidden?.onCustomViewHidden()
    }
}

/** The Activity behind a Compose context, which may be a wrapper of it. */
internal tailrec fun Context.findActivity(): Activity? = when (this) {
    is Activity -> this
    is ContextWrapper -> baseContext.findActivity()
    else -> null
}
