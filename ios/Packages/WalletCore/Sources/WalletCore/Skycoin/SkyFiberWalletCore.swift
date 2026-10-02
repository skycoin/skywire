import Foundation

/// WalletCore for Skycoin and every fiber chain — same daemon, same rules,
/// different node URL. Transaction verification parameters (burn factor,
/// decimal precision, size cap) are read from the node's health endpoint, so
/// a fiber chain with its own settings is honored automatically.
public final class SkyFiberWalletCore: WalletCore {

    public static let dropletExponent = 6
    private static let scanAhead = 10
    private static let scanCap = 100

    private let node: SkycoinNodeClient

    public init(nodeURL: String, session: URLSession = .shared) throws {
        node = try SkycoinNodeClient(baseURL: nodeURL, session: session)
    }

    struct Payload: Sendable {
        let txn: SkycoinTxn
        /// Owning address of each input, in input order.
        let inputAddresses: [String]
        let receiveAddresses: [String]
    }

    public func newSeed() -> String { Bip39.newMnemonic(entropyBits: 128) }

    public func validateSeed(_ mnemonic: String) -> Bool { Bip39.validate(mnemonic) }

    public func deriveAddresses(seed: String, receiveCount: Int, changeCount: Int) throws -> AddressBook {
        let keys = try SkycoinCrypto.generateKeyPairs(seed: Array(seed.utf8), count: receiveCount)
        return AddressBook(receive: keys.map { SkycoinCrypto.addressFromPubKey($0.public) }, change: [])
    }

    public func validateAddress(_ address: String) -> Bool { SkycoinCrypto.isValidAddress(address) }

    public func scanUsed(seed: String) async throws -> (receive: Int, change: Int) {
        var count = 1
        while count < Self.scanCap {
            let window = Array(try deriveAddresses(seed: seed, receiveCount: count + Self.scanAhead, changeCount: 0)
                .receive[count..<(count + Self.scanAhead)])
            let used = try await usedAddresses(window)
            guard let lastUsed = window.lastIndex(where: { used.contains($0) }) else { break }
            count += lastUsed + 1
        }
        return (min(count, Self.scanCap), 0)
    }

    private func usedAddresses(_ addrs: [String]) async throws -> Set<String> {
        let txns = try await node.transactions(addrs)
        let probe = Set(addrs)
        var used = Set<String>()
        for entry in txns {
            for i in entry.txn.inputs where probe.contains(i.owner) { used.insert(i.owner) }
            for o in entry.txn.outputs where probe.contains(o.dst) { used.insert(o.dst) }
        }
        return used
    }

    public func balance(_ book: AddressBook) async throws -> WalletBalance {
        let b = try await node.balance(book.all())
        return WalletBalance(
            confirmed: b.confirmed.coins,
            predicted: b.predicted.coins,
            hours: b.confirmed.hours,
            spendableOutputs: 0
        )
    }

    public func history(_ book: AddressBook) async throws -> [TxRecord] {
        let ours = Set(book.all())
        let records = try await node.transactions(book.all()).map { entry -> TxRecord in
            let txn = entry.txn
            let inSum = Self.sumOfDroplets(txn.inputs.filter { ours.contains($0.owner) }.map(\.coins))
            let outSum = Self.sumOfDroplets(txn.outputs.filter { ours.contains($0.dst) }.map(\.coins))
            let incoming = inSum == 0
            let amount = incoming ? outSum : inSum - min(inSum, outSum)
            let party: String? = incoming
                ? (txn.inputs.first { !ours.contains($0.owner) }?.owner ?? txn.inputs.first?.owner)
                : txn.outputs.first { !ours.contains($0.dst) }?.dst
            let ts = txn.timestamp > 0 ? Int64(txn.timestamp) : Int64(entry.time)
            return TxRecord(
                txid: txn.txid,
                incoming: incoming,
                amount: amount,
                party: party,
                timestamp: ts,
                confirmed: entry.status.confirmed,
                confirmations: Int64(entry.status.height),
                fee: txn.fee
            )
        }
        return sortedForDisplay(records)
    }

    /// Wrapping, as Kotlin's ULong sum wraps; an unparseable amount adds 0.
    private static func sumOfDroplets(_ amounts: [String]) -> UInt64 {
        var sum: UInt64 = 0
        for a in amounts { sum &+= Amounts.parse(a, exponent: dropletExponent) ?? 0 }
        return sum
    }

    public func feePresets() async throws -> FeePresets? { nil }

    public func estimateMax(balance: WalletBalance, feeRate: Int?) -> UInt64 { balance.confirmed }

