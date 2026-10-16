import Foundation
import os

/// WalletCore for Ethereum: BIP 44 keys, EIP-1559 sends, a JSON-RPC node for
/// state and an etherscan-style indexer for history. One class covers the
/// native coin and any ERC-20 token — a token send is the same transaction
/// with the value moved into `transfer` call data and the gas still paid in
/// ETH.
///
/// Base units are chosen to fit the seam's 64-bit amounts, which wei does
/// not (2⁶⁴ wei ≈ 18.4 ETH): the native coin is carried in **gwei** and a
/// token in units of 10^-baseExponent, losing nothing anyone can spend
/// deliberately. Wei only exists inside this class.
public final class EthWalletCore: WalletCore {

    /// An ERC-20 the wallet holds: where it lives and how it counts.
    public struct Erc20Token: Sendable {
        public let contract: String
        public let decimals: Int
        /// The seam's exponent — the token's own, capped so amounts fit 64 bits.
        public var baseExponent: Int { min(decimals, EthWalletCore.maxBaseExponent) }
        /// Raw contract units per seam base unit.
        public var rawScale: BigUInt { BigUInt.pow10(decimals - baseExponent) }

        public init(contract: String, decimals: Int) {
            self.contract = contract
            self.decimals = decimals
        }
    }

    static let weiPerGwei = BigUInt(1_000_000_000)
    /// A plain EOA transfer, exactly.
    static let gasTransfer = BigUInt(21_000)
    /// Fallback when the node will not estimate a token transfer.
    static let gasTokenTransfer = BigUInt(100_000)
    /// What the Max prefill holds back for gas on the native coin: 21 000
    /// gas at 100 gwei. The real remainder is computed with live prices when
    /// the max send is actually planned.
    public static let gasHeadroomGwei: UInt64 = 2_100_000
    /// Cap that keeps any token amount inside the seam's 64 bits.
    static let maxBaseExponent = 9
    private static let scanGap = 3
    private static let scanCap = 10

    private let api: EthRpcClient
    private let token: Erc20Token?
    /// Read once per core, first use — it never changes under a URL.
    private let cachedChainId = OSAllocatedUnfairLock<BigUInt?>(initialState: nil)

    public init(rpcURL: String, indexerURL: String?, session: URLSession = .shared, token: Erc20Token? = nil) throws {
        api = try EthRpcClient(rpcURL: rpcURL, indexerURL: indexerURL, session: session)
        self.token = token
    }

    struct Payload: Sendable {
        let chainId: BigUInt
        let nonce: BigUInt
        let maxPriorityFeePerGas: BigUInt
        let maxFeePerGas: BigUInt
        let gasLimit: BigUInt
        let to: [UInt8]
        let valueWei: BigUInt
        let data: [UInt8]
        /// Which receive index funds and signs this send.
        let fromIndex: Int
    }

    public func newSeed() -> String { Bip39.newMnemonic(entropyBits: 128) }

    public func validateSeed(_ mnemonic: String) -> Bool { Bip39.validate(mnemonic) }

    public func deriveAddresses(seed: String, receiveCount: Int, changeCount: Int) throws -> AddressBook {
        let account = try EthCrypto.accountKey(mnemonic: seed)
        return AddressBook(
            receive: try (0..<receiveCount).map {
                try EthCrypto.address(pubCompressed: EthCrypto.key(account, index: UInt32($0)).pubKey())
            },
            // An account chain has no change side: sends spend from one
            // address and the remainder simply stays on it.
            change: []
        )
    }

    public func validateAddress(_ address: String) -> Bool { EthCrypto.isValidAddress(address) }

    public func scanUsed(seed: String) async throws -> (receive: Int, change: Int) {
        let account = try EthCrypto.accountKey(mnemonic: seed)
        var lastUsed = -1
        var i = 0
        var gap = 0
        while gap < Self.scanGap && i < Self.scanCap {
            let addr = try EthCrypto.address(pubCompressed: EthCrypto.key(account, index: UInt32(i)).pubKey())
            var used = !(try await api.nonce(addr)).isZero
            if !used { used = !(try await api.balanceWei(addr)).isZero }
            if used {
                lastUsed = i
                gap = 0
            } else {
                gap += 1
            }
            i += 1
        }
        return (max(lastUsed + 1, 1), 0)
    }

    public func balance(_ book: AddressBook) async throws -> WalletBalance {
        var total = BigUInt.zero
        var funded = 0
        for addr in book.receive {
            let value = token == nil ? try await api.balanceWei(addr) : try await tokenBalanceRaw(addr)
            if !value.isZero { funded += 1 }
            total = total + value
        }
        let base = toBase(total)
        return WalletBalance(
            confirmed: base,
            // No mempool view over plain RPC; the 30-second refresh is what
            // moves this number, exactly like a block would.
            predicted: base,
            hours: nil,
            spendableOutputs: funded
        )
    }

