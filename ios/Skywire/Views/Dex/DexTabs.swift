import CoreClient
import SwiftUI

/// The open tab (Android: ui/dex/DexTabs.kt), with the banners first so they scroll away with it.
struct DexTabContent: View {
    @ObservedObject var model: DexModel
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 12) {
                banners
                switch model.tab {
                case .market: DexMarketTab(model: model)
                case .orders: DexOrdersTab(model: model)
                case .listings: DexListingsTab(model: model)
                case .history: DexHistoryTab(model: model)
                case .settings: DexSettingsTab(model: model)
                }
            }
            .frame(maxWidth: 640)
            .padding(EdgeInsets(top: 12, leading: 16, bottom: 32, trailing: 16))
            .frame(maxWidth: .infinity)
        }
        .scrollDismissesKeyboard(.interactively)
    }

    /// Onboarding, and a market that has nothing to sell yet (App.jsx).
    @ViewBuilder private var banners: some View {
        if !model.local.registered && model.tab != .settings {
            DexBanner(text: L10n.text("dex_banner_register_title") + " " + L10n.text("dex_banner_register_body"), warn: false,
                      action: L10n.text("dex_open_settings")) { model.selectTab(.settings) }
        }
        if model.ready == false && (model.tab == .market || model.tab == .listings) {
            DexBanner(text: L10n.text("dex_banner_not_ready"), warn: true)
        }
    }
}

/// The action and poll errors, the action's first: it is what the user just did.
private struct DexErrors: View {
    @ObservedObject var model: DexModel

    var body: some View {
        if let action = model.actionError { DexErrorLine(text: action) }
        if let trade = model.tradeError, trade != model.actionError { DexErrorLine(text: trade) }
    }
}

private struct DexLoading: View {
    var body: some View {
        MaterialSpinner(size: 24, stroke: 2).frame(maxWidth: .infinity).padding(24)
    }
}

// MARK: Market

private struct DexMarketTab: View {
    @ObservedObject var model: DexModel
    @State private var formOpen = false

    var body: some View {
        let canSell = !model.sellCoins.isEmpty
        HStack {
            Text("dex_available_products").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
            Spacer()
            if formOpen {
                Button { formOpen = false } label: { Text("cancel") }.buttonStyle(.outlined)
            } else {
                Button {
                    if model.local.registered { formOpen = true } else { model.selectTab(.settings) }
                } label: {
                    HStack(spacing: 4) {
                        MaterialIcon(MI.roundedAdd, size: 18)
                        Text("dex_new_sell_order")
                    }
                }
                .buttonStyle(.filled)
                .disabled(!canSell)
                .accessibilityIdentifier("dex-new-sell")
            }
        }
        DexErrors(model: model)
        if let deposit = model.deposit { DepositCard(model: model, d: deposit) }
        if let payment = model.payment { PaymentCard(model: model, p: payment) }
        if formOpen && canSell { NewOrderForm(model: model) { formOpen = false } }
        if let products = model.products {
            if products.isEmpty {
                DexHint(text: model.ready == false ? L10n.text("dex_no_coins_configured") : L10n.text("dex_no_products"))
            } else {
                ForEach(products) { product in
                    DexCard {
                        HStack {
                            VStack(alignment: .leading, spacing: 0) {
                                Text(verbatim: "\(DexFormat.number(product.amount)) \(product.sellCoin)").skyText(.titleMedium).foregroundStyle(Color.skyOnSurface)
                                Text(verbatim: "\(DexFormat.number(product.price)) \(product.paymentCurrency)").skyText(.bodyLarge).foregroundStyle(Color.skyPrimary)
                                DexHint(text: L10n.format("dex_seller", DexText.shortKey(product.sellerPubKey))).padding(.top, 2)
                            }
                            Spacer(minLength: 8)
                            Button { model.buy(product) } label: { Text("dex_buy") }
                                .buttonStyle(.filled)
                                .disabled(model.working)
                                .accessibilityIdentifier("dex-buy-\(product.id)")
                        }
                    }
                }
            }
        } else {
            DexLoading()
        }
    }
}

