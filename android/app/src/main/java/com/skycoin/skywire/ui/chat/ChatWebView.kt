package com.skycoin.skywire.ui.chat

import android.app.DownloadManager
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.util.Log
import android.view.ViewGroup
import android.webkit.ConsoleMessage
import android.webkit.PermissionRequest
import android.webkit.URLUtil
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.core.content.ContextCompat
import androidx.core.net.toUri
import com.skycoin.skywire.core.SkychatProfile

/**
 * The Android glue around skychat's embedded web UI. Kept out of the Compose
 * file because most of it is WebView plumbing with nothing composable about
 * it: the password gate, the media-permission bridge, uploads, downloads, and
 * the rule for what may leave the page.
 */
internal object ChatWebView {

    private const val TAG = "SkychatWebView"

    /**
     * Media capture the page can ask for, and the runtime permission each one
     * needs. Anything else it requests (protected media, MIDI) is denied —
     * the chat UI has no use for it.
     */
    fun androidPermission(resource: String): String? = when (resource) {
        PermissionRequest.RESOURCE_AUDIO_CAPTURE -> android.Manifest.permission.RECORD_AUDIO
        PermissionRequest.RESOURCE_VIDEO_CAPTURE -> android.Manifest.permission.CAMERA
        else -> null
    }

    fun hasPermission(context: Context, permission: String): Boolean =
        ContextCompat.checkSelfPermission(context, permission) == PackageManager.PERMISSION_GRANTED

