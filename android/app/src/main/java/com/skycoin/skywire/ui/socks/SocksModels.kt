package com.skycoin.skywire.ui.socks

/**
 * The two skysocks-client flags this screen owns. The visor exposes the
 * app's argv over the API, so the flags are read from there and written
 * back as a whole — the server key stays put when only the port changes,
 * and vice versa.
 *
 * The visor writes `--srv` itself (a PUT with a `pk` field), so only the
 * listen address is ever rewritten from here.
 */
object SocksArgs {

    const val APP = "skysocks-client"

    /** Loopback-only by design — see ConfigManager's address pin. */
    const val HOST = "127.0.0.1"
    const val DEFAULT_PORT = 1080

    // The visor writes the double-dash form; the single-dash spelling is
    // accepted too because a hand-edited config may carry it.
    private val SRV = listOf("--srv", "-srv")
    private val ADDR = listOf("--addr", "-addr")

    fun serverPk(args: List<String>): String? = value(args, SRV)?.takeIf { it.isNotEmpty() }

    fun listenPort(args: List<String>): Int =
        value(args, ADDR)?.substringAfterLast(':')?.toIntOrNull() ?: DEFAULT_PORT

    /**
     * [args] rendered back as the single string the API's `args` field
     * takes, with the listen address re-pointed at [port]. Values are
     * quoted only when they need it — the server parses this with
     * shell-like rules.
     */
    fun withPort(args: List<String>, port: Int): String {
        val addr = "$HOST:$port"
        val flag = args.indexOfFirst { token -> ADDR.any { token == it || token.startsWith("$it=") } }
        val updated = when {
            flag < 0 -> args + listOf(ADDR.first(), addr)
            args[flag].contains('=') -> args.toMutableList()
                .also { it[flag] = args[flag].substringBefore('=') + "=" + addr }
            flag + 1 < args.size -> args.toMutableList().also { it[flag + 1] = addr }
            // Trailing flag with no value: a broken argv the visor would
            // reject anyway — complete it rather than shifting everything.
            else -> args + addr
        }
        return updated.joinToString(" ") { token ->
            if (token.any { it.isWhitespace() }) "\"" + token.replace("\"", "\\\"") + "\"" else token
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
}
