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
import com.skycoin.skywire.R
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
        isDark: () -> Boolean,
        onError: (String) -> Unit,
        onHistoryChanged: (Boolean) -> Unit = {},
    ): WebViewClient = object : WebViewClient() {

        private var authAttempts = 0

        // The trading UI is a React app that routes with pushState, so its
        // own screens are history entries — which is what lets back walk them
        // before it gives up and leaves SkyDEX.
        override fun doUpdateVisitedHistory(view: WebView, url: String, isReload: Boolean) {
            onHistoryChanged(view.canGoBack())
        }

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
                onError(view.context.getString(R.string.dex_error_password))
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

        // The earliest moment a script provably runs in the NEW document —
        // onPageStarted can still evaluate against the old one. Committing
        // the theme here keeps a light-mode load from flashing the page's
        // built-in navy while its stylesheet arrives.
        override fun onPageCommitVisible(view: WebView, url: String) {
            if (url != "about:blank") applyTheme(view, isDark())
        }

        override fun onPageFinished(view: WebView, url: String) {
            authAttempts = 0
            onHistoryChanged(view.canGoBack())
            if (url != "about:blank") {
                applyTheme(view, isDark())
                applyPhoneStyles(view)
            }
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
     * Put the page in the app's theme. The trading UI ships one theme — dark
     * navy, declared as six custom properties on `:root` — so following the
     * app is a matter of re-pointing those tokens under a class this side
     * controls. Called from [WebViewClient.onPageCommitVisible] so a light
     * load never paints navy first, again at finish (belt and braces on a
     * page we do not own), and live from the screen when the app's theme
     * changes with the page open — same mechanism as the chat's.
     *
     * Idempotent like the other injections: the stylesheet lands once per
     * document, only the class toggles.
     */
    fun applyTheme(view: WebView, dark: Boolean) {
        view.evaluateJavascript(
            """
            (function () {
              var root = document.documentElement;
              root.classList.toggle('sky-light', ${!dark});
              root.classList.toggle('sky-dark', $dark);
              var id = 'skywire-theme-styles';
              if (document.getElementById(id)) return;
              var style = document.createElement('style');
              style.id = id;
              style.textContent = ${JSONObject.quote(THEME_CSS)};
              (document.head || root).appendChild(style);
            })();
            """.trimIndent(),
            null,
        )
    }

    /**
     * The two themes, written against the page's own tokens.
     *
     * Dark is the page's design on the app's ground: one token moves so the
     * document behind the native header row is the same navy as the rest of
     * the app, and every border, panel and accent stays as shipped. Light is
     * the app's light palette from `ui/theme/Theme.kt` — white ground,
     * near-black ink, the brand blue — plus overrides for each hard-coded
     * translucent dark in the page's CSS, which are all white-on-navy
     * arithmetic that turns to mud on white. `color-scheme` flips with it so
     * the engine draws scrollbars and select popups to match.
     */
    private val THEME_CSS = """
        html.sky-dark {
          --sky-navy: #0A101C;
        }

        html.sky-light {
          color-scheme: light;
          --sky-navy: #FFFFFF;
          --sky-white: #0B1526;
          --sky-blue: #0F7BF4;
          --panel: #FAFCFF;
          --border: rgba(15, 123, 244, .28);
          --muted: #44536B;
          --bs-emphasis-color: #0B1526;
          --bs-emphasis-color-rgb: 11, 21, 38;
        }

        /* Inputs: the page fills them with translucent black over navy. */
        html.sky-light .form-control,
        html.sky-light .form-select { background-color: #FFFFFF; }
        html.sky-light .form-control:focus,
        html.sky-light .form-select:focus { background-color: #FFFFFF; }
        html.sky-light .form-control::placeholder { color: rgba(11, 21, 38, .4); }
        /* — except the trade builder's amount, which the page deliberately
           leaves transparent so it reads as a figure sitting on the leg panel
           rather than a field. Re-stating it keeps the rule above from
           printing a white box inside a tinted one. */
        html.sky-light .trade-leg .leg-amount,
        html.sky-light .trade-leg .leg-amount:focus { background-color: transparent; }

        /* The one place re-pointing --sky-white is wrong: it is the *ink* token,
           and the primary button uses it on a fill of brand blue. Ink on blue
           is what the dark page means by it; on light it has to stay white. */
        html.sky-light .btn-primary,
        html.sky-light .btn-connect,
        html.sky-light .btn-primary:hover,
        html.sky-light .btn-connect:hover:not(:disabled) { color: #FFFFFF; }

        /* The other translucent darks, each re-based on ink or the brand. */
        html.sky-light .trade-builder .trade-leg { background: #F6F9FD; }
        html.sky-light .addr-box { background: #F0F5FC; }
        html.sky-light .recent-connect { background: #F6F9FD; }
        html.sky-light .card.product-card:hover { background-color: rgba(15, 123, 244, .06); }
        html.sky-light .progress { background-color: rgba(11, 21, 38, .1); }
        html.sky-light .deposit-close:hover { background: rgba(11, 21, 38, .08); }

        /* A red readable on white, not the page's salmon-on-navy. */
        html.sky-light .req,
        html.sky-light .recent-del:hover:not(:disabled) { color: #C62828; }

        /* Shadows tuned for a dark ground read as smears on a light one. */
        html.sky-light .qr-modal { box-shadow: 0 12px 40px rgba(11, 21, 38, .2); }
        html.sky-light .connect-card { box-shadow: 0 8px 30px rgba(15, 123, 244, .12); }

        /* The card each table row becomes on a phone is this side's own
           translucent black (PHONE_CSS) — same scope, light fill. */
        @media (max-width: 600px) {
          html.sky-light .table tbody tr.sky-card { background: #FAFCFF; }
        }
    """.trimIndent()

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

          /* Two ways to read every section: the labelled cards, or a compact
             list whose rows hold only the primary pair until tapped open.
             One choice for the whole page, kept across visits — a reader who
             prefers the list prefers it on every tab. */
          var mode = 'card';
          try { mode = localStorage.getItem('skywire-dex-view') || 'card'; } catch (e) {}

          function setMode(m) {
            mode = m === 'list' ? 'list' : 'card';
            try { localStorage.setItem('skywire-dex-view', mode); } catch (e) {}
            document.documentElement.classList.toggle('sky-list', mode === 'list');
            syncViewbar();
          }

          function syncViewbar() {
            var btns = document.querySelectorAll('.sky-viewbar button');
            for (var i = 0; i < btns.length; i++) {
              btns[i].classList.toggle('on', btns[i].dataset.view === mode);
            }
          }

          /* The switch rides in the heading of whatever it switches, which
             React rebuilds on every tab change — so it is (re)inserted from
             enhance(), the same way the data-labels are re-applied.
             It only appears where it does something: Settings is a form, and a
             Cards/List choice above a form is a control with nothing to act on.
             The test is the DOM's, not a list of tab names — whatever the page
             adds later, the switch follows the rows.

             Which heading: the market names its grid ("Available products",
             an .section-title) and its .page-head already carries New Sell
             Order; every other tab's rows ARE the tab, so the page title is
             the heading. Both are one line with the switch on the right —
             a bar of its own above the rows is a row of chrome that says
             nothing. */
          function ensureViewbar(applies) {
            var content = document.querySelector('.content');
            if (!content) return;
            var existing = content.querySelector('.sky-viewbar');
            if (!applies) {
              if (existing) existing.remove();
              return;
            }
            var host = content.querySelector('.section-title') ||
              content.querySelector('.page-head');
            if (!host) return;
            if (existing && existing.parentNode === host) { syncViewbar(); return; }
            if (existing) existing.remove();
            var bar = document.createElement('div');
            bar.className = 'sky-viewbar';
            bar.innerHTML =
              '<button type="button" data-view="card">Cards</button>' +
              '<button type="button" data-view="list">List</button>';
            bar.addEventListener('click', function (e) {
              var b = e.target.closest('button[data-view]');
              if (b) setMode(b.dataset.view);
            });
            if (host.classList.contains('page-head')) {
              // Straight after the title rather than at the end, so when the
              // row is too narrow for three items it is the page's own button
              // (Clear history) that wraps and never the switch.
              var title = host.querySelector('h2');
              host.insertBefore(bar, title ? title.nextSibling : host.firstChild);
            } else {
              host.classList.add('sky-titlerow');
              host.appendChild(bar);
            }
            syncViewbar();
          }

          /* "Clear history" belongs with the other things you set once, not
             beside the list it wipes — a destructive control in a heading row
             is one mis-tap from the tab you just opened. The page's own button
             is hidden in CSS (no flash) and re-offered here.

             It is re-implemented rather than moved because the two tabs are
             separate React screens: History's button does not exist in the DOM
             while Settings is on screen. What it does is entirely local —
             `localStorage.removeItem('exchange:history')` — so the same key,
             behind the same confirm, is the same action. Like the page's own,
             a later poll can re-save trades the market still reports as
             finished; that is the page's behaviour, not a difference. */
          var HISTORY_KEY = 'exchange:history';

          function historyCount() {
            try {
              var raw = JSON.parse(localStorage.getItem(HISTORY_KEY));
              return Array.isArray(raw) ? raw.length : 0;
            } catch (e) { return 0; }
          }

          function ensureHistoryClear() {
            var content = document.querySelector('.content');
            if (!content) return;
            var head = content.querySelector('.page-head h2');
            var onSettings = !!head && head.textContent.trim() === 'Settings';
            var panel = content.querySelector('.sky-history-panel');
            // Nothing saved is nothing to clear — the same condition the
            // History tab put on the button.
            if (!onSettings || historyCount() === 0) {
              if (panel) panel.remove();
              return;
            }
            if (panel) return;
            panel = document.createElement('div');
            panel.className = 'panel sky-history-panel';
            panel.innerHTML =
              '<h4 class="panel-title">Trade history</h4>' +
              '<p class="text-muted">Completed, cancelled and expired trades are ' +
              'kept on this device so History can show them after the market ' +
              'forgets. Clearing removes that local copy.</p>' +
              '<button type="button" class="btn btn-connect mt-3 sky-clear-history">' +
              'Clear history</button>';
            panel.querySelector('.sky-clear-history').addEventListener('click', function (e) {
              var btn = e.currentTarget;
              if (!window.confirm(
                'Clear all locally saved trade history on this device?'
              )) return;
              try { localStorage.removeItem(HISTORY_KEY); } catch (err) {}
              btn.textContent = 'History cleared';
              btn.disabled = true;
            });
            content.appendChild(panel);
          }

          function keyOf(tr) {
            var first = tr.cells[0];
            return (first ? first.textContent.trim() : '') + '#' + tr.rowIndex;
          }

          /* A product card has no row index; its own text identifies it well
             enough to keep it open across the page's 8-second re-renders. */
          function productKey(card) {
            return 'p#' + card.textContent.trim().slice(0, 80);
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
            // Rows to read either way — a table with a header, or the market's
            // product grid. Neither means this section is a form.
            ensureViewbar(!!(
              document.querySelector('table.table thead th') ||
              document.querySelector('.card.product-card')
            ));
            ensureHistoryClear();
            var tables = document.querySelectorAll('table.table');
            for (var t = 0; t < tables.length; t++) {
              var table = tables[t];
              var ths = table.querySelectorAll('thead th');
              if (!ths.length) continue;
              var heads = [];
              for (var h = 0; h < ths.length; h++) heads.push(ths[h].textContent.trim());
              var keep = visibleColumns(heads);
              // The list's closed row shows only the first two kept columns —
              // the pair that identifies the row (Amount and Price wherever
              // the table has them).
              var primary = keep.slice(0, 2);

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
                  td.classList.toggle('sky-primary', primary.indexOf(c) >= 0);
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
            // The market's product grid gets the same two readings: its cards
            // are already cards, and the list rows open on tap for the seller
            // and the Buy button.
            var cards = document.querySelectorAll('.card.product-card');
            for (var p = 0; p < cards.length; p++) {
              cards[p].classList.toggle('sky-open', open[productKey(cards[p])] === true);
            }
          }

          document.addEventListener('click', function (e) {
            if (!e.target || !e.target.closest) return;
            // A control inside the card is the control, not the card.
            if (e.target.closest('button, a, input, select, label')) return;
            var tr = e.target.closest('tr.sky-card');
            if (tr) {
              var key = keyOf(tr);
              open[key] = !open[key];
              tr.classList.toggle('sky-open', open[key]);
              return;
            }
            // Product rows expand only in list mode — the card shows
            // everything already.
            var pc = e.target.closest('.card.product-card');
            if (pc && document.documentElement.classList.contains('sky-list')) {
              var pk = productKey(pc);
              open[pk] = !open[pk];
              pc.classList.toggle('sky-open', open[pk]);
            }
          });

          var queued = false;
          new MutationObserver(function () {
            if (queued) return;
            queued = true;
            requestAnimationFrame(function () { queued = false; enhance(); });
          }).observe(document.body, { childList: true, subtree: true });

          document.documentElement.classList.toggle('sky-list', mode === 'list');
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

        /* The Cards/List switch only means something where the phone layout
           below applies; on wider screens it stays out of the way — and so
           does the Settings panel that takes over Clear history, which is the
           same trade: on a desktop the page's own heading row has the room. */
        .sky-viewbar { display: none; }
        .sky-history-panel { display: none; }

        @media (max-width: 600px) {
          /* Cards or a compact list — the reader's choice, page-wide. It sits
             in the heading row it belongs to, pushed to the right of the
             title; .page-head is already such a row, .section-title is made
             into one. */
          .sky-viewbar { display: flex; margin-left: auto; }
          .section-title.sky-titlerow {
            display: flex;
            align-items: center;
            justify-content: space-between;
            gap: 1rem;
          }

          /* Clear history moves out of History's heading row and into
             Settings, where the rest of the once-and-done lives. Hiding the
             original in CSS rather than script keeps it from flashing in on
             every one of the page's 8-second re-renders. Only History puts a
             link-button in its page-head. */
          .page-head .link-btn { display: none; }
          .sky-history-panel { display: block; }
          .sky-history-panel .text-muted { font-size: 0.85rem; }
          .sky-viewbar button {
            border: 1px solid var(--border);
            background: transparent;
            color: var(--muted);
            font-size: 0.78rem;
            padding: 0.3rem 0.85rem;
            min-height: 34px;
          }
          .sky-viewbar button:first-child { border-radius: 8px 0 0 8px; }
          .sky-viewbar button:last-child { border-radius: 0 8px 8px 0; margin-left: -1px; }
          .sky-viewbar button.on {
            background: var(--sky-blue);
            border-color: var(--sky-blue);
            color: #fff;
          }
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

          /* ---- List mode. Same DOM the card rules build on, read tighter:
             a row is its primary pair on one line, everything else arrives
             when the row is opened. */
          html.sky-list .table tbody tr.sky-card {
            border: 0;
            border-bottom: 1px solid var(--border);
            border-radius: 0;
            background: transparent;
            padding: 0.55rem 1.4rem 0.5rem 0.1rem;
            margin-bottom: 0;
            position: relative;
          }
          html.sky-list .table tbody tr.sky-card:not(.sky-open) > td:not(.sky-primary) {
            display: none;
          }
          html.sky-list .table tbody tr.sky-card:not(.sky-open) > td.sky-primary {
            display: inline-flex;
            width: auto;
            font-size: 1rem;
            gap: 0.4rem;
            margin-right: 1.1rem;
          }
          /* The chevron replaces the Details footer: the row itself is the
             tap target either way, and a footer per row defeats a list. */
          html.sky-list .table tbody tr.sky-card::after {
            content: '\25BE';
            position: absolute;
            right: 0.35rem;
            top: 0.55rem;
            border: 0;
            margin: 0;
            padding: 0;
            color: var(--muted);
          }
          html.sky-list .table tbody tr.sky-card.sky-open::after { content: '\25B4'; }

          /* The market grid, as the same list: amount and price on the line,
             seller and Buy behind the tap. */
          html.sky-list .card-grid { display: block; }
          html.sky-list .card.product-card {
            flex-direction: row;
            flex-wrap: wrap;
            align-items: baseline;
            gap: 0.6rem;
            border: 0;
            border-bottom: 1px solid var(--border);
            border-radius: 0;
            background: transparent;
            padding: 0.55rem 0.1rem 0.5rem;
            margin-bottom: 0;
          }
          html.sky-list .card.product-card .product-price { color: var(--sky-blue); font-weight: 700; }
          html.sky-list .card.product-card:not(.sky-open) .product-seller { display: none; }
          html.sky-list .card.product-card:not(.sky-open) .btn { display: none; }
          html.sky-list .card.product-card:not(.sky-open)::after {
            content: '\25BE';
            margin-left: auto;
            color: var(--muted);
          }
          html.sky-list .card.product-card.sky-open { padding-bottom: 0.75rem; }
          html.sky-list .card.product-card.sky-open .product-seller { flex-basis: 100%; }
          html.sky-list .card.product-card.sky-open .btn { flex-basis: 100%; margin-top: 0.2rem; }
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
    fun chromeClient(isDark: () -> Boolean = { true }): WebChromeClient = object : WebChromeClient() {

        // These dialogs are drawn by the platform, not by Compose, so they do
        // not inherit the app's light/dark the way every other surface does —
        // an Activity theme cannot see a choice that lives in a composition
        // local. Naming the half explicitly is what keeps a confirm from
        // arriving as a dark slab over the light trading page.
        private fun builder(context: Context) = AlertDialog.Builder(
            context,
            if (isDark()) android.R.style.Theme_DeviceDefault_Dialog_Alert
            else android.R.style.Theme_DeviceDefault_Light_Dialog_Alert,
        )

        override fun onJsConfirm(
            view: WebView,
            url: String,
            message: String,
            result: JsResult,
        ): Boolean {
            builder(view.context)
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
            builder(view.context)
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
