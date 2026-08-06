package com.skycoin.skywire.ui.logs

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/** Sources the viewer can tail. [APP_PREFIX] + app name selects one app. */
object LogSources {
    /** Visor runtime ring buffer over the local API. */
    const val CORE = "core"

    /** Captured child-process output — works when the visor won't start. */
    const val PROCESS = "process"

    // Dash, not colon: the value travels inside a navigation route path.
    const val APP_PREFIX = "app-"

    /**
     * [VISOR_PREFIX] + public key: the runtime buffer of a *remote* visor, the
     * one Fleet reads. Same route and same shape as [CORE] — the hypervisor
     * mux resolves the key and fetches over dmsg — so the viewer needs nothing
     * beyond knowing whose logs to ask for.
     */
    const val VISOR_PREFIX = "visor-"

    fun app(name: String) = APP_PREFIX + name
    fun appName(source: String) = source.removePrefix(APP_PREFIX)
    fun isApp(source: String) = source.startsWith(APP_PREFIX)

    fun visor(pk: String) = VISOR_PREFIX + pk
    fun visorPk(source: String) = source.removePrefix(VISOR_PREFIX)
    fun isVisor(source: String) = source.startsWith(VISOR_PREFIX)
}

enum class LogLevel { TRACE, DEBUG, INFO, WARN, ERROR, FATAL, UNKNOWN }

data class LogEntry(
    val timestamp: String,
    val level: LogLevel,
    val module: String,
    val message: String,
    /** Rendered line for copy/share. */
    val raw: String,
)

/**
 * Parsers for the three log formats the core emits.
 *
 * Runtime-logs entries are logrus JSON; app logs and the captured process
 * output are the text formatter's `[ts] LEVEL [module]: msg k="v"` lines —
 * and app lines carry ANSI color because in-proc apps force colors on a
 * non-TTY stream, so stripping is mandatory before parsing.
 */
object LogParser {

    private val json = Json { ignoreUnknownKeys = true; isLenient = true }

    private val ansi = Regex(
        "[\\u001B\\u009B][\\[\\]()#;?]*" +
            "(?:(?:(?:[a-zA-Z\\d]*(?:;[a-zA-Z\\d]*)*)?\\u0007)" +
            "|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))",
    )

    // The formatter sometimes inserts a caller-context token between the
    // level and the [module] (e.g. "DEBUG ClientSession.DialStream [dmsgC]:")
    // — matched lazily and dropped so the module still parses.
    private val textLine = Regex(
        "^\\[(?<ts>[^\\]]+)]\\s+(?<level>TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC)" +
            "(?:\\s+(?<ctx>[^\\[\\s]\\S*))??\\s*(?:\\[(?<module>[^\\]]*)]:)?\\s?(?<msg>.*)$",
    )

    fun level(name: String): LogLevel = when (name.uppercase()) {
        "TRACE" -> LogLevel.TRACE
        "DEBUG" -> LogLevel.DEBUG
        "INFO" -> LogLevel.INFO
        "WARN", "WARNING" -> LogLevel.WARN
        "ERROR" -> LogLevel.ERROR
        "FATAL", "PANIC" -> LogLevel.FATAL
        else -> LogLevel.UNKNOWN
    }

    /** One logrus-JSON entry from the runtime-logs ring buffer. */
    fun parseJsonEntry(line: String): LogEntry {
        val trimmed = line.trim()
        val obj = runCatching { json.parseToJsonElement(trimmed) as? JsonObject }.getOrNull()
            ?: return raw(trimmed)
        // Log fields are usually primitives but not always — a structured
        // value (object/array) must render, not throw.
        fun str(key: String) = obj[key]?.asText().orEmpty()
        val message = str("msg")
        if (message.isEmpty() && obj["level"] == null) return raw(trimmed)
        val extras = obj.entries
            .filter { it.key !in JSON_CORE_KEYS }
            .joinToString(" ") { (k, v) -> "$k=${v.asText()}" }
        return LogEntry(
            timestamp = str("time"),
            level = level(str("level")),
            module = str("_module"),
            message = if (extras.isEmpty()) message else "$message  $extras",
            raw = trimmed,
        )
    }

    /** One text-formatter line (app logs, captured process output). */
    fun parseTextLine(line: String): LogEntry {
        val clean = ansi.replace(line, "").trimEnd()
        if (clean.isBlank()) return raw(clean)
        val match = textLine.find(clean) ?: return raw(clean)
        return LogEntry(
            timestamp = match.groups["ts"]?.value.orEmpty(),
            level = level(match.groups["level"]?.value.orEmpty()),
            module = match.groups["module"]?.value.orEmpty(),
            message = match.groups["msg"]?.value.orEmpty(),
            raw = clean,
        )
    }

    private fun raw(line: String) =
        LogEntry(timestamp = "", level = LogLevel.UNKNOWN, module = "", message = line, raw = line)

    /** Primitive content without quotes; anything structured as its JSON. */
    private fun JsonElement.asText(): String = (this as? JsonPrimitive)?.content ?: toString()

    private val JSON_CORE_KEYS = setOf("time", "level", "msg", "_module", "log_line")
}