/// Where to send the deposit, kept on screen until dismissed (Market.jsx).
private struct DepositCard: View {
    @ObservedObject var model: DexModel
    let d: DexCreatedListing

    var body: some View {
        InstructionCard(title: L10n.text("dex_fund_title"), dismiss: { model.dismissDeposit() }) {
            let amount = DexFormat.number(d.expectedAmount)
            let base = DexFormat.number(d.amount)
            let fee = DexFormat.number(d.commission)
            Text(verbatim: d.expiresAt.isEmpty
                ? L10n.format("dex_fund_body", amount, d.sellCoin, base, fee)
                : L10n.format("dex_fund_body_until", amount, d.sellCoin, base, fee, DexText.time(d.expiresAt)))
                .skyText(.bodyMedium).foregroundStyle(Color.skyOnSurface)
            AddressBlock(model: model, address: d.marketWallet, amount: d.expectedAmount, coin: d.sellCoin,
                         qrTitle: L10n.text("dex_fund_title"), qrHint: L10n.text("dex_hint_escrow"))
            DexHint(text: L10n.text("dex_fund_footer"))
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("dex-deposit")
    }
}

/// What to pay and to whom, kept on screen until dismissed (Market.jsx).
private struct PaymentCard: View {
    @ObservedObject var model: DexModel
    let p: DexPurchase

    var body: some View {
        InstructionCard(title: L10n.text("dex_pay_title"), dismiss: { model.dismissPayment() }) {
            let amount = DexFormat.number(p.amount)
            let pay = DexFormat.number(p.expectedPaymentAmount)
            Text(verbatim: p.expiresAt.isEmpty
                ? L10n.format("dex_pay_body", amount, p.sellCoin, pay, p.paymentCurrency)
                : L10n.format("dex_pay_body_until", amount, p.sellCoin, pay, p.paymentCurrency, DexText.time(p.expiresAt)))
                .skyText(.bodyMedium).foregroundStyle(Color.skyOnSurface)
            AddressBlock(model: model, address: p.sellerWallet, amount: p.expectedPaymentAmount, coin: p.paymentCurrency,
                         qrTitle: L10n.text("dex_pay_seller"), qrHint: L10n.text("dex_hint_exact"))
            DexHint(text: L10n.format("dex_pay_footer", p.sellCoin))
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("dex-payment")
    }
}

private struct InstructionCard<Content: View>: View {
    let title: String
    let dismiss: () -> Void
    @ViewBuilder let content: Content

    var body: some View {
        DexCard {
            HStack {
                Text(verbatim: title).skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
                Spacer(minLength: 0)
                Button(action: dismiss) { MaterialIcon(MI.roundedClose).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 40, height: 40) }
                    .accessibilityLabel(Text("dex_close"))
            }
            VStack(alignment: .leading, spacing: 10) { content }
        }
    }
}

/// You sell / You get, as the page laid the trade out.
private struct NewOrderForm: View {
    @ObservedObject var model: DexModel
    let onDone: () -> Void
    @State private var amount = ""
    @State private var price = ""
    @State private var sellCoin = ""
    @State private var payCoin = ""

    /// You cannot be paid in the coin you sell; any other fibercoin is fine.
    private var payOptions: [String] {
        var seen = Set<String>()
        return (model.currencies + model.sellCoins.filter { $0 != sellCoin }).filter { seen.insert($0).inserted }
    }

