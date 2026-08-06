package com.skycoin.wallet.skycoin

import java.math.BigInteger

/**
 * Unsigned-transaction construction — a faithful port of the reference
 * implementation's transaction package (Create / ChooseSpends /
 * DistributeCoinHoursProportional / RequiredFee), hours-auto with share
 * factor 1/2, the same strategy the desktop wallet uses.
 *
 * Everything here is pure: unspents in, chosen transaction out. Network and
 * signing live elsewhere.
 */
object SkycoinCreate {

    /** An unspent output with its calculated hours at head time. */
    class UxBalance(
        val hash: ByteArray,        // uxid
        val hashHex: String,
        val bkSeq: ULong,
        val address: String,
        val coins: ULong,           // droplets
        val initialHours: ULong,
        val hours: ULong,           // calculated (accumulated) hours
    )

    class Destination(val address: String, val coins: ULong)

    class Created(
        val txn: SkycoinTxn,
        val spends: List<UxBalance>,
        val feeHours: ULong,        // burned
        val hoursToDestinations: ULong,
        val changeCoins: ULong,
        val changeHours: ULong,
    )

    class InsufficientBalance : Exception("balance is not sufficient")
    class InsufficientHours : Exception("hours are not sufficient")
    class NoFee : Exception("transaction has zero coinhour fee")
    class ZeroSpend : Exception("zero spend amount")

    /** ceil(hours / burnFactor) — the coin-hour fee the network demands. */
    fun requiredFee(hours: ULong, burnFactor: UInt): ULong {
        val bf = burnFactor.toULong()
        var fee = hours / bf
        if (hours % bf != 0uL) fee++
        return fee
    }

    fun remainingHours(hours: ULong, burnFactor: UInt): ULong = hours - requiredFee(hours, burnFactor)

    /**
     * Spend chooser, MinimizeUxOuts strategy: first the largest output that
     * carries hours, then zero-hour outputs largest first, then the rest.
     */
    fun chooseSpends(uxa: List<UxBalance>, coins: ULong, hours: ULong, burnFactor: UInt): List<UxBalance> {
        if (coins == 0uL) throw ZeroSpend()
        if (uxa.isEmpty()) throw InsufficientBalance()

        val nonzero = uxa.filter { it.hours != 0uL }.toMutableList()
        val zero = uxa.filter { it.hours == 0uL }.toMutableList()
        if (nonzero.isEmpty()) throw NoFee()

        sortCoinsHighToLow(nonzero)

        var haveCoins = 0uL
        var haveHours = 0uL
        val spending = ArrayList<UxBalance>()

        val first = nonzero.removeAt(0)
        spending.add(first)
        haveCoins += first.coins
        haveHours += first.hours
        if (haveCoins >= coins && remainingHours(haveHours, burnFactor) >= hours) return spending

        sortCoinsHighToLow(zero)
        for (ux in zero) {
            spending.add(ux)
            haveCoins += ux.coins
            haveHours += ux.hours
            if (haveCoins >= coins) break
        }
        if (haveCoins >= coins && remainingHours(haveHours, burnFactor) >= hours) return spending

        sortCoinsHighToLow(nonzero)
        for (ux in nonzero) {
            spending.add(ux)
            haveCoins += ux.coins
            haveHours += ux.hours
            if (haveCoins >= coins && remainingHours(haveHours, burnFactor) >= hours) return spending
        }

        if (haveCoins < coins) throw InsufficientBalance()
        throw InsufficientHours()
    }

    /** coins highest → hours lowest → oldest → uxid, the reference ordering. */
    private fun sortCoinsHighToLow(uxa: MutableList<UxBalance>) {
        uxa.sortWith { a, b ->
            when {
                a.coins != b.coins -> if (a.coins > b.coins) -1 else 1
                a.hours != b.hours -> if (a.hours < b.hours) -1 else 1
                a.bkSeq != b.bkSeq -> if (a.bkSeq < b.bkSeq) -1 else 1
                else -> compareBytes(a.hash, b.hash)
            }
        }
    }

    private fun sortHoursLowToHigh(uxa: MutableList<UxBalance>) {
        uxa.sortWith { a, b ->
            when {
                a.hours != b.hours -> if (a.hours < b.hours) -1 else 1
                a.coins != b.coins -> if (a.coins < b.coins) -1 else 1
                a.bkSeq != b.bkSeq -> if (a.bkSeq < b.bkSeq) -1 else 1
                else -> compareBytes(a.hash, b.hash)
            }
        }
    }

    private fun compareBytes(a: ByteArray, b: ByteArray): Int {
        for (i in a.indices) {
            val x = a[i].toInt() and 0xff
            val y = b[i].toInt() and 0xff
            if (x != y) return if (x < y) -1 else 1
        }
        return 0
    }