    /**
     * A WebView configured for the chat UI. JavaScript and DOM storage are
     * both load-bearing (the app is one page, and Saved Messages, the tab
     * choice and the notification prefs live in localStorage). File and
     * content access stay off — nothing in the page loads from either, and
     * they are the two settings that turn a rendering bug into a file read.
     */
    fun create(context: Context): WebView = WebView(context).apply {
        // MATCH_PARENT is load-bearing, not cosmetic. A WebView left at the
        // default wrap_content is measured with an AT_MOST height, and
        // Chromium then treats the layout viewport height as *indefinite*:
        // `html, body { height: 100% }` computes to 0px even though
        // innerHeight reports the real value, so every flex column in the
        // page collapses — the conversation list vanishes and the composer
        // rides up under the header instead of sitting at the bottom.
        layoutParams = ViewGroup.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.MATCH_PARENT,
        )
        settings.javaScriptEnabled = true
        settings.domStorageEnabled = true
        settings.allowFileAccess = false
        settings.allowContentAccess = false
        // The page ships its own responsive layout with a width=device-width
        // viewport; the legacy desktop-width fallbacks would fight it.
        settings.useWideViewPort = true
        settings.loadWithOverviewMode = false
        settings.setSupportZoom(false)
        settings.builtInZoomControls = false
        // Inline playback for received voice/video messages — full-screen
        // handoff is the WebChromeClient path we deliberately don't take.
        settings.mediaPlaybackRequiresUserGesture = true
        // chrome://inspect on a debug build; never on a release APK.
        val debuggable =
            (context.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE) != 0
        WebView.setWebContentsDebuggingEnabled(debuggable)
        setBackgroundColor(android.graphics.Color.TRANSPARENT)
    }

    /**
     * Client for the page itself: answers the password gate and decides what
     * a navigation means.
     *
     * The UI never navigates — it is one document driven by `pushState` — so
     * a main-frame navigation is always a `<a download>` on an attachment.
     * WebView ignores the `download` attribute and would happily *render* the
     * image or video in place of the chat, so those are turned into real
     * downloads here. Anything off-origin goes to the browser instead of
     * loading inside the chat's own origin.
     */
    fun client(
        baseUrl: () -> String?,
        password: () -> String?,
        onHistoryChanged: (Boolean) -> Unit,
        onError: (String) -> Unit,
    ): WebViewClient = object : WebViewClient() {

        private var authAttempts = 0

        override fun onReceivedHttpAuthRequest(
            view: WebView,
            handler: android.webkit.HttpAuthHandler,
            host: String,
            realm: String,
        ) {
            val secret = password()
            // Re-challenged with the same credential means the credential is
            // wrong; proceeding again would spin forever.
            if (secret == null || authAttempts++ > 0) {
                handler.cancel()
                onError("SkyChat rejected the stored password")
                return
            }
            handler.proceed(SkychatProfile.USER, secret)
        }

        override fun shouldOverrideUrlLoading(
            view: WebView,
            request: WebResourceRequest,
        ): Boolean {
            if (!request.isForMainFrame) return false
            val base = baseUrl()?.toUri()
            val target = request.url
            if (base != null && target.host == base.host && target.port == base.port) {
                // The document itself (a reload) is not a download.
                if (target.path.orEmpty().trimEnd('/').isEmpty()) return false
                download(view.context, target.toString(), null, null, password())
                return true
            }
            return openExternally(view.context, target)
        }

        override fun doUpdateVisitedHistory(view: WebView, url: String, isReload: Boolean) {
            onHistoryChanged(view.canGoBack())
        }

        override fun onPageFinished(view: WebView, url: String) {
            authAttempts = 0
            onHistoryChanged(view.canGoBack())
        }

        override fun onReceivedError(
            view: WebView,
            request: WebResourceRequest,
            error: android.webkit.WebResourceError,
        ) {
            // Subresource failures are the page's own business (a pruned
            // attachment 404s routinely); only a dead main frame is a screen
            // state.
            if (request.isForMainFrame) onError(error.description.toString())
        }
    }

    /**
     * Chrome client: the parts of the page that need the phone — the
     * microphone and camera for voice/video messages, and the file picker
     * for attachments.
     */
    fun chromeClient(
        onPermissionRequest: (PermissionRequest) -> Unit,
        onFileChooser: (ValueCallback<Array<Uri>>, WebChromeClient.FileChooserParams) -> Boolean,
    ): WebChromeClient = object : WebChromeClient() {

        override fun onPermissionRequest(request: PermissionRequest) {
            onPermissionRequest.invoke(request)
        }

        override fun onShowFileChooser(
            view: WebView,
            callback: ValueCallback<Array<Uri>>,
            params: FileChooserParams,
        ): Boolean = onFileChooser(callback, params)

        override fun onConsoleMessage(message: ConsoleMessage): Boolean {
            // Chat problems on a phone are otherwise invisible: the page's
            // own errors land next to the app's logcat, and its server side
            // is one tap away under the bar's Logs action.
            Log.d(TAG, "${message.sourceId()}:${message.lineNumber()} ${message.message()}")
            return true
        }
    }

    /**
     * Hand an attachment to the system downloader. The Authorization header
     * has to ride along — the gate applies to `/files/` like everything else,
     * and DownloadManager fetches in its own process with no session of ours.
     */
    fun download(
        context: Context,
        url: String,
        contentDisposition: String?,
        mimeType: String?,
        password: String?,
    ) {
        val name = URLUtil.guessFileName(url, contentDisposition, mimeType)
        val request = DownloadManager.Request(url.toUri())
            .setTitle(name)
            .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
        password?.let {
            request.addRequestHeader(
                "Authorization",
                okhttp3.Credentials.basic(SkychatProfile.USER, it),
            )
        }
        mimeType?.takeIf { it.isNotEmpty() }?.let { request.setMimeType(it) }
        // The public Downloads folder needs no permission from API 29; below
        // that it would need WRITE_EXTERNAL_STORAGE, so those devices get the
        // app's own external files dir instead of a permission prompt.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            request.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name)
        } else {
            request.setDestinationInExternalFilesDir(
                context,
                Environment.DIRECTORY_DOWNLOADS,
                name,
            )
        }
        runCatching {
            (context.getSystemService(Context.DOWNLOAD_SERVICE) as DownloadManager)
                .enqueue(request)
        }.onFailure { Log.w(TAG, "download of $name failed to start", it) }
    }

    /** True when the navigation was handled (i.e. must not load in-page). */
    private fun openExternally(context: Context, uri: Uri): Boolean {
        val intent = Intent(Intent.ACTION_VIEW, uri).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        return try {
            context.startActivity(intent)
            true
        } catch (e: ActivityNotFoundException) {
            Log.w(TAG, "nothing handles $uri", e)
            // Nothing opened it and it is not ours to render — refusing beats
            // navigating the chat away to a page it cannot come back from.
            true
        }
    }

    /** Tear-down that actually stops the page: SSE otherwise keeps polling. */
    fun release(view: WebView) {
        view.stopLoading()
        view.webChromeClient = null
        view.loadUrl("about:blank")
        view.clearHistory()
        (view.parent as? ViewGroup)?.removeView(view)
        view.destroy()
    }
}
