import Foundation

// The market's payloads as skydex-client passes them through (skycoin
// src/skydex/protocol), and the trading calls (Android: api/SkydexModels.kt,
// api/SkydexApi.kt). Go leaves empty slices as null.

/// client.get_currencies: payment coins with an explorer, and the coins a seller may list.
public struct DexCurrencies: Decodable, Sendable, Equatable {
    public var currencies: [String] = []
    public var sellCoins: [String] = []
    public var marketName = ""

    enum CodingKeys: String, CodingKey { case currencies, sellCoins = "sell_coins", marketName = "market_name" }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        currencies = c.lenient(.currencies, [])
        sellCoins = c.lenient(.sellCoins, [])
        marketName = c.lenient(.marketName, "")
    }
}

public struct DexProduct: Decodable, Sendable, Equatable, Identifiable {
    public var id = "", sellerPubKey = "", sellCoin = ""
    public var amount = 0.0, price = 0.0
    public var paymentCurrency = "", createdAt = ""

    enum CodingKeys: String, CodingKey {
        case id, sellerPubKey = "seller_pubkey", sellCoin = "sell_coin", amount, price
        case paymentCurrency = "payment_currency", createdAt = "created_at"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.lenient(.id, "")
        sellerPubKey = c.lenient(.sellerPubKey, "")
        sellCoin = c.lenient(.sellCoin, "")
        amount = c.lenient(.amount, 0.0)
        price = c.lenient(.price, 0.0)
        paymentCurrency = c.lenient(.paymentCurrency, "")
        createdAt = c.lenient(.createdAt, "")
    }
}

/// An order as the market reports it. The last three fields are this phone's:
/// History keeps finished orders after the market purges them, tagged with the
/// market they came from, the record the web page kept.
public struct DexOrder: Codable, Sendable, Equatable {
    public var id = "", type = "", productId = "", sellCoin = ""
    public var amount = 0.0, price = 0.0
    public var paymentCurrency = ""
    public var expectedPaymentAmount = 0.0
    public var sellerWallet = "", status = "", expiresAt = "", createdAt = ""
    public var marketPk = "", marketName = ""
    public var savedAt: Int64 = 0

    enum CodingKeys: String, CodingKey {
        case id, type, productId = "product_id", sellCoin = "sell_coin", amount, price
        case paymentCurrency = "payment_currency", expectedPaymentAmount = "expected_payment_amount"
        case sellerWallet = "seller_wallet", status, expiresAt = "expires_at", createdAt = "created_at"
        case marketPk = "market_pk", marketName = "market_name", savedAt = "saved_at"
    }

    public init(id: String = "", type: String = "", status: String = "", price: Double = 0, marketPk: String = "", marketName: String = "", savedAt: Int64 = 0) {
        self.id = id
        self.type = type
        self.status = status
        self.price = price
        self.marketPk = marketPk
        self.marketName = marketName
        self.savedAt = savedAt
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.lenient(.id, "")
        type = c.lenient(.type, "")
        productId = c.lenient(.productId, "")
        sellCoin = c.lenient(.sellCoin, "")
        amount = c.lenient(.amount, 0.0)
        price = c.lenient(.price, 0.0)
        paymentCurrency = c.lenient(.paymentCurrency, "")
        expectedPaymentAmount = c.lenient(.expectedPaymentAmount, 0.0)
        sellerWallet = c.lenient(.sellerWallet, "")
        status = c.lenient(.status, "")
        expiresAt = c.lenient(.expiresAt, "")
        createdAt = c.lenient(.createdAt, "")
        marketPk = c.lenient(.marketPk, "")
        marketName = c.lenient(.marketName, "")
        savedAt = c.lenient(.savedAt, Int64(0))
    }
}

/// A seller's own listing: pending (awaiting the deposit), then confirmed.
public struct DexListing: Decodable, Sendable, Equatable, Identifiable {
    public var id = "", sellCoin = ""
    public var amount = 0.0, expectedAmount = 0.0, price = 0.0
    public var paymentCurrency = "", status = "", marketWallet = ""
    public var expiresAt = "", createdAt = "", confirmedAt = "", txHash = "", productStatus = ""
    public var cancelable = false

