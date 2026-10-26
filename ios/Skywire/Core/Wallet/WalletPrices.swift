import Foundation

/// USD prices from coinpaprika, as skycoin-web shows them (Android: wallet/WalletPrices.kt). Off
/// unless the user turns them on: asking tells coinpaprika this phone's address and which coins it
/// looks at. Each price is kept for ten minutes, skycoin-web's interval.
@MainActor
enum WalletPrices {
    /// The UserDefaults key of the on switch, Android's preference key; off by default.
    nonisolated static let prefKey = "wallet_usd_prices"
    static let ttl: TimeInterval = 10 * 60

    private static var cache: [String: (price: Double, at: Date)] = [:]
    private static let session: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 15
        return URLSession(configuration: config)
    }()

    static var enabled: Bool { UserDefaults.standard.bool(forKey: prefKey) }

    static func usd(_ priceId: String) async throws -> Double {
        if let hit = cache[priceId], Date().timeIntervalSince(hit.at) < ttl { return hit.price }
        let url = URL(string: "https://api.coinpaprika.com/v1/tickers/\(priceId)?quotes=USD")!
        let (data, response) = try await session.data(from: url)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else { throw WalletStoreError("price service answered HTTP \(status)") }
        let price = try parse(data)
        cache[priceId] = (price, Date())
        return price
    }

    nonisolated static func parse(_ body: Data) throws -> Double {
        struct Ticker: Decodable {
            struct Quote: Decodable { let price: Double }
            let quotes: [String: Quote]
        }
        guard let price = try JSONDecoder().decode(Ticker.self, from: body).quotes["USD"]?.price else {
            throw WalletStoreError("price service sent no USD price")
        }
        return price
    }
}

extension CoinSpec {
    /// `units` of this coin in USD at `price`, as the user's locale writes money.
    func usdText(_ units: UInt64, price: Double) -> String {
        let formatter = NumberFormatter()
        formatter.numberStyle = .currency
        formatter.currencyCode = "USD"
        formatter.locale = .current
        return formatter.string(from: NSDecimalNumber(decimal: usdValue(units, price))) ?? ""
    }

    /// `units` in USD as a plain number for an input field, cents rounded down: "12.34".
    func usdPlain(_ units: UInt64, price: Double) -> String {
        let cents = Self.rounded(usdValue(units, price), 2)
        let formatter = NumberFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.minimumFractionDigits = 2
        formatter.maximumFractionDigits = 2
        formatter.usesGroupingSeparator = false
        return formatter.string(from: NSDecimalNumber(decimal: cents)) ?? ""
    }

    /// A USD amount typed by the user as this coin's amount at `price`, or nil when it is not a
    /// number. Rounded down to the decimals the coin is shown with, so never more than was asked.
    func coinTextFromUsd(_ usdText: String, price: Double) -> String? {
        guard price > 0, let usd = Decimal(string: usdText.trimmingCharacters(in: .whitespaces), locale: Locale(identifier: "en_US_POSIX")),
              usd > 0, let rate = Decimal(string: String(price), locale: Locale(identifier: "en_US_POSIX"))
        else { return nil }
        return NSDecimalNumber(decimal: Self.rounded(usd / rate, displayDecimals)).stringValue
    }

    private func usdValue(_ units: UInt64, _ price: Double) -> Decimal {
        // String(price) is the shortest form that reads back as the same double: Java's BigDecimal.valueOf.
        let rate = Decimal(string: String(price), locale: Locale(identifier: "en_US_POSIX")) ?? Decimal(price)
        var scale = Decimal(1)
        for _ in 0..<exponent { scale *= 10 }
        return Decimal(units) / scale * rate
    }

    private static func rounded(_ value: Decimal, _ places: Int) -> Decimal {
        var input = value
        var result = Decimal()
        NSDecimalRound(&result, &input, places, .down)
        return result
    }
}
