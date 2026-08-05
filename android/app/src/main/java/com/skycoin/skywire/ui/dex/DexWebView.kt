package com.skycoin.skywire.ui.dex

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.net.Uri
import android.util.Log
import android.view.ViewGroup
import android.webkit.ConsoleMessage
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.core.net.toUri
import com.skycoin.skywire.core.SkydexProfile
import org.json.JSONObject

/**
 * The Android glue around skydex-client's embedded trading UI. Much smaller
 * than the chat's: this page has no gate to answer, no uploads and no media —
 * it is a single-page app talking to its own loopback API. What it does need
 * is the same rule about what may leave the page, and a console pipe, because
 * a trading screen that renders blank has to be diagnosable from logcat.
 */
internal object DexWebView {

    private const val TAG = "SkydexWebView"

    /**
     * A WebView configured for the trading UI. JavaScript is the app itself
     * and DOM storage holds its client-side state (the last market, the open
     * tab). File and content access stay off — nothing in the page loads from
     * either, and they are the two settings that turn a rendering bug into a
     * file read.
     */
    fun create(context: Context): WebView = WebView(context).apply {
        // MATCH_PARENT is load-bearing: a WebView left at wrap_content is
        // measured with an AT_MOST height, and Chromium then treats the layout
        // viewport as indefinite, collapsing every `height: 100%` in the page.
        layoutParams = ViewGroup.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.MATCH_PARENT,
        )
        settings.javaScriptEnabled = true
        settings.domStorageEnabled = true
        settings.allowFileAccess = false
        settings.allowContentAccess = false
        // The page asks for `width=device-width`, and [applyPhoneStyles] gives
        // it the breakpoint it never shipped — so it is laid out at the real
        // width rather than zoomed out to a desktop one. Zoom stays available:
        // this is a screen full of hex addresses and prices.
        settings.useWideViewPort = true
        settings.loadWithOverviewMode = false
        settings.setSupportZoom(true)
        settings.builtInZoomControls = true
        settings.displayZoomControls = false
        // chrome://inspect on a debug build; never on a release APK.
        val debuggable =
            (context.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE) != 0
        WebView.setWebContentsDebuggingEnabled(debuggable)
        setBackgroundColor(android.graphics.Color.TRANSPARENT)
    }

    /**
     * Client for the page itself. The UI is one document driven by React
     * state, so any main-frame navigation off its own origin is a link the
     * page is offering (a block explorer, a help page) and belongs in the
     * browser — not loaded over the trading screen, which cannot navigate
     * back to itself.
     */
    fun client(
        baseUrl: () -> String?,
        password: () -> String?,
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
                onError("SkyDEX rejected the stored password")
                return
            }
            handler.proceed(SkydexProfile.USER, secret)
        }

        override fun shouldOverrideUrlLoading(
            view: WebView,
            request: WebResourceRequest,
        ): Boolean {
            if (!request.isForMainFrame) return false
            val base = baseUrl()?.toUri()
            val target = request.url
            if (base != null && target.host == base.host && target.port == base.port) return false
            return openExternally(view.context, target)
        }

        override fun onPageFinished(view: WebView, url: String) {
            authAttempts = 0
            if (url != "about:blank") applyPhoneStyles(view)
        }

        override fun onReceivedError(
            view: WebView,
            request: WebResourceRequest,
            error: android.webkit.WebResourceError,
        ) {
            // Subresource failures are the page's own business (its market
            // polling 409s the moment a connection drops); only a dead main
            // frame is a screen state.
            if (request.isForMainFrame) onError(error.description.toString())
        }
    }

    /**
     * Give the page the phone layout it does not ship with.
     *
     * The trading UI is built from Bootstrap plus about 10 kB of its own CSS,
     * and that CSS contains **no media query at all** — every padding, grid
     * minimum and flex basis in it was measured on a desktop. It is a built
     * bundle from another repo, so the only lever this side has is a
     * stylesheet layered on top; it is written as one, scoped to phone widths
     * so a tablet in landscape keeps the layout the page intends.
     *
     * A stylesheet rather than DOM surgery, because the page is React: an
     * element changed underneath it is restored on the next render, while a
     * rule in `<head>` survives every re-render and disturbs no state. It is
     * injected once per page load and is idempotent.
     */
    private fun applyPhoneStyles(view: WebView) {
        view.evaluateJavascript(
            """
            (function () {
              var id = 'skywire-phone-styles';
              if (document.getElementById(id)) return;
              var style = document.createElement('style');
              style.id = id;
              style.textContent = ${JSONObject.quote(PHONE_CSS)};
              document.head.appendChild(style);
            })();
            """.trimIndent(),
            null,
        )
    }

    /**
     * The rules, in order: drop the duplicated header; give the page back the
     * width its desktop padding spends; make the tab strip show all five tabs;
     * collapse the grids and the trade builder, whose column minimums assume a
     * screen this size does not have; let tables scroll sideways instead of
     * shredding every cell into a column of single words; and make the small
     * buttons thumb-sized.
     */
    private val PHONE_CSS = """
        /* The native row above this page already says all of this. */
        .app-container > header.header { display: none !important; }

        @media (max-width: 600px) {
          .content { padding: 0.9rem 0.85rem 1.5rem; }
          .panel, .card { padding: 0.9rem; margin-bottom: 0.9rem; }
          .content h2 { font-size: 1.2rem; }

          /* Five tabs do not fit on one line, and a strip that scrolls hides
             the one holding the wallet addresses. Wrap instead. */
          .tabbar { padding: 0 0.35rem; overflow-x: visible; }
          .tabbar-inner { flex-wrap: wrap; }
          .tab-link { padding: 0.65rem 0.6rem; font-size: 0.85rem; }

          /* auto-fit at a 240px minimum is one column here anyway; saying so
             stops the last row stretching a lone card across the screen. */
          .field-grid, .card-grid { grid-template-columns: 1fr; }

          /* 84px label + 140px amount + 140px unit cannot sit on one line. */
          .trade-builder .trade-leg { flex-direction: column; align-items: stretch; gap: 0.35rem; }
          .trade-leg .leg-label { flex: none; }
          .trade-leg .leg-amount { flex: none; width: 100%; font-size: 1.15rem; }
          .trade-leg .leg-coin { flex: none; width: 100%; }

          .table-wrap { -webkit-overflow-scrolling: touch; }
          .table th, .table td { white-space: nowrap; }

          /* A banner's action reads as an action, not a word in a corner. */
          .banner { align-items: flex-start; }
          .banner .btn { width: 100%; }

          /* 0.3rem of padding is a 28px target; a fingertip is 40. */
          .btn, .btn-sm, .link-btn { min-height: 40px; }
          .btn.btn-sm.qr-btn { min-width: 40px; }

          /* Long hex breaks rather than pushing the page sideways. */
          .addr-box.addr-sm { max-width: 100%; }
          .qr-modal { max-height: 85vh; }
        }
    """.trimIndent()

    /** Chrome client: the page's console, next to the app's own logcat. */
    fun chromeClient(): WebChromeClient = object : WebChromeClient() {
        override fun onConsoleMessage(message: ConsoleMessage): Boolean {
            Log.d(TAG, "${message.sourceId()}:${message.lineNumber()} ${message.message()}")
            return true
        }
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
            // navigating the trading screen away to a page it cannot come
            // back from.
            true
        }
    }

    /** Tear-down that actually stops the page: it polls the market on a timer. */
    fun release(view: WebView) {
        view.stopLoading()
        view.webChromeClient = null
        view.loadUrl("about:blank")
        view.clearHistory()
        (view.parent as? ViewGroup)?.removeView(view)
        view.destroy()
    }
}
