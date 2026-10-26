import Foundation

/// WalletCore for Bitcoin mainnet: BIP 84 keys, P2WPKH spends, an esplora
/// server for the chain view. Fee is sat/vB chosen by the user; transactions
/// signal RBF. A BIP 44 key source makes it a legacy wallet: P2PKH
/// addresses and the original sighash.
public final class BtcWalletCore: WalletCore {

    /// Core's dust threshold for P2WPKH outputs.
    public static let dustSats: UInt64 = 294
    /// Core's dust threshold for P2PKH outputs.
    public static let dustSatsP2PKH: UInt64 = 546
    private static let scanGap = 5
    private static let scanCap = 40

    private let api: BtcEsploraClient

    public init(esploraURL: String, session: URLSession = .shared, failover: NodeFailover? = nil) throws {
        api = try BtcEsploraClient(baseURL: esploraURL, session: session, failover: failover)
    }

    struct PlanInput: Sendable {
        let txid: String
        let vout: UInt32
        let value: UInt64
        let change: UInt32
        let index: UInt32
        let pubKeyHash: [UInt8]
    }

    struct Payload: Sendable {
        let inputs: [PlanInput]
        let outputs: [BtcTxn.Output]
        /// Fresh change chain index used by this plan, or -1 when no change.
        let changeIndex: Int
        /// Legacy P2PKH inputs, signed in their scriptSigs.
        let legacy: Bool
    }

    public func newSeed() -> String { Bip39.newMnemonic(entropyBits: 128) }

    public func validateSeed(_ mnemonic: String) -> Bool { Bip39.validate(mnemonic) }

    public func deriveAddresses(seed: KeySource, receiveCount: Int, changeCount: Int) throws -> AddressBook {
        let account = try BtcKeys.accountKey(seed)
        return AddressBook(
            receive: try (0..<receiveCount).map {
                BtcKeys.address(pubKey: try Bip84.key(account, change: 0, index: UInt32($0)).pubKey(), derivation: seed.derivation)
            },
            change: try (0..<changeCount).map {
                BtcKeys.address(pubKey: try Bip84.key(account, change: 1, index: UInt32($0)).pubKey(), derivation: seed.derivation)
            }
        )
    }

    public func validateAddress(_ address: String) -> Bool { BtcAddress.isValid(address) }

    public func scanUsed(seed: KeySource) async throws -> (receive: Int, change: Int) {
        let account = try BtcKeys.accountKey(seed)
        func scanChain(_ chain: UInt32, minimum: Int) async throws -> Int {
            var lastUsed = -1
            var i = 0
            var gap = 0
            while gap < Self.scanGap && i < Self.scanCap {
                let pub = try Bip84.key(account, change: chain, index: UInt32(i)).pubKey()
                let info = try await api.addressInfo(BtcKeys.address(pubKey: pub, derivation: seed.derivation))
                if info.chain.txCount > 0 || info.mempool.txCount > 0 {
                    lastUsed = i
                    gap = 0
                } else {
                    gap += 1
                }
                i += 1
            }
            return max(lastUsed + 1, minimum)
        }
        return (try await scanChain(0, minimum: 1), try await scanChain(1, minimum: 0))
    }

    public func balance(_ book: AddressBook) async throws -> WalletBalance {
        var confirmed: UInt64 = 0
        var all: UInt64 = 0
        var outputs = 0
        var per = [String: UInt64]()
        for addr in book.all() {
            var addrConfirmed: UInt64 = 0
            for u in try await api.utxos(addr) {
                all &+= u.value
                if u.status.confirmed {
                    confirmed &+= u.value
                    addrConfirmed &+= u.value
                    outputs += 1
                }
            }
            per[addr] = addrConfirmed
        }
        return WalletBalance(confirmed: confirmed, predicted: all, hours: nil, spendableOutputs: outputs, perAddress: per)
    }

