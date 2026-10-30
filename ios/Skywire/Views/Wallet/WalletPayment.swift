import Foundation
import WalletCore

/// A payment request as typed on Receive (Android: ui/wallet/WalletPayment.kt). `uri` is nil
/// while nothing valid is asked.
struct PaymentRequest: Equatable {
    let uri: String?
    let badAmount: Bool
    let badHours: Bool
}

/// The longest message a request carries, as skycoin-web allows.
let requestMessageMax = 100

/// What a scanned code puts into the send form, or why it puts nothing.
struct ScanFill: Equatable {
    let address: String?
    let amountText: String?
    let hoursText: String?
    var wrongToken = false
}

extension CoinSpec {
    func paymentRequest(address: String, amountText: String, hoursText: String, message: String) -> PaymentRequest {
        let typed = amountText.trimmingCharacters(in: .whitespaces)
        let units = typed.isEmpty ? nil : Amounts.parse(typed, exponent: exponent).flatMap { $0 > 0 ? $0 : nil }
        let typedHours = kind == .skyFiber ? hoursText.trimmingCharacters(in: .whitespaces) : ""
        let hours = typedHours.isEmpty ? nil : UInt64(typedHours).flatMap { $0 > 0 ? $0 : nil }
        let badAmount = !typed.isEmpty && units == nil
        let badHours = !typedHours.isEmpty && hours == nil
        // EIP-681 has no message field, so the Ethereum family carries only the amount.
        let trimmedNote = String(message.trimmingCharacters(in: .whitespaces).prefix(requestMessageMax))
        let note = trimmedNote.isEmpty || isEthFamily ? nil : trimmedNote
        if badAmount || badHours || (units == nil && hours == nil && note == nil) {
            return PaymentRequest(uri: nil, badAmount: badAmount, badHours: badHours)
        }
        let plain = units.map { Amounts.format($0, exponent: exponent, minDecimals: 0).replacingOccurrences(of: ",", with: "") }
        let uri: String
        switch kind {
        case .skyFiber:
            uri = PaymentUri.fiber(scheme: PaymentUri.schemeFor(name), address: address, amount: plain, hours: hours, message: note)
        case .btc:
            uri = PaymentUri.bitcoin(address: address, amount: plain, message: note)
        case .eth:
            // Wei, from gwei.
            uri = PaymentUri.ether(address: address, wei: units.map { BigUInt($0) * BigUInt.pow10(18 - exponent) })
        case .erc20:
            // A token's smallest unit, from the app's capped exponent.
            guard let contract else { return PaymentRequest(uri: nil, badAmount: false, badHours: false) }
            uri = PaymentUri.erc20(contract: contract, to: address,
                                   units: units.map { BigUInt($0) * BigUInt.pow10((tokenDecimals ?? exponent) - exponent) })
        }
        return PaymentRequest(uri: uri, badAmount: false, badHours: false)
    }

    /// Reads a scanned or pasted code for this coin. A token transfer for another contract fills
    /// nothing: its first address is the contract, and coins sent there are lost. An amount finer
    /// than this coin can carry is dropped rather than rounded.
    func readScan(_ raw: String) -> ScanFill {
        let p = PaymentUri.parse(raw)
        if let target = p.contract {
            let mine = kind == .erc20 && contract?.lowercased() == target.lowercased()
            if !mine { return ScanFill(address: nil, amountText: nil, hoursText: nil, wrongToken: true) }
            let amount = p.tokenUnits.flatMap { Self.decimal($0, shift: tokenDecimals ?? exponent) }
            return ScanFill(address: p.address, amountText: amount.flatMap(amountFor), hoursText: nil)
        }
        // An ether amount on a token's form would be read as tokens.
        let amount = kind == .erc20 && p.scheme == "ethereum" ? nil : p.amount
        return ScanFill(
            address: p.address,
            amountText: amount.flatMap(amountFor),
            hoursText: kind == .skyFiber ? p.hours.map(String.init) : nil
        )
    }

    /// `v` as a plain decimal this coin can carry, or nil when it is not positive or too fine.
    private func amountFor(_ v: Decimal) -> String? {
        guard v > 0 else { return nil }
        let plain = NSDecimalNumber(decimal: v).stringValue
        let scale = plain.firstIndex(of: ".").map { plain.distance(from: $0, to: plain.endIndex) - 1 } ?? 0
        return scale > exponent || plain.contains("e") || plain.contains("E") ? nil : plain
    }

    /// `units` moved `shift` places to the right of the point, exactly.
    private static func decimal(_ units: BigUInt, shift: Int) -> Decimal? {
        var digits = units.description
        if shift > 0 {
            if digits.count <= shift { digits = String(repeating: "0", count: shift - digits.count + 1) + digits }
            digits.insert(".", at: digits.index(digits.endIndex, offsetBy: -shift))
        }
        return Decimal(string: digits, locale: Locale(identifier: "en_US_POSIX"))
    }
}