    var body: some View {
        let amountValue = DexFormat.parseDecimal(amount)
        let priceValue = DexFormat.parseDecimal(price)
        let complete = (amountValue ?? 0) > 0 && (priceValue ?? 0) > 0 && !sellCoin.isEmpty && !payCoin.isEmpty
        DexCard {
            Text("dex_new_sell_order").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
            TradeLeg(label: L10n.text("dex_you_sell"), value: $amount, coin: $sellCoin, coins: model.sellCoins, identifier: "dex-sell-amount").padding(.top, 8)
            MaterialIcon(MI.outlinedArrowDownward).foregroundStyle(Color.skyOnSurfaceVariant).frame(maxWidth: .infinity)
            TradeLeg(label: L10n.text("dex_you_get"), value: $price, coin: $payCoin, coins: payOptions, identifier: "dex-sell-price")
            DexHint(text: complete
                ? L10n.format("dex_trade_summary", DexFormat.number(amountValue ?? 0), sellCoin, DexFormat.number(priceValue ?? 0), payCoin)
                : L10n.text("dex_trade_hint")).padding(.top, 8)
            Button {
                guard let amountValue, let priceValue else { return }
                model.createListing(sellCoin: sellCoin, amount: amountValue, price: priceValue, paymentCurrency: payCoin) {
                    // Market.jsx clears the amounts once the listing exists.
                    amount = ""
                    price = ""
                    onDone()
                }
            } label: { Text("dex_create_sell_order").frame(maxWidth: .infinity) }
            .buttonStyle(.filled)
            .disabled(!complete || model.working)
            .accessibilityIdentifier("dex-create-sell")
            .padding(.top, 8)
        }
        .onAppear { fill() }
        .onChange(of: model.sellCoins) { _ in fill() }
        .onChange(of: sellCoin) { _ in fill() }
    }

    private func fill() {
        if !model.sellCoins.contains(sellCoin) { sellCoin = model.sellCoins.first ?? "" }
        if !payOptions.contains(payCoin) { payCoin = payOptions.first ?? "" }
    }
}

private struct TradeLeg: View {
    let label: String
    @Binding var value: String
    @Binding var coin: String
    let coins: [String]
    let identifier: String

    var body: some View {
        Text(verbatim: label).skyText(.labelMedium).foregroundStyle(Color.skyOnSurfaceVariant)
        HStack(spacing: 8) {
            SkyOutlinedTextField(placeholder: LocalizedStringKey("0.00"), text: Binding(
                get: { value },
                set: { value = $0.filter { $0.isNumber || $0 == "." || $0 == "," } }
            ), keyboard: .decimalPad, notch: .skySurfaceVariant, identifier: identifier)
            Menu {
                ForEach(coins, id: \.self) { c in
                    Button { coin = c } label: { Text(verbatim: c) }
                }
            } label: {
                HStack(spacing: 4) {
                    Text(verbatim: coin.isEmpty ? "—" : coin).skyText(.bodyLarge).foregroundStyle(Color.skyOnSurface)
                    MaterialIcon(MI.filledArrowDropDown, size: 20).foregroundStyle(Color.skyOnSurfaceVariant)
                }
                .frame(width: 112, height: 56)
                .overlay(RoundedRectangle(cornerRadius: SkyRadius.extraSmall).stroke(Color.skyOutline, lineWidth: 1))
            }
            .disabled(coins.isEmpty)
        }
    }
}

// MARK: My Orders

private struct DexOrdersTab: View {
    @ObservedObject var model: DexModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        DexErrors(model: model)
        if let orders = model.orders {
            if orders.isEmpty {
                DexHint(text: L10n.text("dex_no_orders"))
            } else {
                ForEach(orders, id: \.historyKey) { order in
                    OrderCard(model: model, order: order) {
                        dialogs.confirm(L10n.text("dex_cancel_buy_confirm"), confirm: L10n.text("dex_cancel_buy"), cancel: L10n.text("dex_keep")) {
                            model.cancelOrder(order)
                        }
                    }
                }
            }
        } else {
            DexLoading()
        }
    }
}

private struct OrderCard: View {
    @ObservedObject var model: DexModel
    let order: DexOrder
    let onCancel: () -> Void