    /** Hours split across destinations proportional to coins, remainder rules intact. */
    fun distributeCoinHoursProportional(coins: List<ULong>, hours: ULong): List<ULong> {
        require(coins.isNotEmpty()) { "no destinations" }
        require(coins.all { it != 0uL }) { "zero-coin destination" }

        var total = BigInteger.ZERO
        for (c in coins) total = total.add(c.toBigInteger())
        val hoursInt = hours.toBigInteger()

        var assigned = 0uL
        val out = coins.map { c ->
            val frac = c.toBigInteger().multiply(hoursInt).divide(total)
            val h = frac.toLong().toULong()
            assigned += h
            h
        }.toMutableList()

        var remaining = hours - assigned
        var i = 0
        while (remaining > 0uL && i < out.size) {
            if (out[i] == 0uL) {
                out[i] = 1uL
                remaining--
            }
            i++
        }
        i = 0
        while (remaining > 0uL) {
            out[i] = out[i] + 1uL
            remaining--
            i++
        }
        return out
    }

    /**
     * Create the unsigned transaction. Hours selection is auto/share; the
     * share of post-burn hours sent onward is 1/2, retried at 1/1 when change
     * hours would otherwise be burned with no change output to carry them.
     */
    fun create(
        unspents: List<UxBalance>,
        to: List<Destination>,
        changeAddress: String,
        burnFactor: UInt,
        shareFull: Boolean = false,
        callCount: Int = 0,
    ): Created {
        require(to.isNotEmpty()) { "no destinations" }
        require(to.all { it.coins != 0uL }) { "zero-coin destination" }

        val txn = SkycoinTxn()
        var totalOutCoins = 0uL
        for (d in to) totalOutCoins = addOrThrow(totalOutCoins, d.coins)

        var spends = chooseSpends(unspents, totalOutCoins, 0uL, burnFactor)

        var totalInputCoins = 0uL
        var totalInputHours = 0uL
        for (s in spends) {
            totalInputCoins = addOrThrow(totalInputCoins, s.coins)
            totalInputHours = addOrThrow(totalInputHours, s.hours)
            txn.pushInput(s.hash)
        }

        var feeHours = requiredFee(totalInputHours, burnFactor)
        if (feeHours == 0uL) throw NoFee()
        var remaining = totalInputHours - feeHours

        val allocatedHours = if (shareFull) remaining else remaining / 2uL
        val addrHours = distributeCoinHoursProportional(to.map { it.coins }, allocatedHours)
        for ((i, d) in to.withIndex()) {
            txn.pushOutput(d.address, d.coins, addrHours[i])
        }

        var totalOutHours = 0uL
        for (h in addrHours) totalOutHours = addOrThrow(totalOutHours, h)
        if (totalOutCoins > totalInputCoins) throw InsufficientBalance()
        if (totalOutHours > remaining) throw InsufficientHours()

        var changeCoins = totalInputCoins - totalOutCoins
        var changeHours = remaining - totalOutHours

        // No coin change but hour change: force one more input when the extra
        // burn it causes costs less than the hours it saves.
        if (changeCoins == 0uL && changeHours > 0uL) {
            val leftovers = unspents.filter { u -> spends.none { it.hash.contentEquals(u.hash) } }.toMutableList()
            sortHoursLowToHigh(leftovers)
            if (leftovers.isNotEmpty()) {
                val extra = leftovers[0]
                val newTotalHours = addOrThrow(totalInputHours, extra.hours)
                val newFee = requiredFee(newTotalHours, burnFactor)
                check(newFee >= feeHours) { "fee decreased when adding an input" }
                val additionalFee = newFee - feeHours
                if (additionalFee < changeHours) {
                    changeCoins = extra.coins
                    check(extra.hours >= additionalFee) { "additional fee exceeds the extra input's hours" }
                    changeHours = addOrThrow(changeHours, extra.hours - additionalFee)
                    spends = spends + extra
                    txn.pushInput(extra.hash)
                    totalInputHours = newTotalHours
                    feeHours = newFee
                    remaining = totalInputHours - feeHours
                }
            }
        }

        // Still hours with nowhere to go: retry once sending the full share onward.
        if (changeCoins == 0uL && changeHours > 0uL && !shareFull) {
            if (callCount > 0) error("create already retried at full share")
            return create(unspents, to, changeAddress, burnFactor, shareFull = true, callCount = 1)
        }

        if (changeCoins > 0uL) {
            txn.pushOutput(changeAddress, changeCoins, changeHours)
        }

        // Null signatures give the unsigned transaction its final length.
        txn.sigs.clear()
        repeat(txn.inputs.size) { txn.sigs.add(ByteArray(65)) }
        txn.updateHeader()

        return Created(
            txn = txn,
            spends = spends,
            feeHours = feeHours,
            hoursToDestinations = totalOutHours,
            changeCoins = changeCoins,
            changeHours = changeHours,
        )
    }

    private fun addOrThrow(a: ULong, b: ULong): ULong {
        val sum = a + b
        if (sum < a) throw ArithmeticException("uint64 overflow")
        return sum
    }

    private fun ULong.toBigInteger(): BigInteger = BigInteger(toString())
}