    public func history(_ book: AddressBook) async throws -> [TxRecord] {
        let ours = Set(book.receive.map { $0.lowercased() })
        var seen = [String: TxRecord]()
        var order = [String]()
        for addr in book.receive {
            let rows = if let token {
                try await api.tokenTransfers(addr, contract: token.contract)
            } else {
                try await api.transactions(addr)
            }
            for row in rows where seen[row.hash] == nil {
                // A failed send moved no value — only its gas burned, which
                // is not this list's story to tell.
                if row.isError != "0" { continue }
                let from = row.from.lowercased()
                let to = row.to.lowercased()
                let incoming = !ours.contains(from)
                let confirmations = Int64(row.confirmations) ?? 0
                let feeWei = (BigUInt(decimal: row.gasUsed) ?? .zero) * (BigUInt(decimal: row.gasPrice) ?? .zero)
                let party: String? = if ours.contains(from) && ours.contains(to) {
                    nil
                } else if incoming {
                    row.from
                } else {
                    row.to
                }
                seen[row.hash] = TxRecord(
                    txid: row.hash,
                    incoming: incoming,
                    amount: toBase(BigUInt(decimal: row.value) ?? .zero),
                    party: party,
                    timestamp: Int64(row.timestamp) ?? 0,
                    confirmed: confirmations > 0,
                    confirmations: confirmations,
                    // Always the ETH gas, in gwei — a token has no fee of its
                    // own, and the UI labels this line ETH on both cores.
                    fee: feeWei.isZero ? nil : Self.weiToGwei(feeWei)
                )
                order.append(row.hash)
            }
        }
        return sortedForDisplay(order.map { seen[$0]! })
    }

    /// EIP-1559 prices itself; there is no knob worth a card of presets.
    public func feePresets() async throws -> FeePresets? { nil }

    /// The Max prefill. Gas prices are not knowable here (this cannot
    /// suspend), so the native coin holds back a generous fixed headroom;
    /// sendMax in buildTx then computes the real remainder with live prices.
    /// A token spends no ETH from its own balance, so Max is all of it.
    public func estimateMax(balance: WalletBalance, feeRate: Int?) -> UInt64 {
        if token != nil { return balance.confirmed }
        return balance.confirmed > Self.gasHeadroomGwei ? balance.confirmed - Self.gasHeadroomGwei : 0
    }

