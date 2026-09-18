package com.skycoin.skywire.ui.webview

import android.app.AlertDialog
import android.content.Context
import android.webkit.JsPromptResult
import android.webkit.JsResult
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.widget.EditText
import com.skycoin.skywire.R

/**
 * A [WebChromeClient] that answers the page's JavaScript dialogs.
 *
 * This is not a nicety, and the alternative is not "no dialog" — it is a dead
 * screen. A WebView whose client leaves `onJsConfirm` to the default never
 * answers the [JsResult], and the page's JavaScript thread stays blocked
 * inside `window.confirm()` **for the life of the page**: it goes on showing
 * exactly what it was showing a moment ago, and nothing in it responds to
 * touch ever again. Every button, every scroll, every timer. The rest of the
 * app is untouched, which is what makes it read as "the chat screen's touch
 * stopped working" rather than as a crash.
 *
 * Reproduced on Android 17 against skychat's own "Delete this conversation?".
 * That page guards seven destructive actions behind `confirm()` — delete a
 * conversation, leave or delete a group, remove a contact, wipe the saved
 * notes, stop pairing, stop publishing a profile — and arms a `beforeunload`
 * on top of them while a voice or video message is recording.
 *
 * So every dialog the platform can raise is answered here, including the ones
 * no page uses today. Which one it is does not matter: an unanswered dialog is
 * the same frozen screen either way, and the next page to add a `prompt()`
 * should not have to discover that.
 *
 * Subclass it rather than [WebChromeClient] directly — that is what keeps the
 * guarantee with the WebView instead of with whoever remembers.
 *
 * [isDark] is read per dialog, not captured: these are drawn by the platform
 * and cannot see the app's theme (an Activity theme has no view of a
 * composition local), so the half is named explicitly or a confirm arrives as
 * a dark slab over a light page.
 */
internal abstract class JsDialogChromeClient(
    private val isDark: () -> Boolean,
) : WebChromeClient() {

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

    override fun onJsPrompt(
        view: WebView,
        url: String,
        message: String,
        defaultValue: String?,
        result: JsPromptResult,
    ): Boolean {
        val field = EditText(view.context).apply { setText(defaultValue.orEmpty()) }
        builder(view.context)
            .setMessage(message)
            .setView(field)
            .setPositiveButton(android.R.string.ok) { _, _ -> result.confirm(field.text.toString()) }
            .setNegativeButton(android.R.string.cancel) { _, _ -> result.cancel() }
            .setOnCancelListener { result.cancel() }
            .show()
        return true
    }

    /**
     * The page's own wording is not used: browsers stopped passing custom
     * `beforeunload` text years ago, so [message] arrives empty and the page
     * is left to say what it means through the fact that it asked at all. In
     * this app that fact has one meaning — a recording is running.
     */
    override fun onJsBeforeUnload(
        view: WebView,
        url: String,
        message: String,
        result: JsResult,
    ): Boolean {
        builder(view.context)
            .setTitle(R.string.webview_leave_title)
            .setMessage(R.string.webview_leave_body)
            .setPositiveButton(R.string.webview_leave_confirm) { _, _ -> result.confirm() }
            .setNegativeButton(android.R.string.cancel) { _, _ -> result.cancel() }
            .setOnCancelListener { result.cancel() }
            .show()
        return true
    }
}
