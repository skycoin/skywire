package com.skycoin.skywire.ui.wallet

/** One destination of a send as typed, before its values are checked. */
data class DestDraft(val address: String = "", val amountText: String = "", val hoursText: String = "")

/** Bulk send text, read; [line] is 1-based and names the line at fault. */
sealed class BulkParse {
    class Ok(val destinations: List<DestDraft>) : BulkParse()
    data object Empty : BulkParse()
    class BadLine(val line: Int) : BulkParse()
    class MixedHours(val line: Int) : BulkParse()
}

/**
 * Reads one destination per line, as `address,amount` or `address,amount,hours`,
 * the format skycoin-web's bulk send takes. Either every line has hours or none does.
 */
fun parseBulk(text: String): BulkParse {
    val lines = text.lines().map { it.trim() }.withIndex().filter { it.value.isNotEmpty() }
    if (lines.isEmpty()) return BulkParse.Empty
    var width = 0
    val out = ArrayList<DestDraft>()
    for ((i, line) in lines) {
        val fields = line.split(',').map { it.trim() }
        if (fields.size !in 2..3 || fields.any { it.isEmpty() }) return BulkParse.BadLine(i + 1)
        if (width != 0 && fields.size != width) return BulkParse.MixedHours(i + 1)
        width = fields.size
        out += DestDraft(fields[0], fields[1], fields.getOrElse(2) { "" })
    }
    return BulkParse.Ok(out)
}