    public func history(_ book: AddressBook) async throws -> [TxRecord] {
        let ours = Set(book.all())
        let tip = (try? await api.tipHeight()) ?? 0
        var seen = [String: TxRecord]()
        var order = [String]()
        for addr in book.all() {
            for tx in try await api.transactions(addr) where seen[tx.txid] == nil {
                var inSum: UInt64 = 0
                for v in tx.vin { if let p = v.prevout, let a = p.address, ours.contains(a) { inSum &+= p.value } }
                var outSum: UInt64 = 0
                for v in tx.vout { if let a = v.address, ours.contains(a) { outSum &+= v.value } }
                let incoming = inSum == 0
                let amount: UInt64
                if incoming {
                    amount = outSum
                } else {
                    // What actually left the wallet, fee excluded from the headline number.
                    let spent = inSum - min(inSum, outSum)
                    amount = spent >= tx.fee ? spent - tx.fee : spent
                }
                let party: String? = incoming
                    ? tx.vin.first { $0.prevout?.address != nil && !ours.contains($0.prevout!.address!) }?.prevout?.address
                    : tx.vout.first { $0.address != nil && !ours.contains($0.address!) }?.address
                let own = distinct(incoming
                    ? tx.vout.compactMap(\.address).filter { ours.contains($0) }
                    : tx.vin.compactMap { $0.prevout?.address }.filter { ours.contains($0) })
                let confirmations: Int64 = tx.status.confirmed && tip >= tx.status.blockHeight
                    ? tip - tx.status.blockHeight + 1
                    : 0
                seen[tx.txid] = TxRecord(
                    txid: tx.txid,
                    incoming: incoming,
                    amount: amount,
                    party: party,
                    timestamp: tx.status.confirmed ? tx.status.blockTime : Int64(Date().timeIntervalSince1970),
                    confirmed: tx.status.confirmed,
                    confirmations: confirmations,
                    fee: tx.fee,
                    own: own
                )
                order.append(tx.txid)
            }
        }
        return sortedForDisplay(order.map { seen[$0]! })
    }

    public func nodeInfo() async throws -> NodeInfo {
        NodeInfo(coinName: "Bitcoin", ticker: "BTC", height: try await api.tipHeight())
    }

    public func feePresets() async throws -> FeePresets? {
        let (economy, normal, priority) = try await api.feeRates()
        return FeePresets(economy: economy, normal: normal, priority: priority)
    }

    public func estimateMax(balance: WalletBalance, feeRate: Int?) -> UInt64 {
        let rate = UInt64(max(feeRate ?? 1, 1))
        if balance.spendableOutputs == 0 { return 0 }
        let vsize = UInt64(BtcTxn.estimateVsize(inputCount: balance.spendableOutputs, outputScripts: [[UInt8](repeating: 0, count: 22)]))
        let fee = rate &* vsize
        return balance.confirmed > fee ? balance.confirmed - fee : 0
    }

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
        if !more.isEmpty { throw WalletError.invalidAmount("this chain sends to one destination at a time") }
        if changeAddress != nil { throw WalletError.invalidAddress }
        guard let destScript = BtcAddress.scriptPubKey(toAddress) else { throw WalletError.invalidAddress }
        let rate = UInt64(max(feeRate ?? 1, 1))

        let legacy = seed.derivation == .bip44
        let dust = legacy ? Self.dustSatsP2PKH : Self.dustSats
        let account = try BtcKeys.accountKey(seed)
        // address → (chain, index, hash160)
        var programOf = [String: (UInt32, UInt32, [UInt8])]()
        for (i, a) in book.receive.enumerated() {
            programOf[a] = (0, UInt32(i), Hashes.hash160(try Bip84.key(account, change: 0, index: UInt32(i)).pubKey()))
        }
        for (i, a) in book.change.enumerated() {
            programOf[a] = (1, UInt32(i), Hashes.hash160(try Bip84.key(account, change: 1, index: UInt32(i)).pubKey()))
        }

        // Confirmed outputs only — the design promise is that pending funds
        // cannot be respent from this wallet. A chosen from-address spends
        // only what it holds.
        var utxos = [(String, BtcEsploraClient.Utxo)]()
        for addr in book.all() {
            if let fromAddress, addr != fromAddress { continue }
            for u in try await api.utxos(addr) where u.status.confirmed { utxos.append((addr, u)) }
        }
        if utxos.isEmpty { throw WalletError.insufficientBalance }
        // Largest first; a stable sort, as Kotlin's sortByDescending is.
        utxos = utxos.enumerated().sorted {
            $0.element.1.value != $1.element.1.value ? $0.element.1.value > $1.element.1.value : $0.offset < $1.offset
        }.map(\.element)

        let changeScriptLen = legacy ? 25 : 22 // P2PKH or P2WPKH change

        func planInput(_ p: (String, BtcEsploraClient.Utxo)) throws -> PlanInput {
            guard let program = programOf[p.0] else {
                throw WalletCoreError("output on \(p.0), which is not part of this wallet")
            }
            let (chain, index, hash) = program
            return PlanInput(txid: p.1.txid, vout: p.1.vout, value: p.1.value, change: chain, index: index, pubKeyHash: hash)
        }

