package com.skycoin.skywire.chat

import java.io.BufferedReader

/** One server-sent event: its `id:` (may be empty), `event:` name and joined `data:` lines. */
data class SseEvent(val id: String, val event: String, val data: String)

/**
 * Minimal text/event-stream parser (WHATWG "Server-sent events" §9.2.6):
 * fields up to a blank line make one event, `:` lines are comments.
 */
class SseReader(private val reader: BufferedReader) {

    private var lastId = ""

    /** The next event, or null when the stream ended. */
    fun next(): SseEvent? {
        var event = ""
        val data = StringBuilder()
        var any = false
        while (true) {
            val line = reader.readLine() ?: return null
            if (line.isEmpty()) {
                if (any && data.isNotEmpty()) {
                    return SseEvent(lastId, event, data.removeSuffix("\n").toString())
                }
                event = ""
                data.clear()
                any = false
                continue
            }
            if (line.startsWith(":")) continue
            val colon = line.indexOf(':')
            val field = if (colon < 0) line else line.substring(0, colon)
            var value = if (colon < 0) "" else line.substring(colon + 1)
            if (value.startsWith(" ")) value = value.substring(1)
            when (field) {
                "data" -> data.append(value).append('\n')
                "event" -> event = value
                "id" -> lastId = value
            }
            any = true
        }
    }
}
