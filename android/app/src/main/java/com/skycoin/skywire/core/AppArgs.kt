package com.skycoin.skywire.core

/**
 * Argv editing for the app flags the phone owns in the visor config.
 *
 * Every app profile ([SkychatProfile], [SkydexProfile]) does the same two
 * things to the argv the generator produced — read a flag's value, and force a
 * flag to the value this device requires — so the parsing lives once. Both
 * spellings the config can carry are accepted: `--flag value` and `--flag=value`.
 */

/**
 * Set [flag]'s value to what [next] returns for the current one (null when the
 * flag is absent), appending the flag if it wasn't there. Returning null leaves
 * the existing value untouched.
 */
internal fun MutableList<String>.pinValue(flag: List<String>, next: (String?) -> String?) {
    val i = indexOfFirst { token -> flag.any { token == it || token.startsWith("$it=") } }
    when {
        i < 0 -> next(null)?.let { this += listOf(flag.first(), it) }
        this[i].contains('=') ->
            next(this[i].substringAfter('='))?.let { this[i] = flag.first() + "=" + it }
        i + 1 < size -> next(this[i + 1])?.let { this[i + 1] = it }
        // Trailing flag with no value: a broken argv the visor would reject
        // anyway — complete it rather than shifting everything.
        else -> next(null)?.let { this += it }
    }
}

/** First value of any of [flags] in [args], or null when none is present. */
internal fun argValue(args: List<String>, flags: List<String>): String? {
    args.forEachIndexed { i, token ->
        flags.forEach { flag ->
            if (token.startsWith("$flag=")) return token.substringAfter('=')
            if (token == flag && i + 1 < args.size) return args[i + 1]
        }
    }
    return null
}

/**
 * The host every listener the phone starts must bind. Android has no per-app
 * network namespace, so a listener on any other address is one every device on
 * the same network can reach; loopback is the narrowest an app can ask for.
 */
internal const val LOOPBACK_HOST = "127.0.0.1"

/**
 * `<host>:<port>` with the host forced to [LOOPBACK_HOST], keeping whatever
 * port [current] carries and falling back to [defaultPort] when it has none.
 * Returns null for a value it cannot parse — a malformed address only gets
 * worse from rewriting, and the visor reports it.
 */
internal fun loopbackAddr(current: String?, defaultPort: Int): String? {
    val port = current?.substringAfterLast(':')?.toIntOrNull()
    return when {
        port != null -> "$LOOPBACK_HOST:$port"
        current == null -> "$LOOPBACK_HOST:$defaultPort"
        else -> null
    }
}
