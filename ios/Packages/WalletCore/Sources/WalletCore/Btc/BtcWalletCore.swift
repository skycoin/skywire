import Foundation

/// WalletCore for Bitcoin mainnet: BIP 84 keys, P2WPKH spends, an esplora
/// server for the chain view. Fee is sat/vB chosen by the user; transactions
/// signal RBF.
public final class BtcWalletCore: WalletCore {

    /// Core's dust threshold for P2WPKH outputs.
    public static let dustSats: UInt64 = 294
    private static let scanGap = 5
    private static let scanCap = 40

    private let api: BtcEsploraClient

    public init(esploraURL: String, session: URLSession = .shared) throws {
        api = try BtcEsploraClient(baseURL: esploraURL, session: session)
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
    }

    public func newSeed() -> String { Bip39.newMnemonic(entropyBits: 128) }

    public func validateSeed(_ mnemonic: String) -> Bool { Bip39.validate(mnemonic) }

    public func deriveAddresses(seed: String, receiveCount: Int, changeCount: Int) throws -> AddressBook {
        let account = try Bip84.accountKey(mnemonic: seed)
        return AddressBook(
            receive: try (0..<receiveCount).map {
                Bip84.address(pubKey: try Bip84.key(account, change: 0, index: UInt32($0)).pubKey())
            },
            change: try (0..<changeCount).map {
                Bip84.address(pubKey: try Bip84.key(account, change: 1, index: UInt32($0)).pubKey())
            }
        )
    }

    public func validateAddress(_ address: String) -> Bool { BtcAddress.isValid(address) }

    public func scanUsed(seed: String) async throws -> (receive: Int, change: Int) {
        let account = try Bip84.accountKey(mnemonic: seed)
        func scanChain(_ chain: UInt32, minimum: Int) async throws -> Int {
            var lastUsed = -1
            var i = 0
            var gap = 0
            while gap < Self.scanGap && i < Self.scanCap {
                let addr = Bip84.address(pubKey: try Bip84.key(account, change: chain, index: UInt32(i)).pubKey())
                let info = try await api.addressInfo(addr)
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
        for addr in book.all() {
            for u in try await api.utxos(addr) {
                all &+= u.value
                if u.status.confirmed {
                    confirmed &+= u.value
                    outputs += 1
                }
            }
        }
        return WalletBalance(confirmed: confirmed, predicted: all, hours: nil, spendableOutputs: outputs)
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
                    fee: tx.fee
                )
                order.append(tx.txid)
            }
        }
        return sortedForDisplay(order.map { seen[$0]! })
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
        seed: String,
        book: AddressBook,
        toAddress: String,
        amount: UInt64,
        feeRate: Int?,
        sendMax: Bool
    ) async throws -> TxPlan {
        guard let destScript = BtcAddress.scriptPubKey(toAddress) else { throw WalletError.invalidAddress }
        let rate = UInt64(max(feeRate ?? 1, 1))

        let account = try Bip84.accountKey(mnemonic: seed)
        // address → (chain, index, hash160)
        var programOf = [String: (UInt32, UInt32, [UInt8])]()
        for (i, a) in book.receive.enumerated() {
            programOf[a] = (0, UInt32(i), Hashes.hash160(try Bip84.key(account, change: 0, index: UInt32(i)).pubKey()))
        }
        for (i, a) in book.change.enumerated() {
            programOf[a] = (1, UInt32(i), Hashes.hash160(try Bip84.key(account, change: 1, index: UInt32(i)).pubKey()))
        }

        // Confirmed outputs only — the design promise is that pending funds
        // cannot be respent from this wallet.
        var utxos = [(String, BtcEsploraClient.Utxo)]()
        for addr in book.all() {
            for u in try await api.utxos(addr) where u.status.confirmed { utxos.append((addr, u)) }
        }
        if utxos.isEmpty { throw WalletError.insufficientBalance }
        // Largest first; a stable sort, as Kotlin's sortByDescending is.
        utxos = utxos.enumerated().sorted {
            $0.element.1.value != $1.element.1.value ? $0.element.1.value > $1.element.1.value : $0.offset < $1.offset
        }.map(\.element)

        let changeScriptLen = 22 // P2WPKH change

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
            let vsize = BtcTxn.estimateVsize(inputCount: utxos.count, outputScripts: [destScript])
            let fee = rate &* UInt64(vsize)
            if total <= fee &+ Self.dustSats { throw WalletError.insufficientBalance }
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
                    changeIndex: -1
                ))
            )
        }

        if amount == 0 { throw WalletError.invalidAmount("enter an amount to send") }
        if amount < Self.dustSats { throw WalletError.invalidAmount("amount is below the dust limit") }

        var selected = [(String, BtcEsploraClient.Utxo)]()
        var inSum: UInt64 = 0
        var feeWithChange: UInt64 = 0
        let withChange = [destScript, [UInt8](repeating: 0, count: changeScriptLen)]
        for u in utxos {
            selected.append(u)
            inSum &+= u.1.value
            feeWithChange = rate &* UInt64(BtcTxn.estimateVsize(inputCount: selected.count, outputScripts: withChange))
            if inSum >= amount &+ feeWithChange { break }
        }
        if inSum < amount { throw WalletError.insufficientBalance }

        var change: UInt64 = inSum >= amount &+ feeWithChange ? inSum - amount - feeWithChange : 0
        var fee = feeWithChange
        var changeIndex = -1
        var outputs = [BtcTxn.Output(valueSats: amount, scriptPubKey: destScript)]

        if change < Self.dustSats {
            // No change output: whatever is left over joins the fee.
            fee = inSum - amount
            let feeNoChange = rate &* UInt64(BtcTxn.estimateVsize(inputCount: selected.count, outputScripts: [destScript]))
            if fee < feeNoChange { throw WalletError.insufficientBalance }
            change = 0
        } else {
            changeIndex = book.change.count
            let changeAddr = Bip84.address(pubKey: try Bip84.key(account, change: 1, index: UInt32(changeIndex)).pubKey())
            guard let changeScript = BtcAddress.scriptPubKey(changeAddr) else {
                throw WalletCoreError("derived change address failed to decode")
            }
            outputs.append(BtcTxn.Output(valueSats: change, scriptPubKey: changeScript))
        }

        let vsize = BtcTxn.estimateVsize(inputCount: selected.count, outputScripts: outputs.map(\.scriptPubKey))
        return TxPlan(
            toAddress: toAddress,
            amount: amount,
            fee: fee,
            changeAmount: change,
            hoursToRecipient: nil,
            hoursChange: nil,
            vsize: vsize,
            feeRate: Int(rate),
            payload: .btc(Payload(inputs: try selected.map(planInput), outputs: outputs, changeIndex: changeIndex))
        )
    }

    public func signTx(seed: String, plan: TxPlan) throws -> SignedTx {
        guard case .btc(let payload) = plan.payload else { throw WalletCoreError("not a Bitcoin plan") }
        let account = try Bip84.accountKey(mnemonic: seed)
        var txn = try BtcTxn(
            inputs: payload.inputs.map {
                BtcTxn.Input(txid: $0.txid, vout: $0.vout, valueSats: $0.value, pubKeyHash: $0.pubKeyHash)
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
