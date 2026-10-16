import Foundation

/// User-correctable problems building a transaction (Kotlin's WalletException).
/// The messages are Kotlin's, word for word.
public enum WalletError: Error, Equatable, LocalizedError {
    case invalidAddress
    case invalidAmount(String)
    case insufficientBalance
    case insufficientHours
    case noHoursToBurn
    case dustChange
    case nodeRejected(String)
    /// Token sends: the token balance is there, the gas money is not.
    case insufficientGas(String)

    public var errorDescription: String? {
        switch self {
        case .invalidAddress: "invalid destination address"
        case .invalidAmount(let m): m
        case .insufficientBalance: "balance is not sufficient"
        case .insufficientHours: "coin hours are not sufficient"
        case .noHoursToBurn: "outputs hold no coin hours yet — hours accrue over time"
        case .dustChange: "amount leaves change too small to spend"
        case .nodeRejected(let m): m
        case .insufficientGas(let m): m
        }
    }
}

/// A node or indexer answered with an error, or not in the shape expected:
/// what Kotlin raises as IOException. The message is the server's own words
/// where it gave any.
public struct NetworkError: Error, Equatable, LocalizedError {
    public let message: String
    public init(_ message: String) { self.message = message }
    public var errorDescription: String? { message }
}

/// A broken precondition: what Kotlin's require/check/error raise. Not
/// something a user can fix; it names the rule that failed.
public struct WalletCoreError: Error, Equatable, LocalizedError {
    public let message: String
    public init(_ message: String) { self.message = message }
    public var errorDescription: String? { message }
}

func require(_ condition: Bool, _ message: @autoclosure () -> String) throws {
    if !condition { throw WalletCoreError(message()) }
}