    enum CodingKeys: String, CodingKey {
        case id, sellCoin = "sell_coin", amount, expectedAmount = "expected_amount", price
        case paymentCurrency = "payment_currency", status, marketWallet = "market_wallet"
        case expiresAt = "expires_at", createdAt = "created_at", confirmedAt = "confirmed_at"
        case txHash = "tx_hash", productStatus = "product_status", cancelable
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = c.lenient(.id, "")
        sellCoin = c.lenient(.sellCoin, "")
        amount = c.lenient(.amount, 0.0)
        expectedAmount = c.lenient(.expectedAmount, 0.0)
        price = c.lenient(.price, 0.0)
        paymentCurrency = c.lenient(.paymentCurrency, "")
        status = c.lenient(.status, "")
        marketWallet = c.lenient(.marketWallet, "")
        expiresAt = c.lenient(.expiresAt, "")
        createdAt = c.lenient(.createdAt, "")
        confirmedAt = c.lenient(.confirmedAt, "")
        txHash = c.lenient(.txHash, "")
        productStatus = c.lenient(.productStatus, "")
        cancelable = c.lenient(.cancelable, false)
    }
}

public struct DexOrderStatus: Decodable, Sendable, Equatable {
    public var orderId = "", status = ""
    public var confirmations = 0, requiredConfirmations = 0
    public var paymentTxHash = "", paidAt = ""

    enum CodingKeys: String, CodingKey {
        case orderId = "order_id", status, confirmations, requiredConfirmations = "required_confirmations"
        case paymentTxHash = "payment_tx_hash", paidAt = "paid_at"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        orderId = c.lenient(.orderId, "")
        status = c.lenient(.status, "")
        confirmations = c.lenient(.confirmations, 0)
        requiredConfirmations = c.lenient(.requiredConfirmations, 0)
        paymentTxHash = c.lenient(.paymentTxHash, "")
        paidAt = c.lenient(.paidAt, "")
    }
}

/// client.create_listing: what to deposit, and where.
public struct DexCreatedListing: Decodable, Sendable, Equatable {
    public var listingId = "", sellCoin = ""
    public var amount = 0.0, commission = 0.0, expectedAmount = 0.0
    public var marketWallet = "", expiresAt = ""

    enum CodingKeys: String, CodingKey {
        case listingId = "listing_id", sellCoin = "sell_coin", amount, commission
        case expectedAmount = "expected_amount", marketWallet = "market_wallet", expiresAt = "expires_at"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        listingId = c.lenient(.listingId, "")
        sellCoin = c.lenient(.sellCoin, "")
        amount = c.lenient(.amount, 0.0)
        commission = c.lenient(.commission, 0.0)
        expectedAmount = c.lenient(.expectedAmount, 0.0)
        marketWallet = c.lenient(.marketWallet, "")
        expiresAt = c.lenient(.expiresAt, "")
    }
}

/// client.buy_product: the exact amount to pay the seller.
public struct DexPurchase: Decodable, Sendable, Equatable {
    public var orderId = "", sellCoin = ""
    public var amount = 0.0, price = 0.0
    public var paymentCurrency = ""
    public var expectedPaymentAmount = 0.0
    public var sellerWallet = "", expiresAt = ""

    enum CodingKeys: String, CodingKey {
        case orderId = "order_id", sellCoin = "sell_coin", amount, price, paymentCurrency = "payment_currency"
        case expectedPaymentAmount = "expected_payment_amount", sellerWallet = "seller_wallet", expiresAt = "expires_at"
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        orderId = c.lenient(.orderId, "")
        sellCoin = c.lenient(.sellCoin, "")
        amount = c.lenient(.amount, 0.0)
        price = c.lenient(.price, 0.0)
        paymentCurrency = c.lenient(.paymentCurrency, "")
        expectedPaymentAmount = c.lenient(.expectedPaymentAmount, 0.0)
        sellerWallet = c.lenient(.sellerWallet, "")
        expiresAt = c.lenient(.expiresAt, "")
    }
}

