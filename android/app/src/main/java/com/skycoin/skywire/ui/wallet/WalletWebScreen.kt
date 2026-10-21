package com.skycoin.skywire.ui.wallet

import android.Manifest
import android.content.ActivityNotFoundException
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.view.ViewGroup
import android.webkit.PermissionRequest
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import com.skycoin.skywire.R
import com.skycoin.skywire.core.WalletWeb
import com.skycoin.skywire.ui.chat.QrBridge
import com.skycoin.skywire.ui.components.HelpTopic
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.ui.navigation.HoldTabSwipe
import com.skycoin.skywire.ui.theme.LocalDarkTheme
import com.skycoin.skywire.ui.webview.JsDialogChromeClient

/**
 * The wallet tab: skycoin-web, served by the core on loopback, the same
 * wallet a desktop visor serves. [onPreviousWallet] opens the old in-app
 * wallet, offered while it still holds wallets.
 */
@Composable
fun WalletWebScreen(onBack: () -> Unit, onPreviousWallet: (() -> Unit)? = null) {
    val context = LocalContext.current
    val dark = rememberUpdatedState(LocalDarkTheme.current)
    var webView by remember { mutableStateOf<WebView?>(null) }
    var canGoBack by remember { mutableStateOf(false) }
    var failed by remember { mutableStateOf(false) }

    // The page's QR scanner asks for the camera through getUserMedia.
    var pendingCamera by remember { mutableStateOf<PermissionRequest?>(null) }
    val cameraPermission = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted ->
        pendingCamera?.let { if (granted) it.grant(it.resources) else it.deny() }
        pendingCamera = null
    }

    BackHandler(enabled = canGoBack) { webView?.goBack() }
    // Swipes change tabs only from the wallet's first page.
    HoldTabSwipe(canGoBack)

    Scaffold(
        topBar = {
            SkyTopBar(
                title = stringResource(R.string.app_wallet),
                onBack = onBack,
                help = HelpTopic(R.string.help_wallet_title, R.string.help_wallet_body),
                actions = {
                    if (onPreviousWallet != null) {
                        TextButton(onClick = onPreviousWallet) {
                            Text(stringResource(R.string.wallet_previous))
                        }
                    }
                },
            )
        },
        contentWindowInsets = WindowInsets(0),
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .imePadding(),
        ) {
            AndroidView(
                modifier = Modifier.fillMaxSize(),
                factory = { ctx ->
                    WebView(ctx).apply {
                        // wrap_content leaves the page's 100% heights at 0px.
                        layoutParams = ViewGroup.LayoutParams(
                            ViewGroup.LayoutParams.MATCH_PARENT,
                            ViewGroup.LayoutParams.MATCH_PARENT,
                        )
                        settings.javaScriptEnabled = true
                        settings.domStorageEnabled = true
                        settings.allowFileAccess = false
                        settings.allowContentAccess = false
                        settings.setSupportZoom(false)
                        addJavascriptInterface(QrBridge(), "SkywireQr")
                        WebView.setWebContentsDebuggingEnabled(
                            (ctx.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE) != 0,
                        )
                        webViewClient = object : WebViewClient() {
                            override fun shouldOverrideUrlLoading(
                                view: WebView,
                                request: WebResourceRequest,
                            ): Boolean {
                                if (request.url.toString().startsWith(WalletWeb.URL)) return false
                                // Explorer and help links leave the wallet's origin.
                                try {
                                    view.context.startActivity(Intent(Intent.ACTION_VIEW, request.url))
                                } catch (_: ActivityNotFoundException) {
                                }
                                return true
                            }

                            override fun onPageStarted(view: WebView, url: String?, favicon: Bitmap?) {
                                failed = false
                            }

                            override fun doUpdateVisitedHistory(view: WebView, url: String?, isReload: Boolean) {
                                canGoBack = view.canGoBack()
                            }

                            override fun onReceivedError(
                                view: WebView,
                                request: WebResourceRequest,
                                error: WebResourceError,
                            ) {
                                // The core is not up yet, or skycoin-web is stopped.
                                if (request.isForMainFrame) failed = true
                            }
                        }
                        webChromeClient = object : JsDialogChromeClient({ dark.value }) {
                            override fun onPermissionRequest(request: PermissionRequest) {
                                if (request.resources.toList() != listOf(PermissionRequest.RESOURCE_VIDEO_CAPTURE)) {
                                    request.deny()
                                    return
                                }
                                val camera = Manifest.permission.CAMERA
                                if (ContextCompat.checkSelfPermission(ctx, camera) == PackageManager.PERMISSION_GRANTED) {
                                    request.grant(request.resources)
                                } else {
                                    pendingCamera = request
                                    cameraPermission.launch(camera)
                                }
                            }
                        }
                        loadUrl(WalletWeb.URL)
                        webView = this
                    }
                },
                onRelease = { view ->
                    view.stopLoading()
                    view.destroy()
                    webView = null
                },
            )
            if (failed) Surface(Modifier.fillMaxSize()) {
                Column(
                    modifier = Modifier.padding(24.dp),
                    verticalArrangement = Arrangement.spacedBy(16.dp, Alignment.CenterVertically),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    Text(stringResource(R.string.wallet_web_unreachable))
                    FilledTonalButton(onClick = { webView?.loadUrl(WalletWeb.URL) }) {
                        Text(stringResource(R.string.wallet_web_retry))
                    }
                }
            }
        }
    }
}