        if sendMax {
            var total: UInt64 = 0
            for u in utxos { total &+= u.1.value }
            let vsize = BtcTxn.estimateVsize(inputCount: utxos.count, outputScripts: [destScript], legacy: legacy)
            let fee = rate &* UInt64(vsize)
            if total <= fee &+ dust { throw WalletError.insufficientBalance }
            let sendAmount = total - fee
            return TxPlan(
                toAddress: toAddress,
                amount: sendAmount,
                fee: fee,
                changeAmount: 0,
                hoursToRecipient: nil,
                hoursChange: nil,
                vsize: vsize,
                feeRate: Int(rate),
                payload: .btc(Payload(
                    inputs: try utxos.map(planInput),
                    outputs: [BtcTxn.Output(valueSats: sendAmount, scriptPubKey: destScript)],
                    changeIndex: -1,
                    legacy: legacy
                ))
            )
        }

        if amount == 0 { throw WalletError.invalidAmount("enter an amount to send") }
        if amount < dust { throw WalletError.invalidAmount("amount is below the dust limit") }

        var selected = [(String, BtcEsploraClient.Utxo)]()
        var inSum: UInt64 = 0
        var feeWithChange: UInt64 = 0
        let withChange = [destScript, [UInt8](repeating: 0, count: changeScriptLen)]
        for u in utxos {
            selected.append(u)
            inSum &+= u.1.value
            feeWithChange = rate &* UInt64(BtcTxn.estimateVsize(inputCount: selected.count, outputScripts: withChange, legacy: legacy))
            if inSum >= amount &+ feeWithChange { break }
        }
        if inSum < amount { throw WalletError.insufficientBalance }

        var change: UInt64 = inSum >= amount &+ feeWithChange ? inSum - amount - feeWithChange : 0
        var fee = feeWithChange
        var changeIndex = -1
        var outputs = [BtcTxn.Output(valueSats: amount, scriptPubKey: destScript)]

        if change < dust {
            // No change output: whatever is left over joins the fee.
            fee = inSum - amount
            let feeNoChange = rate &* UInt64(BtcTxn.estimateVsize(inputCount: selected.count, outputScripts: [destScript], legacy: legacy))
            if fee < feeNoChange { throw WalletError.insufficientBalance }
            change = 0
        } else {
            changeIndex = book.change.count
            let changePub = try Bip84.key(account, change: 1, index: UInt32(changeIndex)).pubKey()
            let changeAddr = BtcKeys.address(pubKey: changePub, derivation: seed.derivation)
            guard let changeScript = BtcAddress.scriptPubKey(changeAddr) else {
                throw WalletCoreError("derived change address failed to decode")
            }
            outputs.append(BtcTxn.Output(valueSats: change, scriptPubKey: changeScript))
        }

        let vsize = BtcTxn.estimateVsize(inputCount: selected.count, outputScripts: outputs.map(\.scriptPubKey), legacy: legacy)
        return TxPlan(
            toAddress: toAddress,
            amount: amount,
            fee: fee,
            changeAmount: change,
            hoursToRecipient: nil,
            hoursChange: nil,
            vsize: vsize,
            feeRate: Int(rate),
            payload: .btc(Payload(inputs: try selected.map(planInput), outputs: outputs, changeIndex: changeIndex, legacy: legacy))
        )
    }

    public func signTx(seed: KeySource, plan: TxPlan) throws -> SignedTx {
        guard case .btc(let payload) = plan.payload else { throw WalletCoreError("not a Bitcoin plan") }
        let account = try BtcKeys.accountKey(seed)
        var txn = try BtcTxn(
            inputs: payload.inputs.map {
                BtcTxn.Input(txid: $0.txid, vout: $0.vout, valueSats: $0.value, pubKeyHash: $0.pubKeyHash, legacy: payload.legacy)
            },
            outputs: payload.outputs
        )
        let keys = try payload.inputs.map { try Bip84.key(account, change: $0.change, index: $0.index).key }
        try txn.sign(keys: keys)
        return SignedTx(rawHex: txn.serialize().hex, txid: txn.txid(), changeIndex: payload.changeIndex)
    }

    public func broadcast(_ tx: SignedTx) async throws -> String {
        try await api.broadcast(rawHex: tx.rawHex)
    }

    /// The change chain index a broadcast plan consumed, or -1.
    public func consumedChangeIndex(_ tx: SignedTx) -> Int { tx.changeIndex ?? -1 }
}
