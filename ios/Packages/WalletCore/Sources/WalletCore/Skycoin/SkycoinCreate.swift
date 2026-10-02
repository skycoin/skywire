/// Unsigned-transaction construction — a faithful port of the reference
/// implementation's transaction package (Create / ChooseSpends /
/// DistributeCoinHoursProportional / RequiredFee), hours-auto with share
/// factor 1/2, the same strategy the desktop wallet uses.
///
/// Everything here is pure: unspents in, chosen transaction out. Network and
/// signing live elsewhere.
public enum SkycoinCreate {

    /// An unspent output with its calculated hours at head time.
    public struct UxBalance: Sendable {
        public let hash: [UInt8] // uxid
        public let hashHex: String
        public let bkSeq: UInt64
        public let address: String
        public let coins: UInt64 // droplets
        public let initialHours: UInt64
        public let hours: UInt64 // calculated (accumulated) hours

        public init(
            hash: [UInt8], hashHex: String, bkSeq: UInt64, address: String,
            coins: UInt64, initialHours: UInt64, hours: UInt64
        ) {
            self.hash = hash
            self.hashHex = hashHex
            self.bkSeq = bkSeq
            self.address = address
            self.coins = coins
            self.initialHours = initialHours
            self.hours = hours
        }
    }

    public struct Destination: Sendable {
        public let address: String
        public let coins: UInt64

        public init(address: String, coins: UInt64) {
            self.address = address
            self.coins = coins
        }
    }

    public struct Created: Sendable {
        public let txn: SkycoinTxn
        public let spends: [UxBalance]
        public let feeHours: UInt64 // burned
        public let hoursToDestinations: UInt64
        public let changeCoins: UInt64
        public let changeHours: UInt64
    }

    public enum CreateError: Error, Equatable {
        case insufficientBalance // "balance is not sufficient"
        case insufficientHours // "hours are not sufficient"
        case noFee // "transaction has zero coinhour fee"
        case zeroSpend // "zero spend amount"
    }

    /// ceil(hours / burnFactor) — the coin-hour fee the network demands.
    public static func requiredFee(hours: UInt64, burnFactor: UInt32) -> UInt64 {
        let bf = UInt64(burnFactor)
        var fee = hours / bf
        if hours % bf != 0 { fee += 1 }
        return fee
    }

    public static func remainingHours(_ hours: UInt64, burnFactor: UInt32) -> UInt64 {
        hours - requiredFee(hours: hours, burnFactor: burnFactor)
    }

    /// Spend chooser, MinimizeUxOuts strategy: first the largest output that
    /// carries hours, then zero-hour outputs largest first, then the rest.
    public static func chooseSpends(
        _ uxa: [UxBalance], coins: UInt64, hours: UInt64, burnFactor: UInt32
    ) throws -> [UxBalance] {
        if coins == 0 { throw CreateError.zeroSpend }
        if uxa.isEmpty { throw CreateError.insufficientBalance }

        var nonzero = uxa.filter { $0.hours != 0 }
        var zero = uxa.filter { $0.hours == 0 }
        if nonzero.isEmpty { throw CreateError.noFee }

        sortCoinsHighToLow(&nonzero)

        // Wrapping sums, as Kotlin's ULong arithmetic wraps: absurd amounts
        // from a node end in create()'s overflow check, not a trap here.
        var haveCoins: UInt64 = 0
        var haveHours: UInt64 = 0
        var spending = [UxBalance]()

        let first = nonzero.removeFirst()
        spending.append(first)
        haveCoins &+= first.coins
        haveHours &+= first.hours
        if haveCoins >= coins && remainingHours(haveHours, burnFactor: burnFactor) >= hours { return spending }

        sortCoinsHighToLow(&zero)
        for ux in zero {
            spending.append(ux)
            haveCoins &+= ux.coins
            haveHours &+= ux.hours
            if haveCoins >= coins { break }
        }
        if haveCoins >= coins && remainingHours(haveHours, burnFactor: burnFactor) >= hours { return spending }

        sortCoinsHighToLow(&nonzero)
        for ux in nonzero {
            spending.append(ux)
            haveCoins &+= ux.coins
            haveHours &+= ux.hours
            if haveCoins >= coins && remainingHours(haveHours, burnFactor: burnFactor) >= hours { return spending }
        }

        if haveCoins < coins { throw CreateError.insufficientBalance }
        throw CreateError.insufficientHours
    }

    /// coins highest → hours lowest → oldest → uxid, the reference ordering.
    private static func sortCoinsHighToLow(_ uxa: inout [UxBalance]) {
        uxa.sort { a, b in
            if a.coins != b.coins { return a.coins > b.coins }
            if a.hours != b.hours { return a.hours < b.hours }
            if a.bkSeq != b.bkSeq { return a.bkSeq < b.bkSeq }
            return compareBytes(a.hash, b.hash) < 0
        }
    }

    private static func sortHoursLowToHigh(_ uxa: inout [UxBalance]) {
        uxa.sort { a, b in
            if a.hours != b.hours { return a.hours < b.hours }
            if a.coins != b.coins { return a.coins < b.coins }
            if a.bkSeq != b.bkSeq { return a.bkSeq < b.bkSeq }
            return compareBytes(a.hash, b.hash) < 0
        }
    }

    private static func compareBytes(_ a: [UInt8], _ b: [UInt8]) -> Int {
        for i in a.indices where a[i] != b[i] {
            return a[i] < b[i] ? -1 : 1
        }
        return 0
    }