/// A refusal from the market, or from skydex-client: the text is theirs, shown as is.
public struct SkydexRefusal: LocalizedError, Sendable {
    public let message: String
    public let code: String
    public let status: Int

    public var errorDescription: String? { message }
}

extension SkydexClient {
    /// A round trip over dmsg to the market takes seconds on a relayed route.
    static let marketTimeout: TimeInterval = 45

    /// A GET, or a POST with `body`; a refusal comes back as SkydexRefusal in the market's words.
    private func market<T: Decodable>(_ path: String, _ body: [String: any Sendable]? = nil, as: T.Type) async throws -> T {
        let data = try body.map { try JSONSerialization.data(withJSONObject: $0) }
        let response = try await send(data == nil ? .get : .post, path, body: data, timeout: Self.marketTimeout)
        guard response.isSuccess else {
            let refusal = (try? JSONSerialization.jsonObject(with: response.body)) as? [String: Any]
            let error = refusal?["error"] as? String ?? ""
            throw SkydexRefusal(message: error, code: refusal?["code"] as? String ?? "", status: response.status)
        }
        let text = response.body.isEmpty ? Data("{}".utf8) : response.body
        return try JSONDecoder().decode(T.self, from: text)
    }

    private struct Products: Decodable {
        var products: [DexProduct]?
    }

    private struct Orders: Decodable {
        var orders: [DexOrder]?
    }

    private struct Listings: Decodable {
        var listings: [DexListing]?
    }

    private struct Message: Decodable {
        var message: String?
    }

    public func currencies() async throws -> DexCurrencies { try await market("/api/currencies", as: DexCurrencies.self) }

    public func products() async throws -> [DexProduct] { try await market("/api/products", as: Products.self).products ?? [] }

    public func orders() async throws -> [DexOrder] { try await market("/api/orders", as: Orders.self).orders ?? [] }

    public func listings() async throws -> [DexListing] { try await market("/api/listings/mine", as: Listings.self).listings ?? [] }

    public func orderStatus(_ orderId: String) async throws -> DexOrderStatus {
        try await market("/api/order-status", ["order_id": orderId], as: DexOrderStatus.self)
    }

    public func register(walletSky: String, wallets: [String: String]) async throws {
        _ = try await market("/api/register", ["wallet_sky": walletSky, "wallets": wallets], as: Message.self)
    }

    public func createListing(sellCoin: String, amount: Double, price: Double, paymentCurrency: String) async throws -> DexCreatedListing {
        try await market("/api/listings", ["sell_coin": sellCoin, "amount": amount, "price": price, "payment_currency": paymentCurrency], as: DexCreatedListing.self)
    }

    /// The market's confirmation, which may be empty.
    public func cancelListing(_ listingId: String) async throws -> String {
        try await market("/api/listings/cancel", ["listing_id": listingId], as: Message.self).message ?? ""
    }

    public func cancelOrder(_ orderId: String) async throws -> String {
        try await market("/api/orders/cancel", ["order_id": orderId], as: Message.self).message ?? ""
    }

    public func buy(_ productId: String) async throws -> DexPurchase {
        try await market("/api/buy", ["product_id": productId], as: DexPurchase.self)
    }
}

/// What the native SkyDEX screens do the way the web trading page did.
public enum DexFormat {
    /// A market amount as the page printed it: shortest decimal, never exponent form.
    public static func number(_ value: Double) -> String {
        guard value.isFinite, let decimal = Decimal(string: String(value), locale: Locale(identifier: "en_US_POSIX")) else { return "" }
        return NSDecimalNumber(decimal: decimal).stringValue
    }

