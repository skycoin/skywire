package com.skycoin.skywire.core

import java.io.File

/**
 * The phone's profile for the skychat app: the argv [ConfigManager] pins on
 * every launch, and the password file that argv points at.
 *
 * **Why skychat gets a password here when the desktop leaves it off.**
 * Android has no per-app network namespace: a listener on 127.0.0.1 is
 * reachable by *every* other app on the device that holds INTERNET. skychat's
 * surface is the whole account — history, contacts, sending — so left open it
 * would be readable and writable by any installed app, with no prompt. The
 * gate skychat already ships ([--password-file], `commands/auth.go`) closes
 * that, and the password is a device-local secret ([SecretStore]) the WebView
 * answers the challenge with. Written before the app is ever started, so
 * there is no window in which the surface is open.
 */
object SkychatProfile {

    const val APP = "skychat"

    /** Loopback-only by design; the port stays whatever the config says. */
    const val HOST = LOOPBACK_HOST
    const val DEFAULT_PORT = 8001

    /**
     * Basic-auth username. skychat ignores it (`auth.go` checks the password
     * alone), but a WebView challenge has to send something.
     */
    const val USER = "skywire"

    /** Same name the visor's own password management uses, under `local_path`. */
    private const val PASSWORD_FILE = "skychat-password"

    // The visor writes the double-dash form; the single-dash spelling is
    // accepted too because a hand-edited config may carry it.
    private val ADDR = listOf("--addr", "-addr")
    private val PASSWORD_FILE_FLAG = listOf("--password-file", "-password-file")
    private val PORTLESS = listOf("--portless", "-portless")
    private val PERSIST = listOf("--persist", "-persist")
    private val PERSIST_DB = listOf("--persist-db", "-persist-db")

    /** Where the phone keeps its chat history, under [SkywirePaths.localDir]. */
    private const val HISTORY_FILE = "skychat-history.db"

    fun passwordFile(paths: SkywirePaths): File = File(paths.localDir, PASSWORD_FILE)

    fun historyFile(paths: SkywirePaths): File = File(paths.localDir, HISTORY_FILE)

    /** Where the WebView loads the chat UI from. */
    fun baseUrl(port: Int): String = "http://$HOST:$port/"

    fun listenPort(args: List<String>): Int =
        argValue(args, ADDR)?.substringAfterLast(':')?.toIntOrNull() ?: DEFAULT_PORT

    /**
     * The argv the phone owns, re-applied on every launch. Everything else —
     * `--pair-enable`, the visor-managed `--internal-token` — passes through.
     *
     *  - `--portless` is dropped. It exists so a hypervisor-only deployment
     *    can avoid opening a port at all, but the phone's *only* way into the
     *    UI is that port: the visor's `/skychat/proxy/…` mount serves the same
     *    handler under a path prefix, and every fetch in the page is
     *    root-absolute (`/history`, `/sse`, …), so the UI cannot run there
     *    without rewriting all of them.
     *  - `--addr` host forced to loopback (the port is left alone).
     *  - `--password-file` pinned at [passwordFile] — see the class comment.
     *  - `--persist` ON, at [historyFile]. It is off by default because a
     *    desktop can be left with an ephemeral chat, but on a phone that
     *    default means every conversation is erased the next time the core
     *    restarts — which it does on every crash, every reconnect and every
     *    app update. It is also what the call log reads back (the Calls tab
     *    is call records recovered from history), so without it a missed
     *    call notifies once and then never happened.
     */
    fun phoneArgs(args: List<String>, passwordFile: File, historyFile: File): List<String> {
        val pinned = args.filterNot { token ->
            PORTLESS.any { token == it || token.startsWith("$it=") }
        }.toMutableList()
        if (pinned.none { token -> PERSIST.any { token == it || token.startsWith("$it=") } }) {
            pinned += "--persist"
        }
        pinned.pinValue(PERSIST_DB) { historyFile.absolutePath }
        pinned.pinValue(ADDR) { current -> loopbackAddr(current, DEFAULT_PORT) }
        pinned.pinValue(PASSWORD_FILE_FLAG) { passwordFile.absolutePath }
        return pinned
    }
}
