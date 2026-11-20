import Foundation

/// USD prices from coinpaprika, as skycoin-web shows them (Android: wallet/WalletPrices.kt). Off
/// unless the user turns them on: asking tells coinpaprika this phone's address and which coins it
/// looks at. A price is asked for every ten minutes, and the last is stored.
@MainActor
enum WalletPrices {
    /// The UserDefaults key of the on switch, Android's preference key; off by default.
    nonisolated static let prefKey = "wallet_usd_prices"
    /// The UserDefaults key of the last quotes, a dictionary by coin; Android's file of the same name.
    nonisolated static let storeKey = "wallet_prices"
    static let ttl: TimeInterval = 10 * 60
    /// A quote older than this is shown with its time and converts no amounts.
    static let fresh: TimeInterval = 60 * 60

    /// One coin in US dollars at the time the service answered.
    struct Quote: Equatable {
        let usd: Double
        let at: Date
    }

    private static var cache: [String: Quote] = [:]
    private static let session: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 15
        return URLSession(configuration: config)
    }()

    static var enabled: Bool { UserDefaults.standard.bool(forKey: prefKey) }

    /// The last price known for `priceId`, from this run or an earlier one.
    static func last(_ priceId: String, defaults: UserDefaults = .standard) -> Quote? {
        if let hit = cache[priceId] { return hit }
        guard let stored = (defaults.dictionary(forKey: storeKey)?[priceId] as? String).flatMap(decode) else { return nil }
        cache[priceId] = stored
        return stored
    }

    /// Keeps `quote` as the last price of `priceId`, across restarts too.
    static func keep(_ quote: Quote, for priceId: String, defaults: UserDefaults = .standard) {
        cache[priceId] = quote
        var stored = defaults.dictionary(forKey: storeKey) ?? [:]
        stored[priceId] = encode(quote)
        defaults.set(stored, forKey: storeKey)
    }

    /// Forgets the quotes held in memory, as a restart does.
    static func dropCache() { cache = [:] }

    static func usd(_ priceId: String) async throws -> Double {
        if let hit = last(priceId), Date().timeIntervalSince(hit.at) < ttl { return hit.usd }
        let url = URL(string: "https://api.coinpaprika.com/v1/tickers/\(priceId)?quotes=USD")!
        let (data, response) = try await session.data(from: url)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else { throw WalletStoreError("price service answered HTTP \(status)") }
        let price = try parse(data)
        keep(Quote(usd: price, at: Date()), for: priceId)
        return price
    }

    /// The stored form of a quote, Android's: the price, a bar, and the time in epoch ms.
    nonisolated static func encode(_ quote: Quote) -> String {
        "\(quote.usd)|\(Int64((quote.at.timeIntervalSince1970 * 1000).rounded()))"
    }

    nonisolated static func decode(_ stored: String) -> Quote? {
        let parts = stored.split(separator: "|", omittingEmptySubsequences: false)
        guard parts.count == 2, let usd = Double(parts[0]), usd > 0, usd.isFinite, let ms = Int64(parts[1]), ms > 0 else { return nil }
        return Quote(usd: usd, at: Date(timeIntervalSince1970: TimeInterval(ms) / 1000))
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
