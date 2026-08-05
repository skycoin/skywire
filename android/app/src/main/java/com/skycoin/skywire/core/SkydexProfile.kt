package com.skycoin.skywire.core

import java.io.File

/**
 * The phone's profile for the skydex-client app: the argv [ConfigManager] pins
 * on every launch, and the password file that argv points at.
 *
 * **Why the trading UI gets a password here when the desktop leaves it off.**
 * The same reason skychat does ([SkychatProfile]) — Android has no per-app
 * network namespace, so a loopback listener is reachable by every installed app
 * holding INTERNET — but with more behind the port: the live market session,
 * the wallet addresses registered with it, and placing or cancelling orders.
 * Left open, "an app on this phone can trade your coins" is the accurate
 * description of the default.
 *
 * The gate is skywire's own wrapper (`cmd/apps/skydex-client/commands/auth.go`),
 * added for this: the engine that serves the UI comes from the skycoin repo and
 * has no authentication, so the wrapper takes over `--addr`, puts basic auth on
 * it, and moves the engine to a loopback port drawn fresh at every start. The
 * password is a device-local secret ([SecretStore]) that the WebView answers the
 * challenge with and [com.skycoin.skywire.api.SkydexApi] sends on every call.
 * Written before the app is ever started, so there is no window in which the
 * surface is open.
 */
object SkydexProfile {

    const val APP = "skydex-client"

    /** Loopback-only by design; the port stays whatever the config says. */
    const val HOST = LOOPBACK_HOST
    const val DEFAULT_PORT = 8051

    /**
     * Basic-auth username. The gate ignores it (`auth.go` checks the password
     * alone), but a WebView challenge has to send something.
     */
    const val USER = "skywire"

    /** Same shape as skychat's, under `local_path`. */
    private const val PASSWORD_FILE = "skydex-password"

    // The visor writes the double-dash form; the single-dash spelling is
    // accepted too because a hand-edited config may carry it.
    private val ADDR = listOf("--addr", "-addr")
    private val PASSWORD_FILE_FLAG = listOf("--password-file", "-password-file")

    fun passwordFile(paths: SkywirePaths): File = File(paths.localDir, PASSWORD_FILE)

    /** Where the WebView loads the trading UI from. */
    fun baseUrl(port: Int): String = "http://$HOST:$port/"

    fun listenPort(args: List<String>): Int =
        argValue(args, ADDR)?.substringAfterLast(':')?.toIntOrNull() ?: DEFAULT_PORT

    /**
     * The argv the phone owns, re-applied on every launch. Everything else —
     * `--market-port`, and above all the `--market-pk` the SkyDEX screen
     * writes — passes through untouched, so this never undoes a user's choice.
     *
     *  - `--addr` host forced to loopback (the port is left alone). The
     *    generated `:8051` listens on every interface.
     *  - `--password-file` pinned at [passwordFile] — see the class comment.
     */
    fun phoneArgs(args: List<String>, passwordFile: File): List<String> {
        val pinned = args.toMutableList()
        pinned.pinValue(ADDR) { current -> loopbackAddr(current, DEFAULT_PORT) }
        pinned.pinValue(PASSWORD_FILE_FLAG) { passwordFile.absolutePath }
        return pinned
    }
}