    /// Hours split across destinations proportional to coins, remainder
    /// rules intact.
    public static func distributeCoinHoursProportional(coins: [UInt64], hours: UInt64) throws -> [UInt64] {
        try require(!coins.isEmpty, "no destinations")
        try require(coins.allSatisfy { $0 != 0 }, "zero-coin destination")

        var total = BigUInt.zero
        for c in coins { total = total + BigUInt(c) }
        let hoursBig = BigUInt(hours)

        var assigned: UInt64 = 0
        var out = coins.map { c -> UInt64 in
            // c ≤ total, so the share fits 64 bits.
            let h = (BigUInt(c) * hoursBig / total).clampedUInt64
            assigned += h
            return h
        }

        var remaining = hours - assigned
        var i = 0
        while remaining > 0 && i < out.count {
            if out[i] == 0 {
                out[i] = 1
                remaining -= 1
            }
            i += 1
        }
        i = 0
        while remaining > 0 {
            out[i] += 1
            remaining -= 1
            i += 1
        }
        return out
    }

    /// Create the unsigned transaction. Hours selection is auto/share; the
    /// share of post-burn hours sent onward is 1/2, retried at 1/1 when
    /// change hours would otherwise be burned with no change output to carry
    /// them.
    public static func create(
        unspents: [UxBalance],
        to: [Destination],
        changeAddress: String,
        burnFactor: UInt32,
        shareFull: Bool = false,
        callCount: Int = 0
    ) throws -> Created {
        try require(!to.isEmpty, "no destinations")
        try require(to.allSatisfy { $0.coins != 0 }, "zero-coin destination")

        var txn = SkycoinTxn()
        var totalOutCoins: UInt64 = 0
        for d in to { totalOutCoins = try addOrThrow(totalOutCoins, d.coins) }

        var spends = try chooseSpends(unspents, coins: totalOutCoins, hours: 0, burnFactor: burnFactor)

        var totalInputCoins: UInt64 = 0
        var totalInputHours: UInt64 = 0
        for s in spends {
            totalInputCoins = try addOrThrow(totalInputCoins, s.coins)
            totalInputHours = try addOrThrow(totalInputHours, s.hours)
            try txn.pushInput(s.hash)
        }

        var feeHours = requiredFee(hours: totalInputHours, burnFactor: burnFactor)
        if feeHours == 0 { throw CreateError.noFee }
        var remaining = totalInputHours - feeHours

        let allocatedHours = shareFull ? remaining : remaining / 2
        let addrHours = try distributeCoinHoursProportional(coins: to.map(\.coins), hours: allocatedHours)
        for (i, d) in to.enumerated() {
            try txn.pushOutput(address: d.address, coins: d.coins, hours: addrHours[i])
        }

        var totalOutHours: UInt64 = 0
        for h in addrHours { totalOutHours = try addOrThrow(totalOutHours, h) }
        if totalOutCoins > totalInputCoins { throw CreateError.insufficientBalance }
        if totalOutHours > remaining { throw CreateError.insufficientHours }

        var changeCoins = totalInputCoins - totalOutCoins
        var changeHours = remaining - totalOutHours

        // No coin change but hour change: force one more input when the extra
        // burn it causes costs less than the hours it saves.
        if changeCoins == 0 && changeHours > 0 {
            var leftovers = unspents.filter { u in !spends.contains { $0.hash == u.hash } }
            sortHoursLowToHigh(&leftovers)
            if let extra = leftovers.first {
                let newTotalHours = try addOrThrow(totalInputHours, extra.hours)
                let newFee = requiredFee(hours: newTotalHours, burnFactor: burnFactor)
                try require(newFee >= feeHours, "fee decreased when adding an input")
                let additionalFee = newFee - feeHours
                if additionalFee < changeHours {
                    changeCoins = extra.coins
                    try require(extra.hours >= additionalFee, "additional fee exceeds the extra input's hours")
                    changeHours = try addOrThrow(changeHours, extra.hours - additionalFee)
                    spends.append(extra)
                    try txn.pushInput(extra.hash)
                    totalInputHours = newTotalHours
                    feeHours = newFee
                    remaining = totalInputHours - feeHours
                }
            }
        }

        // Still hours with nowhere to go: retry once sending the full share onward.
        if changeCoins == 0 && changeHours > 0 && !shareFull {
            try require(callCount == 0, "create already retried at full share")
            return try create(
                unspents: unspents, to: to, changeAddress: changeAddress, burnFactor: burnFactor,
                shareFull: true, callCount: 1
            )
        }

        if changeCoins > 0 {
            try txn.pushOutput(address: changeAddress, coins: changeCoins, hours: changeHours)
        }

        // Null signatures give the unsigned transaction its final length.
        txn.sigs = Array(repeating: [UInt8](repeating: 0, count: 65), count: txn.inputs.count)
        txn.updateHeader()

        return Created(
            txn: txn,
            spends: spends,
            feeHours: feeHours,
            hoursToDestinations: totalOutHours,
            changeCoins: changeCoins,
            changeHours: changeHours
        )
    }

    private static func addOrThrow(_ a: UInt64, _ b: UInt64) throws -> UInt64 {
        let (sum, overflow) = a.addingReportingOverflow(b)
        if overflow { throw WalletCoreError("uint64 overflow") }
        return sum
    }
}
