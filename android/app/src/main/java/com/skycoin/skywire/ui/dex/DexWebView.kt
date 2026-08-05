package com.skycoin.skywire.ui.dex

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.net.Uri
import android.util.Log
import android.view.ViewGroup
import android.app.AlertDialog
import android.webkit.ConsoleMessage
import android.webkit.JsResult
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
     * A stylesheet rather than DOM surgery wherever it can be, because the
     * page is React: a rule in `<head>` survives every re-render and disturbs
     * no state, while an element changed underneath React is restored on the
     * next one. The tables are the exception and [TABLE_CARDS] explains why.
     * Both injections are idempotent and run once per page load.
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
        view.evaluateJavascript(TABLE_CARDS, null)
    }

    /**
     * What CSS alone cannot do for the tables: name each cell, and decide which
     * cells a closed card shows.
     *
     * A `<td>` carries no clue what column it is in, so the labels are copied
     * off the `<thead>` into `data-label` for the stylesheet to print. Which
     * cells stay visible is chosen by column *name* rather than position —
     * "Amount" and "Price" are what identify a row across all three tables,
     * while "ID", the escrow address and the transaction hashes are reference
     * data — and any cell holding a control is always kept, because a Cancel
     * button behind a toggle is a Cancel button nobody presses.
     *
     * Everything here is re-applied by a MutationObserver, and the open cards
     * are remembered in a Set outside the DOM. Both are load-bearing: the page
     * re-renders its tables every eight seconds while it polls, which would
     * otherwise strip the labels and snap every open card shut mid-read.
     */
    private val TABLE_CARDS = """
        (function () {
          if (window.__skywirePhoneTables) return;
          window.__skywirePhoneTables = true;

          var KEEP = ['type', 'amount', 'price', 'status', 'lifecycle'];
          var MAX_VISIBLE = 4;
          var open = {};

          function keyOf(tr) {
            var first = tr.cells[0];
            return (first ? first.textContent.trim() : '') + '#' + tr.rowIndex;
          }

          function visibleColumns(heads) {
            var picked = [];
            for (var k = 0; k < KEEP.length && picked.length < MAX_VISIBLE; k++) {
              for (var i = 0; i < heads.length && picked.length < MAX_VISIBLE; i++) {
                var head = heads[i].toLowerCase();
                if (picked.indexOf(i) < 0 && head.indexOf(KEEP[k]) === 0) picked.push(i);
              }
            }
            return picked;
          }

          /**
           * A lifecycle cell is a chain of badges: the completed steps, the
           * one it is on, and the ones ahead, with arrows between. Everything
           * that is not the current step is marked so the closed card can drop
           * it. Returns whether this cell is such a chain.
           *
           * The current step is the one the page paints `bg-info`; the last
           * badge is the fallback, for a chain that has run to its end.
           */
          function markChain(td) {
            var badges = td.querySelectorAll('.badge');
            if (badges.length < 2) return false;
            td.classList.add('sky-chain');
            var current = td.querySelector('.badge.bg-info') || badges[badges.length - 1];
            var parts = td.querySelectorAll('span, div');
            for (var i = 0; i < parts.length; i++) {
              var part = parts[i];
              if (part === current || part.contains(current)) {
                part.classList.remove('sky-past');
              } else {
                part.classList.add('sky-past');
              }
            }
            // The arrow trailing the current badge is inside its own step.
            var siblings = current.parentNode ? current.parentNode.children : [];
            for (var s = 0; s < siblings.length; s++) {
              if (siblings[s] !== current) siblings[s].classList.add('sky-past');
            }
            return true;
          }

          function enhance() {
            var tables = document.querySelectorAll('table.table');
            for (var t = 0; t < tables.length; t++) {
              var table = tables[t];
              var ths = table.querySelectorAll('thead th');
              if (!ths.length) continue;
              var heads = [];
              for (var h = 0; h < ths.length; h++) heads.push(ths[h].textContent.trim());
              var keep = visibleColumns(heads);

              var rows = table.querySelectorAll('tbody tr');
              for (var r = 0; r < rows.length; r++) {
                var tr = rows[r];
                // A one-cell row is the table's own "nothing here yet" line.
                if (tr.cells.length < 2) continue;
                tr.classList.add('sky-card');
                tr.classList.toggle('sky-open', open[keyOf(tr)] === true);
                for (var c = 0; c < tr.cells.length; c++) {
                  var td = tr.cells[c];
                  var head = heads[c] || '';
                  // The Actions column is named, not guessed at: every other
                  // column can hold a link-styled button too (the id copier),
                  // and those are reference data, not actions.
                  // An Actions cell on a finished row holds a placeholder dash
                  // and nothing else. Its label is suppressed, so left in it
                  // is a bare "—" on a line of its own.
                  var action = head.toLowerCase() === 'actions';
                  var control = td.querySelector('button, input, select');
                  td.classList.toggle('sky-hide', action && !control);
                  var chain = markChain(td);
                  // A lifecycle chain shows one badge when closed, so it is
                  // "Status" then — which is also what it reads as.
                  td.setAttribute('data-label', chain ? 'Status' : head);
                  td.classList.toggle('sky-actions', action && !!control);
                  td.classList.toggle('sky-detail', !action && keep.indexOf(c) < 0);
                  // A hash or an address needs the full width; a number, a
                  // single badge and a button do not.
                  td.classList.toggle(
                    'sky-wide',
                    !action && !chain &&
                      (td.children.length > 1 || td.textContent.trim().length > 24)
                  );
                }
              }
            }
          }

          document.addEventListener('click', function (e) {
            if (!e.target || !e.target.closest) return;
            // A control inside the card is the control, not the card.
            if (e.target.closest('button, a, input, select, label')) return;
            var tr = e.target.closest('tr.sky-card');
            if (!tr) return;
            var key = keyOf(tr);
            open[key] = !open[key];
            tr.classList.toggle('sky-open', open[key]);
          });

          var queued = false;
          new MutationObserver(function () {
            if (queued) return;
            queued = true;
            requestAnimationFrame(function () { queued = false; enhance(); });
          }).observe(document.body, { childList: true, subtree: true });

          enhance();
        })();
    """.trimIndent()

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

          /* Tables become cards. Ten columns of listing cannot be read on a
             phone at any font size, and sideways scrolling puts the Actions
             column — the one holding Cancel — off the edge where nobody finds
             it. Each row becomes a labelled card instead: the four fields that
             identify it plus its buttons, and the rest a tap away. Which cells
             those are is decided in script, by column name. */
          /* The table's box is one element carrying both classes
             (`<div class="panel table-wrap">`) — a rounded panel around what
             are now rounded cards. Let the cards be the only boxes. */
          .panel.table-wrap {
            background: transparent;
            border: 0;
            padding: 0;
          }
          .table-wrap { overflow-x: visible; }
          .table, .table tbody, .table tr, .table td { display: block; }
          .table thead { display: none; }

          .table tbody tr.sky-card {
            border: 1px solid var(--border);
            border-radius: 12px;
            background: #00000038;
            padding: 0.85rem 0.9rem 0.15rem;
            margin-bottom: 0.7rem;
          }
          .table tbody tr.sky-card > td {
            display: flex;
            align-items: baseline;
            justify-content: space-between;
            gap: 0.75rem;
            border: 0;
            padding: 0.25rem 0;
            white-space: normal;
            /* Bootstrap paints cell backgrounds with an inset shadow, which
               inside a card reads as a second panel behind the fields. */
            box-shadow: none;
            background: transparent;
          }
          .table tbody tr.sky-card > td::before {
            content: attr(data-label);
            color: var(--muted);
            font-size: 0.78rem;
            flex: 0 0 auto;
          }

          /* The two numbers that say what a row IS, weighted the way the
             market's own product card weights them: the amount plain, the
             price in the accent. */
          .table tbody tr.sky-card > td[data-label="Amount"] {
            font-size: 1.3rem;
            font-weight: 700;
          }
          .table tbody tr.sky-card > td[data-label="Price"] {
            font-size: 1.3rem;
            font-weight: 700;
            color: var(--sky-blue);
          }

          /* A badge chain, an address or a hash gets the whole width rather
             than the sliver left over beside its label. */
          .table tbody tr.sky-card > td.sky-wide {
            flex-direction: column;
            align-items: flex-start;
            gap: 0.35rem;
            word-break: break-all;
          }

          /* A closed card shows where the listing IS, not the three steps it
             took to get there. The completed and future badges — and the
             arrows between them — come back when the card is opened, which is
             where "why is my deposit still pending" gets answered. */
          .table tbody tr.sky-card:not(.sky-open) > td.sky-chain .sky-past { display: none; }

          /* "Actions" is not a word anyone needs above a button. */
          .table tbody tr.sky-card > td.sky-actions {
            display: block;
            padding: 0.6rem 0 0.15rem;
          }
          .table tbody tr.sky-card > td.sky-actions::before { content: none; }
          .table tbody tr.sky-card > td.sky-actions .btn { width: 100%; }
          .table tbody tr.sky-card > td.sky-hide { display: none; }

          .table tbody tr.sky-card:not(.sky-open) > td.sky-detail { display: none; }
          .table tbody tr.sky-card::after {
            content: 'Details';
            display: block;
            text-align: center;
            color: var(--sky-blue);
            font-size: 0.78rem;
            padding: 0.55rem 0 0.5rem;
            border-top: 1px solid var(--border);
            margin-top: 0.5rem;
          }
          .table tbody tr.sky-card.sky-open::after { content: 'Hide details'; }

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

    /**
     * Chrome client: the page's console next to the app's own logcat, and its
     * JavaScript dialogs.
     *
     * The dialogs are not a nicety. A WebView with no `onJsConfirm` suppresses
     * `window.confirm()` and hands the page `false`, and the page guards
     * cancelling a listing or an order behind exactly that call — so without
     * this, **Cancel silently does nothing**, which on a screen holding
     * escrowed coins is the worst possible way to fail.
     */
    fun chromeClient(): WebChromeClient = object : WebChromeClient() {

        override fun onJsConfirm(
            view: WebView,
            url: String,
            message: String,
            result: JsResult,
        ): Boolean {
            AlertDialog.Builder(view.context)
                .setMessage(message)
                .setPositiveButton(android.R.string.ok) { _, _ -> result.confirm() }
                .setNegativeButton(android.R.string.cancel) { _, _ -> result.cancel() }
                .setOnCancelListener { result.cancel() }
                .show()
            return true
        }

        override fun onJsAlert(
            view: WebView,
            url: String,
            message: String,
            result: JsResult,
        ): Boolean {
            AlertDialog.Builder(view.context)
                .setMessage(message)
                .setPositiveButton(android.R.string.ok) { _, _ -> result.confirm() }
                .setOnCancelListener { result.confirm() }
                .show()
            return true
        }

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