    var body: some View {
        let live = model.statuses[order.id]
        let status = live.flatMap { $0.status.isEmpty ? nil : $0.status } ?? order.status
        DexCard {
            HStack(spacing: 6) {
                TypeBadge(type: order.type)
                StatusBadge(status: status)
                Spacer(minLength: 0)
                Text(verbatim: DexText.shortId(order.id)).skyText(.labelSmall, mono: true).foregroundStyle(Color.skyOnSurfaceVariant)
            }
            Text(verbatim: "\(DexFormat.number(order.amount)) \(order.sellCoin)").skyText(.titleMedium).foregroundStyle(Color.skyOnSurface).padding(.top, 8)
            Text(verbatim: "\(DexFormat.number(order.price)) \(order.paymentCurrency)").skyText(.bodyMedium).foregroundStyle(Color.skyPrimary)
            if order.type == "buy" && status == "pending_payment" && !order.sellerWallet.isEmpty {
                Text(verbatim: L10n.format("dex_pay_exactly", DexFormat.number(order.expectedPaymentAmount), order.paymentCurrency))
                    .skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyOnSurface).padding(.top, 10)
                AddressBlock(model: model, address: order.sellerWallet, amount: order.expectedPaymentAmount, coin: order.paymentCurrency,
                             qrTitle: L10n.text("dex_pay_seller"), qrHint: L10n.text("dex_hint_exact"))
            }
            if order.type == "buy" {
                Group {
                    if status == "completed" {
                        Text("dex_delivered").skyText(.bodySmall).foregroundStyle(Color.dexGood)
                    } else if status == "pending_payment" {
                        DexHint(text: L10n.text("dex_awaiting_payment"))
                    } else if let live, DexFormat.inFlight.contains(status) {
                        ConfirmationsBar(confirmations: live.confirmations, required: live.requiredConfirmations)
                    }
                }
                .padding(.top, 10)
            }
            VStack(alignment: .leading, spacing: 0) {
                if let tx = live?.paymentTxHash, !tx.isEmpty { DexField(label: L10n.text("dex_payment_tx"), value: DexText.shortId(tx), mono: true) }
                if !order.createdAt.isEmpty { DexField(label: L10n.text("dex_created"), value: DexText.dateTime(order.createdAt)) }
            }
            .padding(.top, 8)
            if order.type == "buy" && status == "pending_payment" {
                let busy = model.busyId == order.id
                Button(action: onCancel) {
                    Text(busy ? L10n.key("dex_canceling") : L10n.key("dex_cancel_buy")).foregroundStyle(Color.skyError)
                }
                .buttonStyle(.outlined)
                .disabled(busy)
                .padding(.top, 8)
            }
        }
    }
}

// MARK: My Listings

private struct DexListingsTab: View {
    @ObservedObject var model: DexModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        DexErrors(model: model)
        if let listings = model.listings {
            if listings.isEmpty {
                DexHint(text: L10n.text("dex_no_listings"))
            } else {
                ForEach(listings) { l in
                    ListingCard(model: model, l: l) {
                        let pending = l.status == "pending"
                        let text = pending ? L10n.text("dex_cancel_listing_confirm") : L10n.format("dex_cancel_offer_confirm", l.sellCoin.isEmpty ? DexModel.sky : l.sellCoin)
                        dialogs.confirm(text, confirm: L10n.text("dex_cancel_offer"), cancel: L10n.text("dex_keep")) { model.cancelListing(l) }
                    }
                }
                if listings.contains(where: { $0.status == "pending" }) {
                    DexBanner(text: L10n.text("dex_listings_pending_note"), warn: false)
                }
            }
        } else {
            DexLoading()
        }
    }
}

private struct ListingCard: View {
    @ObservedObject var model: DexModel
    let l: DexListing
    let onCancel: () -> Void

