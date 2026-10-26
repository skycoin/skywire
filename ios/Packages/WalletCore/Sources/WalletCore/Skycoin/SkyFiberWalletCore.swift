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
        let book: AddressBook
    }

    public func newSeed() -> String { Bip39.newMnemonic(entropyBits: 128) }

    public func validateSeed(_ mnemonic: String) -> Bool { Bip39.validate(mnemonic) }

    // The seed is the phrase's bytes (deriveAddresses), so no checksum is needed.
    public func acceptsLegacySeed(_ mnemonic: String) -> Bool { Bip39.legacyWords(mnemonic) }

    public func takesAnySeed() -> Bool { true }

    public func deriveAddresses(seed: KeySource, receiveCount: Int, changeCount: Int) throws -> AddressBook {
        if seed.derivation == .bip44 {
            let account = try SkycoinBip44.accountKey(mnemonic: seed.mnemonic, passphrase: seed.passphrase)
            func chain(_ c: UInt32, _ n: Int) throws -> [String] {
                try (0..<n).map {
                    SkycoinCrypto.addressFromPubKey(try SkycoinBip44.key(account, chain: c, index: UInt32($0)).pubKey())
                }
            }
            return AddressBook(receive: try chain(0, receiveCount), change: try chain(1, changeCount))
        }
        // The classic wallet has one chain; change returns to an address of it.
        let keys = try SkycoinCrypto.generateKeyPairs(seed: Array(seed.mnemonic.utf8), count: receiveCount)
        return AddressBook(receive: keys.map { SkycoinCrypto.addressFromPubKey($0.public) }, change: [])
    }

    /// The account and chain xpubs of a BIP 44 wallet, as skycoin's
    /// GetXPubKey gives them; nil otherwise.
    public func extendedPublicKeys(seed: KeySource) throws -> ExtendedPublicKeys? {
        if seed.derivation != .bip44 { return nil }
        let account = try SkycoinBip44.accountKey(mnemonic: seed.mnemonic, passphrase: seed.passphrase)
        return ExtendedPublicKeys(
            account: try account.xpub(),
            receive: try Bip32.ckdPriv(account, index: 0).xpub(),
            change: try Bip32.ckdPriv(account, index: 1).xpub()
        )
    }

    public func validateAddress(_ address: String) -> Bool { SkycoinCrypto.isValidAddress(address) }

    public func scanUsed(seed: KeySource) async throws -> (receive: Int, change: Int) {
        let receive = try await scanChain(start: 1) { n in try self.deriveAddresses(seed: seed, receiveCount: n, changeCount: 0).receive }
        // A BIP 44 wallet's change chain starts empty; its first address may never have been used.
        if seed.derivation != .bip44 { return (receive, 0) }
        let change = try await scanChain(start: 0) { n in try self.deriveAddresses(seed: seed, receiveCount: 0, changeCount: n).change }
        return (receive, change)
    }

    /// How many addresses of one chain are in use, looking scanAhead past
    /// the last used one.
    private func scanChain(start: Int, derive: (Int) throws -> [String]) async throws -> Int {
        var count = start
        while count < Self.scanCap {
            let window = Array(try derive(count + Self.scanAhead)[count..<(count + Self.scanAhead)])
            let used = try await usedAddresses(window)
            guard let lastUsed = window.lastIndex(where: { used.contains($0) }) else { break }
            count += lastUsed + 1
        }
        return min(count, Self.scanCap)
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
        // The per-address split: head outputs are the confirmed unspent
        // ones, which is exactly what the node's confirmed balance sums.
        var per = [String: UInt64]()
        for o in try await node.outputs(book.all()).headOutputs {
            let coins = Amounts.parse(o.coins, exponent: Self.dropletExponent) ?? 0
            per[o.address] = (per[o.address] ?? 0) &+ coins
        }
        return WalletBalance(
            confirmed: b.confirmed.coins,
            predicted: b.predicted.coins,
            hours: b.confirmed.hours,
            spendableOutputs: 0,
            perAddress: per
        )
    }

    /// The confirmed unspent outputs a coin picker can offer.
    public func utxos(_ book: AddressBook) async throws -> [UtxoRecord] {
        let outputs = try await node.outputs(book.all())
        let outgoing = Set(outputs.outgoingOutputs.map(\.hash))
        let records = outputs.headOutputs
            .filter { !outgoing.contains($0.hash) }
            .map {
                UtxoRecord(
                    uxid: $0.hash,
                    address: $0.address,
                    coins: Amounts.parse($0.coins, exponent: Self.dropletExponent) ?? 0,
                    hours: $0.calculatedHours
                )
            }
        // Largest first; a stable sort, as Kotlin's sortedByDescending is.
        return records.enumerated().sorted {
            $0.element.coins != $1.element.coins ? $0.element.coins > $1.element.coins : $0.offset < $1.offset
        }.map(\.element)
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
            let own = distinct(incoming
                ? txn.outputs.map(\.dst).filter { ours.contains($0) }
                : txn.inputs.map(\.owner).filter { ours.contains($0) })
            let ts = txn.timestamp > 0 ? Int64(txn.timestamp) : Int64(entry.time)
            return TxRecord(
                txid: txn.txid,
                incoming: incoming,
                amount: amount,
                party: party,
                timestamp: ts,
                confirmed: entry.status.confirmed,
                confirmations: Int64(entry.status.height),
                fee: txn.fee,
                own: own
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

    public func nodeInfo() async throws -> NodeInfo {
        let h = try await node.health()
        // A node that does not serve its progress is not called unsynced: nothing says it is.
        let progress = try? await node.progress()
        let name = !h.fiber.displayName.isEmpty ? h.fiber.displayName : (!h.fiber.name.isEmpty ? h.fiber.name : h.coin)
        let ticker = !h.fiber.ticker.isEmpty ? h.fiber.ticker : h.fiber.name.uppercased()
        var explorer = h.fiber.explorerURL
        while explorer.hasSuffix("/") { explorer.removeLast() }
        return NodeInfo(
            coinName: name.isEmpty ? nil : name.prefix(1).uppercased() + name.dropFirst(),
            ticker: ticker.isEmpty ? nil : ticker,
            version: h.version.version.isEmpty ? nil : h.version.version,
            height: Int64(truncatingIfNeeded: h.blockchain.head.seq),
            burnFactor: Int(h.userVerifyTxn.burnFactor),
            pubkey: h.blockchainPubkey.isEmpty ? nil : h.blockchainPubkey,
            explorerURL: explorer.isEmpty ? nil : explorer,
            // skycoin-web refuses nodes older than 0.24.0; the API this wallet calls is from there on.
            problem: Self.versionBelow(h.version.version, floor: [0, 24, 0]) ? .tooOld : nil,
            connections: h.openConnections >= 0 ? h.openConnections : nil,
            synced: progress.map { $0.current >= $0.highest }
        )
    }

    /// The chain as skycoin-web's Blockchain page shows it.
    public func chainInfo() async throws -> ChainInfo {
        let meta = try await node.metadata()
        let supply = try await node.coinSupply()
        return ChainInfo(
            height: Int64(truncatingIfNeeded: meta.head.seq),
            timestamp: Int64(truncatingIfNeeded: meta.head.timestamp),
            blockHash: meta.head.blockHash,
            currentSupply: supply.currentSupply,
            totalSupply: supply.totalSupply
        )
    }

    /// The node's peers, as skycoin-web's Networking page shows them.
    public func peers() async throws -> [PeerInfo] {
        try await node.connections().connections.map {
            PeerInfo(
                address: $0.address, height: Int64(truncatingIfNeeded: $0.height), outgoing: $0.outgoing,
                trusted: $0.trusted, userAgent: $0.userAgent, lastSent: $0.lastSent, lastReceived: $0.lastReceived
            )
        }
    }

    /// The node's unconfirmed transactions; `mine` marks those moving this
    /// wallet's coins.
    public func pending(_ book: AddressBook) async throws -> [PendingInfo] {
        let ours = Set(book.all())
        return try await node.pendingTxs().map { e in
            let t = e.transaction
            return PendingInfo(
                txid: t.txid,
                received: e.received,
                coins: Self.sumOfDroplets(t.outputs.map(\.coins)),
                mine: t.inputs.contains { ours.contains($0.owner) } || t.outputs.contains { ours.contains($0.dst) }
            )
        }
    }

    public func deletePending(txid: String) async throws {
        try await node.deletePending(txid: txid)
    }

    public func feePresets() async throws -> FeePresets? { nil }

    public func estimateMax(balance: WalletBalance, feeRate: Int?) -> UInt64 { balance.confirmed }

    public func buildTx(
        seed: KeySource,
        book: AddressBook,
        toAddress: String,
        amount: UInt64,
        feeRate: Int?,
        sendMax: Bool,
        fromAddress: String? = nil,
        inputs: Set<String>? = nil,
        hoursTo: UInt64? = nil,
        more: [Recipient] = [],
        changeAddress: String? = nil,
        shareFactor: Decimal? = nil,
        feeLevel: FeeLevel? = nil
    ) async throws -> TxPlan {
        let allTo = [toAddress] + more.map(\.address) + (changeAddress.map { [$0] } ?? [])
        if allTo.contains(where: { !validateAddress($0) }) { throw WalletError.invalidAddress }
        if sendMax && !more.isEmpty { throw WalletError.invalidAmount("Max sends to one destination") }
        if more.contains(where: { ($0.hours == nil) != (hoursTo == nil) }) {
            throw WalletError.invalidAmount("set the hours for every destination, or for none")
        }

        let params = try await node.health().userVerifyTxn

        let outputs = try await node.outputs(book.all())
        let outgoing = Set(outputs.outgoingOutputs.map(\.hash))
        // A chosen from-address spends only what it holds, and picked
        // outputs crowd out the automatic choice; the rest never enter the
        // selection.
        let chosen = outputs.headOutputs.filter {
            !outgoing.contains($0.hash)
                && (fromAddress == nil || $0.address == fromAddress)
                && (inputs == nil || inputs!.contains($0.hash))
        }
        let spendable = try chosen.map { o in
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
        let destinations = [SkycoinCreate.Destination(address: toAddress, coins: sendAmount, hours: hoursTo)]
            + more.map { SkycoinCreate.Destination(address: $0.address, coins: $0.amount, hours: $0.hours) }
        if destinations.contains(where: { $0.coins == 0 }) { throw WalletError.invalidAmount("enter an amount to send") }

        // The chain caps decimal precision; sub-precision amounts are rejected
        // by every node, so fail here with the limit spelled out. Wrapping,
        // as Kotlin's ULong product wraps.
        var divisor: UInt64 = 1
        for _ in 0..<max(0, Self.dropletExponent - params.maxDecimals) { divisor &*= 10 }
        if divisor == 0 { throw WalletCoreError("division by zero") }
        if destinations.contains(where: { $0.coins % divisor != 0 }) {
            throw WalletError.invalidAmount("amounts on this chain carry at most \(params.maxDecimals) decimals")
        }

        // A BIP 44 wallet's change goes to a fresh address of its change chain, as
        // skycoin's own BIP 44 wallet does; the classic wallet's goes back where it came from.
        let change: String
        if let changeAddress {
            change = changeAddress
        } else if seed.derivation == .bip44 {
            change = try deriveAddresses(seed: seed, receiveCount: 0, changeCount: book.change.count + 1).change.last!
        } else if let from = fromAddress ?? book.receive.first {
            change = from
        } else {
            throw WalletCoreError("wallet has no addresses")
        }
        let created: SkycoinCreate.Created
        do {
            created = try SkycoinCreate.create(
                unspents: spendable,
                to: destinations,
                changeAddress: change,
                burnFactor: params.burnFactor,
                shareFactor: shareFactor ?? SkycoinCreate.defaultShare
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

        var total: UInt64 = 0
        for d in destinations { total &+= d.coins }
        return TxPlan(
            toAddress: toAddress,
            amount: total,
            fee: created.feeHours,
            changeAmount: created.changeCoins,
            hoursToRecipient: created.hoursToDestinations,
            hoursChange: created.changeHours,
            vsize: nil,
            feeRate: nil,
            payload: .sky(Payload(txn: created.txn, inputAddresses: inputAddresses, book: book)),
            // The destinations are the transaction's first outputs, in order.
            recipients: destinations.enumerated().map { i, d in
                Recipient(address: d.address, amount: d.coins, hours: created.txn.outputs[i].hours)
            },
            changeAddress: change
        )
    }

    public func signTx(seed: KeySource, plan: TxPlan) throws -> SignedTx {
        guard case .sky(let payload) = plan.payload else { throw WalletCoreError("not a Skycoin-family plan") }
        let byAddress = try keysByAddress(seed, payload.book)
        let inputKeys = try payload.inputAddresses.map { addr in
            guard let key = byAddress[addr] else { throw WalletCoreError("input address \(addr) is not part of this wallet") }
            return key
        }
        var txn = payload.txn
        try txn.signInputs(keys: inputKeys)
        return SignedTx(rawHex: txn.serializeHex(), txid: txn.txidHex())
    }

    private func keysByAddress(_ seed: KeySource, _ book: AddressBook) throws -> [String: [UInt8]] {
        var byAddress = [String: [UInt8]]()
        if seed.derivation == .bip44 {
            let account = try SkycoinBip44.accountKey(mnemonic: seed.mnemonic, passphrase: seed.passphrase)
            for (chain, n) in [(UInt32(0), book.receive.count), (UInt32(1), book.change.count)] {
                for i in 0..<n {
                    let k = try SkycoinBip44.key(account, chain: chain, index: UInt32(i))
                    byAddress[SkycoinCrypto.addressFromPubKey(try k.pubKey())] = k.key
                }
            }
            return byAddress
        }
        for kp in try SkycoinCrypto.generateKeyPairs(seed: Array(seed.mnemonic.utf8), count: book.receive.count) {
            byAddress[SkycoinCrypto.addressFromPubKey(kp.public)] = kp.secret
        }
        return byAddress
    }

    public func broadcast(_ tx: SignedTx) async throws -> String {
        try await node.inject(rawTxHex: tx.rawHex)
    }

    public func unsignedHex(_ plan: TxPlan) -> String? {
        guard case .sky(let payload) = plan.payload else { return nil }
        return payload.txn.unsignedHex()
    }

    public func signRaw(seed: KeySource, book: AddressBook, rawHex: String) async throws -> SignedTx {
        var txn = try Self.decodeRaw(rawHex)
        // The owner of each input is the address its unspent output sits at.
        var owners = [String: String]()
        for o in try await node.outputs(book.all()).headOutputs { owners[o.hash] = o.address }
        let byAddress = try keysByAddress(seed, book)
        let keys = try txn.inputs.map { uxid in
            guard let owner = owners[uxid.hex], let key = byAddress[owner] else { throw WalletError.notInWallet }
            return key
        }
        try txn.signInputs(keys: keys)
        return SignedTx(rawHex: txn.serializeHex(), txid: txn.txidHex())
    }

    public func broadcastRaw(_ rawHex: String) async throws -> String {
        let txn = try Self.decodeRaw(rawHex)
        if !txn.isSigned() { throw WalletError.badTransaction("the transaction is not signed") }
        return try await node.inject(rawTxHex: rawHex.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    private static func decodeRaw(_ rawHex: String) throws -> SkycoinTxn {
        do {
            return try SkycoinTxn.decode(rawHex)
        } catch let e as WalletCoreError {
            throw WalletError.badTransaction(e.message)
        }
    }

    /// True when `version` parses and is below `floor`; an unknown version
    /// is not held against a node.
    static func versionBelow(_ version: String, floor: [Int]) -> Bool {
        var v = version.trimmingCharacters(in: .whitespacesAndNewlines)
        if v.hasPrefix("v") { v.removeFirst() }
        if let dash = v.firstIndex(of: "-") { v = String(v[..<dash]) }
        var parts = [Int]()
        for p in v.split(separator: ".", omittingEmptySubsequences: false) {
            guard let n = Int(p) else { return false }
            parts.append(n)
        }
        for (i, f) in floor.enumerated() {
            let n = i < parts.count ? parts[i] : 0
            if n != f { return n < f }
        }
        return false
    }
}

public struct ChainInfo: Equatable, Sendable {
    public let height: Int64
    public let timestamp: Int64
    public let blockHash: String
    public let currentSupply: String
    public let totalSupply: String
}

public struct PeerInfo: Equatable, Sendable {
    public let address: String
    public let height: Int64
    public let outgoing: Bool
    public let trusted: Bool
    public let userAgent: String
    public let lastSent: Int64
    public let lastReceived: Int64
}

/// One unconfirmed transaction in a node's pool; `mine` when it moves this
/// wallet's coins.
public struct PendingInfo: Equatable, Sendable {
    public let txid: String
    public let received: String
    public let coins: UInt64
    public let mine: Bool
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

/// The list with repeats dropped, first occurrence kept (Kotlin's distinct).
func distinct(_ items: [String]) -> [String] {
    var seen = Set<String>()
    return items.filter { seen.insert($0).inserted }
}
