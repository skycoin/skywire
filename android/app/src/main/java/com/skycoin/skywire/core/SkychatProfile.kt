package com.skycoin.skywire.core

import java.io.File
import java.security.SecureRandom

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
    const val HOST = "127.0.0.1"
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

    fun passwordFile(paths: SkywirePaths): File = File(paths.localDir, PASSWORD_FILE)

    /** Where the WebView loads the chat UI from. */
    fun baseUrl(port: Int): String = "http://$HOST:$port/"

    fun listenPort(args: List<String>): Int =
        value(args, ADDR)?.substringAfterLast(':')?.toIntOrNull() ?: DEFAULT_PORT

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
     */
    fun phoneArgs(args: List<String>, passwordFile: File): List<String> {
        val pinned = args.filterNot { token ->
            PORTLESS.any { token == it || token.startsWith("$it=") }
        }.toMutableList()
        pinned.pinValue(ADDR) { current ->
            val port = current?.substringAfterLast(':')?.toIntOrNull()
            when {
                port != null -> "$HOST:$port"
                current == null -> "$HOST:$DEFAULT_PORT"
                // A malformed value only gets worse from rewriting — leave it
                // and let the visor report it.
                else -> null
            }
        }
        pinned.pinValue(PASSWORD_FILE_FLAG) { passwordFile.absolutePath }
        return pinned
    }

    /**
     * The on-disk form of [password], in the scheme `auth.go` verifies:
     * `"<hex salt>:<hex sha256(password || salt)>"`, 16-byte salt — the same
     * hashing the hypervisor's user store uses.
     */
    fun passwordRecord(password: String): String {
        val salt = ByteArray(SALT_LEN).also { SecureRandom().nextBytes(it) }
        return salt.toHex() + ":" + hash(password, salt).toHex()
    }

    /**
     * Whether an existing record still stands for [password] — re-hashed with
     * the record's own salt. The check is what lets the file be left alone on
     * a normal launch and rewritten when the stored secret has rotated (a
     * wiped keystore), instead of the app 401-ing against its own gate.
     */
    fun matches(record: String, password: String): Boolean {
        val (saltHex, hashHex) = record.trim().split(":", limit = 2)
            .takeIf { it.size == 2 } ?: return false
        val salt = runCatching { saltHex.fromHex() }.getOrNull() ?: return false
        return hash(password, salt).toHex() == hashHex.lowercase()
    }

    private fun hash(password: String, salt: ByteArray): ByteArray =
        java.security.MessageDigest.getInstance("SHA-256")
            .digest(password.toByteArray(Charsets.UTF_8) + salt)

    /**
     * Set `flag`'s value to what [next] returns for the current one (null when
     * the flag is absent), appending the flag if it wasn't there. Returning
     * null leaves the existing value untouched.
     */
    private fun MutableList<String>.pinValue(flag: List<String>, next: (String?) -> String?) {
        val i = indexOfFirst { token -> flag.any { token == it || token.startsWith("$it=") } }
        when {
            i < 0 -> next(null)?.let { this += listOf(flag.first(), it) }
            this[i].contains('=') ->
                next(this[i].substringAfter('='))?.let { this[i] = flag.first() + "=" + it }
            i + 1 < size -> next(this[i + 1])?.let { this[i + 1] = it }
            // Trailing flag with no value: a broken argv the visor would
            // reject anyway — complete it rather than shifting everything.
            else -> next(null)?.let { this += it }
        }
    }

    /** Accepts both `--flag value` and `--flag=value`. */
    private fun value(args: List<String>, flags: List<String>): String? {
        args.forEachIndexed { i, token ->
            flags.forEach { flag ->
                if (token.startsWith("$flag=")) return token.substringAfter('=')
                if (token == flag && i + 1 < args.size) return args[i + 1]
            }
        }
        return null
    }

    private fun ByteArray.toHex(): String = joinToString("") { "%02x".format(it) }

    private fun String.fromHex(): ByteArray = ByteArray(length / 2) { i ->
        substring(i * 2, i * 2 + 2).toInt(16).toByte()
    }

    private const val SALT_LEN = 16
}