    public func buildTx(
        seed: String,
        book: AddressBook,
        toAddress: String,
        amount: UInt64,
        feeRate: Int?,
        sendMax: Bool
    ) async throws -> TxPlan {
        if !validateAddress(toAddress) { throw WalletError.invalidAddress }

        let params = try await node.health().userVerifyTxn

        let outputs = try await node.outputs(book.all())
        let outgoing = Set(outputs.outgoingOutputs.map(\.hash))
        let spendable = try outputs.headOutputs.filter { !outgoing.contains($0.hash) }.map { o in
            guard let hash = [UInt8](hex: o.hash), hash.count == 32 else {
                throw WalletError.nodeRejected("node reported an unparseable output hash")
            }
            guard let coins = Amounts.parse(o.coins, exponent: Self.dropletExponent) else {
                throw WalletError.nodeRejected("node reported an unparseable output amount")
            }
            return SkycoinCreate.UxBalance(
                hash: hash,
                hashHex: o.hash,
                bkSeq: o.blockSeq,
                address: o.address,
                coins: coins,
                initialHours: o.hours,
                hours: o.calculatedHours
            )
        }

        let sendAmount: UInt64
        if sendMax {
            var total: UInt64 = 0
            for u in spendable { total &+= u.coins }
            if total == 0 { throw WalletError.insufficientBalance }
            sendAmount = total
        } else {
            sendAmount = amount
        }
        if sendAmount == 0 { throw WalletError.invalidAmount("enter an amount to send") }

        // The chain caps decimal precision; sub-precision amounts are rejected
        // by every node, so fail here with the limit spelled out.
        var divisor: UInt64 = 1
        for _ in 0..<max(0, Self.dropletExponent - params.maxDecimals) { divisor *= 10 }
        if sendAmount % divisor != 0 {
            throw WalletError.invalidAmount("amounts on this chain carry at most \(params.maxDecimals) decimals")
        }

        guard let changeAddress = book.receive.first else { throw WalletCoreError("wallet has no addresses") }
        let created: SkycoinCreate.Created
        do {
            created = try SkycoinCreate.create(
                unspents: spendable,
                to: [SkycoinCreate.Destination(address: toAddress, coins: sendAmount)],
                changeAddress: changeAddress,
                burnFactor: params.burnFactor
            )
        } catch SkycoinCreate.CreateError.insufficientBalance {
            throw WalletError.insufficientBalance
        } catch SkycoinCreate.CreateError.insufficientHours {
            throw WalletError.insufficientHours
        } catch SkycoinCreate.CreateError.noFee {
            throw WalletError.noHoursToBurn
        } catch SkycoinCreate.CreateError.zeroSpend {
            throw WalletError.invalidAmount("enter an amount to send")
        }

        let size = created.txn.serialize().count
        if size > Int(params.maxTransactionSize) {
            throw WalletError.nodeRejected(
                "transaction of \(size) bytes exceeds the chain limit of \(params.maxTransactionSize)"
            )
        }

        let inputAddresses = try created.txn.inputs.map { uxid in
            guard let spend = created.spends.first(where: { $0.hash == uxid }) else {
                throw WalletCoreError("input without its spent output")
            }
            return spend.address
        }

        return TxPlan(
            toAddress: toAddress,
            amount: sendAmount,
            fee: created.feeHours,
            changeAmount: created.changeCoins,
            hoursToRecipient: created.hoursToDestinations,
            hoursChange: created.changeHours,
            vsize: nil,
            feeRate: nil,
            payload: .sky(Payload(txn: created.txn, inputAddresses: inputAddresses, receiveAddresses: book.receive))
        )
    }

    public func signTx(seed: String, plan: TxPlan) throws -> SignedTx {
        guard case .sky(let payload) = plan.payload else { throw WalletCoreError("not a Skycoin-family plan") }
        let keys = try SkycoinCrypto.generateKeyPairs(seed: Array(seed.utf8), count: payload.receiveAddresses.count)
        var byAddress = [String: [UInt8]]()
        for kp in keys { byAddress[SkycoinCrypto.addressFromPubKey(kp.public)] = kp.secret }
        let inputKeys = try payload.inputAddresses.map { addr in
            guard let key = byAddress[addr] else { throw WalletCoreError("input address \(addr) is not part of this wallet") }
            return key
        }
        var txn = payload.txn
        try txn.signInputs(keys: inputKeys)
        return SignedTx(rawHex: txn.serializeHex(), txid: txn.txidHex())
    }

    public func broadcast(_ tx: SignedTx) async throws -> String {
        try await node.inject(rawTxHex: tx.rawHex)
    }
}

/// Pending first, then newest first — every core's history order
/// (Kotlin: compareBy { confirmed }.thenByDescending { timestamp }, a stable
/// sort).
func sortedForDisplay(_ records: [TxRecord]) -> [TxRecord] {
    records.enumerated().sorted { a, b in
        if a.element.confirmed != b.element.confirmed { return !a.element.confirmed }
        if a.element.timestamp != b.element.timestamp { return a.element.timestamp > b.element.timestamp }
        return a.offset < b.offset
    }.map(\.element)
}