    /// The link a wallet scans to fill in address and amount, or nil for a coin
    /// whose scheme is not known (QrButton.jsx buildUri). Up to 8 places.
    public static func paymentURI(address: String, amount: Double, coin: String) -> String? {
        let schemes = ["BTC": "bitcoin", "LTC": "litecoin", "DOGE": "dogecoin", "DASH": "dash", "BCH": "bitcoincash", "SKY": "skycoin"]
        guard let scheme = schemes[coin.uppercased()], !address.isEmpty, amount.isFinite, amount > 0 else { return nil }
        var plain = String(format: "%.8f", locale: Locale(identifier: "en_US_POSIX"), amount)
        while plain.hasSuffix("0") { plain.removeLast() }
        if plain.hasSuffix(".") { plain.removeLast() }
        return "\(scheme):\(address)?amount=\(plain)"
    }

    /// A decimal typed with either separator: decimal keyboards in es and fr offer a comma.
    public static func parseDecimal(_ text: String) -> Double? {
        let cleaned = text.trimmingCharacters(in: .whitespaces).replacingOccurrences(of: ",", with: ".")
        guard let value = Double(cleaned), value.isFinite else { return nil }
        return value
    }

    /// Order states that are over: settled, or ended without settling.
    public static let terminal: Set<String> = ["completed", "canceled", "cancelled", "expired"]
    /// Buy orders worth polling get_order_status for: still waiting on the chain.
    public static let inFlight: Set<String> = ["pending_payment", "paid", "confirmed"]

    /// Folds the market's finished orders into the saved history, tagged with
    /// the market (api.js mergeTradeHistory). Keyed by side and id: a self-trade
    /// is one order seen as both a buy and a sell.
    public static func mergeHistory(_ saved: [DexOrder], _ orders: [DexOrder], marketPk: String, marketName: String, now: Int64) -> [DexOrder] {
        var keys: [String] = []
        var byKey: [String: DexOrder] = [:]
        for o in saved {
            let key = o.type + ":" + o.id
            if byKey[key] == nil { keys.append(key) }
            byKey[key] = o
        }
        for o in orders where !o.id.isEmpty && terminal.contains(o.status) {
            let key = o.type + ":" + o.id
            let previous = byKey[key]
            var next = o
            next.marketPk = marketPk.isEmpty ? previous?.marketPk ?? "" : marketPk
            next.marketName = marketName.isEmpty ? previous?.marketName ?? "" : marketName
            next.savedAt = (previous?.savedAt ?? 0) > 0 ? previous!.savedAt : now
            if previous == nil { keys.append(key) }
            byKey[key] = next
        }
        return keys.compactMap { byKey[$0] }
    }

    /// A market the old page remembered (`exchange:markets`).
    public struct LegacyMarket: Equatable, Sendable {
        public let pk: String
        public let name: String
        public let ts: Int64
    }

    /// What the old trading page kept in localStorage (api.js keys).
    public struct Legacy: Equatable, Sendable {
        public var wallets: [String: String] = [:]
        public var markets: [LegacyMarket] = []
        public var history: [DexOrder] = []
    }

    public static func parseLegacy(_ storage: [String: String]) -> Legacy {
        func element(_ key: String) -> Any? {
            storage[key].flatMap { try? JSONSerialization.jsonObject(with: Data($0.utf8), options: .fragmentsAllowed) }
        }
        var out = Legacy()
        for (coin, value) in element("exchange:wallets") as? [String: Any] ?? [:] {
            let address = (value as? String)?.trimmingCharacters(in: .whitespaces) ?? ""
            if !address.isEmpty { out.wallets[coin] = address }
        }
        for item in element("exchange:markets") as? [Any] ?? [] {
            guard let o = item as? [String: Any], let pk = (o["pk"] as? String)?.trimmingCharacters(in: .whitespaces), !pk.isEmpty else { continue }
            out.markets.append(LegacyMarket(pk: pk, name: o["name"] as? String ?? "", ts: (o["ts"] as? NSNumber)?.int64Value ?? 0))
        }
        for item in element("exchange:history") as? [Any] ?? [] {
            guard let data = try? JSONSerialization.data(withJSONObject: item),
                  let order = try? JSONDecoder().decode(DexOrder.self, from: data), !order.id.isEmpty
            else { continue }
            out.history.append(order)
        }
        return out
    }
}
