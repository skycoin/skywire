package com.skycoin.wallet

/** Fixed-point parsing and rendering for base-unit integer amounts. */
object Amounts {

    /**
     * Parse a decimal string into base units with the given exponent
     * (6 for droplets, 8 for satoshis). Null on malformed input or more
     * fractional digits than the exponent allows.
     */
    fun parse(text: String, exponent: Int): ULong? {
        val t = text.trim()
        if (t.isEmpty() || t.count { it == '.' } > 1) return null
        val parts = t.split('.')
        val whole = parts[0].ifEmpty { "0" }
        val frac = if (parts.size == 2) parts[1] else ""
        if (whole.any { !it.isDigit() } || frac.any { !it.isDigit() }) return null
        if (frac.length > exponent) return null
        val fracPadded = frac.padEnd(exponent, '0')
        return try {
            val w = whole.toULong()
            val scale = pow10(exponent)
            val scaled = w * scale
            if (w != 0uL && scaled / w != scale) return null // overflow
            val f = if (fracPadded.isEmpty()) 0uL else fracPadded.toULong()
            val sum = scaled + f
            if (sum < scaled) return null
            sum
        } catch (e: NumberFormatException) {
            null
        }
    }

    /** Count of decimal places actually used (validating against a chain's max precision). */
    fun decimals(text: String): Int {
        val i = text.indexOf('.')
        return if (i < 0) 0 else text.trim().length - i - 1
    }

    /** Render base units at full precision, trailing zeros kept to minDecimals. */
    fun format(units: ULong, exponent: Int, minDecimals: Int = -1): String {
        val scale = pow10(exponent)
        val whole = units / scale
        val frac = units % scale
        val keep = if (minDecimals >= 0) minDecimals else exponent
        var fracStr = frac.toString().padStart(exponent, '0')
        while (fracStr.length > keep && fracStr.endsWith('0')) {
            fracStr = fracStr.dropLast(1)
        }
        val wholeStr = groupThousands(whole.toString())
        return if (fracStr.isEmpty()) wholeStr else "$wholeStr.$fracStr"
    }

    fun groupThousands(digits: String): String {
        val sb = StringBuilder()
        digits.forEachIndexed { i, c ->
            if (i > 0 && (digits.length - i) % 3 == 0) sb.append(',')
            sb.append(c)
        }
        return sb.toString()
    }

    private fun pow10(n: Int): ULong {
        var v = 1uL
        repeat(n) { v *= 10uL }
        return v
    }
}