    var body: some View {
        let pending = l.status == "pending"
        DexCard {
            HStack {
                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: "\(DexFormat.number(l.amount)) \(l.sellCoin)").skyText(.titleMedium).foregroundStyle(Color.skyOnSurface)
                    Text(verbatim: "\(DexFormat.number(l.price)) \(l.paymentCurrency)").skyText(.bodyMedium).foregroundStyle(Color.skyPrimary)
                }
                Spacer(minLength: 0)
                Text(verbatim: DexText.shortId(l.id)).skyText(.labelSmall, mono: true).foregroundStyle(Color.skyOnSurfaceVariant)
            }
            LifecycleChips(status: l.status).padding(.top, 8)
            ProductStateBadge(listing: l).padding(.top, 4)
            VStack(alignment: .leading, spacing: 6) {
                if pending {
                    let fee = l.expectedAmount - l.amount
                    let extra = fee > 0 ? " (" + L10n.format("dex_amount_plus_fee", DexFormat.number(l.amount), String(format: "%.3f", locale: Locale(identifier: "en_US_POSIX"), fee)) + ")" : ""
                    DexField(label: L10n.text("dex_deposit"), value: "\(DexFormat.number(l.expectedAmount)) \(l.sellCoin)" + extra)
                    if !l.expiresAt.isEmpty { DexField(label: L10n.text("dex_expires"), value: DexText.time(l.expiresAt)) }
                    if !l.marketWallet.isEmpty {
                        AddressBlock(model: model, address: l.marketWallet, amount: l.expectedAmount, coin: l.sellCoin,
                                     qrTitle: L10n.text("dex_fund_title"), qrHint: L10n.text("dex_hint_escrow"))
                    }
                } else {
                    DexField(label: L10n.text("dex_deposit"), value: L10n.text("dex_received"))
                }
                if !l.txHash.isEmpty { DexField(label: L10n.text("dex_deposit_tx"), value: DexText.shortId(l.txHash), mono: true) }
            }
            .padding(.top, 8)
            Group {
                if l.cancelable {
                    let busy = model.busyId == l.id
                    Button(action: onCancel) {
                        Text(busy ? L10n.key("dex_canceling") : L10n.key("dex_cancel_offer")).foregroundStyle(Color.skyError)
                    }
                    .buttonStyle(.outlined)
                    .disabled(busy)
                } else if l.status == "confirmed" && l.productStatus == "frozen" {
                    DexHint(text: L10n.text("dex_buyer_paying"))
                } else if l.status == "confirmed" && l.productStatus == "sold" {
                    DexHint(text: L10n.text("dex_product_sold"))
                }
            }
            .padding(.top, 8)
        }
    }
}

// MARK: History

private struct DexHistoryTab: View {
    @ObservedObject var model: DexModel

    var body: some View {
        let rows = model.local.history.sorted { DexText.millis($0.createdAt) ?? $0.savedAt > DexText.millis($1.createdAt) ?? $1.savedAt }
        if rows.isEmpty {
            DexHint(text: L10n.text("dex_no_history"))
        } else {
            ForEach(rows, id: \.historyKey) { o in
                DexCard {
                    HStack(spacing: 6) {
                        TypeBadge(type: o.type)
                        StatusBadge(status: o.status)
                        Spacer(minLength: 0)
                        Button { model.removeHistory(o) } label: {
                            MaterialIcon(MI.roundedClose, size: 18).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 32, height: 32)
                        }
                        .accessibilityLabel(Text("dex_remove_from_history"))
                    }
                    Text(verbatim: "\(DexFormat.number(o.amount)) \(o.sellCoin)").skyText(.titleMedium).foregroundStyle(Color.skyOnSurface)
                    Text(verbatim: "\(DexFormat.number(o.price)) \(o.paymentCurrency)").skyText(.bodyMedium).foregroundStyle(Color.skyPrimary)
                    VStack(alignment: .leading, spacing: 0) {
                        DexField(label: L10n.text("dex_market"), value: o.marketName.isEmpty ? (o.marketPk.isEmpty ? "—" : DexText.shortKey(o.marketPk)) : o.marketName)
                        DexField(label: L10n.text("dex_date"), value: o.createdAt.isEmpty ? "—" : DexText.dateTime(o.createdAt))
                    }
                    .padding(.top, 6)
                }
            }
            DexHint(text: L10n.text("dex_history_hint"))
        }
    }
}

// MARK: Settings