    public func buildTx(
        seed: String,
        book: AddressBook,
        toAddress: String,
        amount: UInt64,
        feeRate: Int?,
        sendMax: Bool
    ) async throws -> TxPlan {
        let trimmedTo = toAddress.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let dest = EthCrypto.parseAddress(trimmedTo) else { throw WalletError.invalidAddress }
        if !sendMax && amount == 0 { throw WalletError.invalidAmount("enter an amount to send") }

        // The funding account: the address holding the most of what is being
        // sent. An account chain does not sweep across addresses the way
        // UTXOs do — one address signs, and its balance is the ceiling.
        var best: (index: Int, address: String, balance: BigUInt)?
        for (i, addr) in book.receive.enumerated() {
            let value = token == nil ? try await api.balanceWei(addr) : try await tokenBalanceRaw(addr)
            if best == nil || value > best!.balance { best = (i, addr, value) }
        }
        guard let best else { throw WalletError.insufficientBalance }
        let (fromIndex, fromAddr, fromBalance) = best

        let id = try await chainId()
        let priority = await api.maxPriorityFeePerGas()
        // Twice the current base fee rides out the worst-case climb while
        // the send waits; anything unspent is never charged.
        let maxFee = try await api.baseFeePerGas() * BigUInt(2) + priority
        let nonce = try await api.nonce(fromAddr)

        guard let token else {
            var valueWei = BigUInt(amount) * Self.weiPerGwei
            var gas = Self.padGas(await api.estimateGas(from: fromAddr, to: toAddress, valueWei: valueWei, data: nil))
                ?? Self.gasTransfer
            var feeWei = gas * maxFee
            if sendMax {
                if fromBalance <= feeWei { throw WalletError.insufficientBalance }
                // Floor to a whole gwei so the plan's amount and the wire's
                // value are the same number.
                valueWei = BigUInt(Self.weiToGwei(fromBalance - feeWei)) * Self.weiPerGwei
                gas = Self.padGas(await api.estimateGas(from: fromAddr, to: toAddress, valueWei: valueWei, data: nil))
                    ?? gas
                feeWei = gas * maxFee
            }
            if fromBalance < valueWei + feeWei { throw WalletError.insufficientBalance }
            return TxPlan(
                toAddress: trimmedTo,
                amount: Self.weiToGwei(valueWei),
                fee: Self.weiToGweiCeil(feeWei),
                changeAmount: 0,
                hoursToRecipient: nil,
                hoursChange: nil,
                vsize: Int(truncatingIfNeeded: gas.clampedUInt64),
                feeRate: Int(truncatingIfNeeded: Self.weiToGweiCeil(maxFee)),
                payload: .eth(Payload(
                    chainId: id, nonce: nonce, maxPriorityFeePerGas: priority, maxFeePerGas: maxFee,
                    gasLimit: gas, to: dest, valueWei: valueWei, data: [], fromIndex: fromIndex
                ))
            )
        }

        let amountRaw = sendMax ? fromBalance : BigUInt(amount) * token.rawScale
        if amountRaw.isZero { throw WalletError.invalidAmount("enter an amount to send") }
        if fromBalance < amountRaw { throw WalletError.insufficientBalance }
        let data = try EthTxn.erc20Transfer(to: dest, amount: amountRaw)
        let gas = Self.padGas(await api.estimateGas(from: fromAddr, to: token.contract, valueWei: .zero, data: data))
            ?? Self.gasTokenTransfer
        let feeWei = gas * maxFee
        let ethWei = try await api.balanceWei(fromAddr)
        if ethWei < feeWei {
            throw WalletError.insufficientGas("the sending address holds too little ETH for the network fee")
        }
        guard let contractBytes = EthCrypto.parseAddress(token.contract) else {
            throw WalletCoreError("token registered with an invalid contract address")
        }
        return TxPlan(
            toAddress: trimmedTo,
            amount: (amountRaw / token.rawScale).clampedUInt64,
            fee: Self.weiToGweiCeil(feeWei),
            changeAmount: 0,
            hoursToRecipient: nil,
            hoursChange: nil,
            vsize: Int(truncatingIfNeeded: gas.clampedUInt64),
            feeRate: Int(truncatingIfNeeded: Self.weiToGweiCeil(maxFee)),
            payload: .eth(Payload(
                chainId: id, nonce: nonce, maxPriorityFeePerGas: priority, maxFeePerGas: maxFee,
                gasLimit: gas, to: contractBytes, valueWei: .zero, data: data, fromIndex: fromIndex
            ))
        )
    }

    public func signTx(seed: String, plan: TxPlan) throws -> SignedTx {
        guard case .eth(let payload) = plan.payload else { throw WalletCoreError("not an Ethereum plan") }
        let key = try EthCrypto.key(EthCrypto.accountKey(mnemonic: seed), index: UInt32(payload.fromIndex))
        let signed = try EthTxn(
            chainId: payload.chainId,
            nonce: payload.nonce,
            maxPriorityFeePerGas: payload.maxPriorityFeePerGas,
            maxFeePerGas: payload.maxFeePerGas,
            gasLimit: payload.gasLimit,
            to: payload.to,
            value: payload.valueWei,
            data: payload.data
        ).signed(sec: key.key)
        return SignedTx(rawHex: signed.raw.hex, txid: "0x" + signed.hash.hex)
    }

    public func broadcast(_ tx: SignedTx) async throws -> String {
        guard let raw = [UInt8](hex: tx.rawHex) else { throw WalletCoreError("signed transaction is not hex") }
        return try await api.sendRaw(raw)
    }

    // MARK: units

    private func chainId() async throws -> BigUInt {
        if let id = cachedChainId.withLock({ $0 }) { return id }
        let id = try await api.chainId()
        cachedChainId.withLock { $0 = id }
        return id
    }

    private func tokenBalanceRaw(_ address: String) async throws -> BigUInt {
        guard let token, let owner = EthCrypto.parseAddress(address) else { return .zero }
        let result = try await api.ethCall(to: token.contract, data: EthTxn.erc20BalanceOf(owner: owner))
        return result.isEmpty ? .zero : BigUInt(bigEndian: result)
    }

    /// Wei (native) or raw contract units (token) → the seam's base units.
    private func toBase(_ value: BigUInt) -> UInt64 {
        if let token { return (value / token.rawScale).clampedUInt64 }
        return Self.weiToGwei(value)
    }

    static func weiToGwei(_ wei: BigUInt) -> UInt64 { (wei / weiPerGwei).clampedUInt64 }

    static func weiToGweiCeil(_ wei: BigUInt) -> UInt64 {
        ((wei + weiPerGwei - BigUInt(1)) / weiPerGwei).clampedUInt64
    }

    /// A fifth over the node's estimate: first sends to fresh slots cost more.
    static func padGas(_ estimate: BigUInt?) -> BigUInt? {
        estimate.map { $0 * BigUInt(12) / BigUInt(10) }
    }
}