private struct DexSettingsTab: View {
    @ObservedObject var model: DexModel
    @EnvironmentObject private var dialogs: SkyDialogs
    @State private var sky = ""
    @State private var payouts: [String: String] = [:]

    var body: some View {
        DexCard {
            Text("dex_wallets_title").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
            DexHint(text: L10n.text("dex_wallets_body")).padding(.top, 6)
            SkyOutlinedTextField(label: L10n.key("dex_sky_address"), placeholder: L10n.key("dex_sky_address_hint"), text: $sky, mono: true,
                                 notch: .skySurfaceVariant, identifier: "dex-sky-address")
                .padding(.top, 12)
            ForEach(model.currencies, id: \.self) { coin in
                PayoutField(coin: coin, text: Binding(get: { payouts[coin] ?? "" }, set: { payouts[coin] = $0 }))
                    .padding(.top, 8)
            }
            if model.currencies.isEmpty && model.ready != nil {
                DexHint(text: L10n.text("dex_no_payment_coins")).padding(.top, 8)
            }
            if let message = model.walletsMessage {
                Text(verbatim: message.text).skyText(.bodySmall).foregroundStyle(message.ok ? Color.dexGood : Color.skyError).padding(.top, 10)
                    .accessibilityIdentifier("dex-wallets-message")
            }
            Button { model.saveWallets(sky: sky, payouts: payouts) } label: {
                Text(model.savingWallets ? L10n.key("dex_saving") : L10n.key("dex_save_addresses")).frame(maxWidth: .infinity)
            }
            .buttonStyle(.filled)
            .disabled(model.savingWallets)
            .accessibilityIdentifier("dex-save-addresses")
            .padding(.top, 12)
        }
        DexCard {
            let pk = model.market?.marketPK ?? ""
            Text("dex_connection_title").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
            Text(verbatim: pk).skyText(.bodySmall, mono: true).foregroundStyle(Color.skyOnSurface).padding(.top, 8).textSelection(.enabled)
            Button {
                UIPasteboard.general.string = pk
                model.notice = L10n.text("copied_to_clipboard")
            } label: {
                HStack(spacing: 6) {
                    MaterialIcon(MI.roundedContentCopy, size: 18)
                    Text("dex_copy_key")
                }
            }
            .buttonStyle(.outlined)
            .disabled(pk.isEmpty)
            .padding(.top, 8)
            DexHint(text: L10n.text("dex_switch_hint")).padding(.top, 6)
        }
        // "Clear history" sits here, away from the list it wipes, as the phone layout of the page had it.
        if !model.local.history.isEmpty {
            DexCard {
                Text("dex_history_title").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
                DexHint(text: L10n.text("dex_history_body")).padding(.top, 6)
                Button {
                    dialogs.confirm(L10n.text("dex_clear_history_confirm"), confirm: L10n.text("dex_clear_history"), cancel: L10n.text("dex_keep")) { model.clearHistory() }
                } label: { Text("dex_clear_history") }
                .buttonStyle(.tonal)
                .padding(.top, 10)
            }
        }
        Color.clear.frame(height: 0)
            .onAppear { fill() }
            .onChange(of: model.currencies) { _ in fill() }
    }

    private func fill() {
        if sky.isEmpty { sky = model.local.wallets[DexModel.sky] ?? "" }
        for coin in model.currencies where payouts[coin] == nil {
            payouts[coin] = model.local.wallets[coin] ?? ""
        }
    }
}

private struct PayoutField: View {
    let coin: String
    @Binding var text: String

    var body: some View {
        SkyOutlinedTextField(label: LocalizedStringKey(L10n.format("dex_payout_address", coin)), placeholder: LocalizedStringKey(L10n.format("dex_payout_hint", coin)),
                             text: $text, mono: true, notch: .skySurfaceVariant, identifier: "dex-payout-\(coin)")
    }
}

extension DexOrder {
    /// A self-trade is one order id seen as a buy and as a sell.
    var historyKey: String { type + ":" + id }
}
