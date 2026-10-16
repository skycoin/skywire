import PhotosUI
import SwiftUI

/// The selected coin's wallets (Android: ui/wallet/WalletManage.kt): switch, rename, reveal,
/// remove, add.
struct WalletManageView: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        WalletScreen(title: Text("wallet_wallets_title")) {
            ScrollView {
                VStack(spacing: 10) {
                    ForEach(model.coinWallets) { wallet in
                        Button { openActions(wallet) } label: { row(wallet) }
                            .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: SkyRadius.large))))
                            .accessibilityIdentifier("wallet-row-\(wallet.receiveAddresses.first ?? wallet.id)")
                    }
                    HStack(spacing: 12) {
                        Button {
                            model.startCreate()
                            model.path.append(.create)
                        } label: { Text("wallet_manage_create").lineLimit(1).frame(maxWidth: .infinity) }
                        .buttonStyle(TonalButtonStyle(height: 48))
                        .accessibilityIdentifier("wallet-manage-create")
                        Button {
                            model.startRestore()
                            model.path.append(.restore)
                        } label: { Text("wallet_intro_restore").lineLimit(1).frame(maxWidth: .infinity) }
                        .buttonStyle(TonalButtonStyle(height: 48))
                        .accessibilityIdentifier("wallet-manage-restore")
                    }
                    .padding(.top, 6)
                    Spacer().frame(height: 14)
                }
                .padding(.horizontal, 20)
            }
        }
    }

    private func row(_ wallet: WalletMeta) -> some View {
        HStack(spacing: 13) {
            Text(verbatim: String(wallet.name.prefix(2)).uppercased())
                .skyText(.labelMedium)
                .foregroundStyle(Color.skyPrimary)
                .frame(width: 38, height: 38)
                .background(Color.skySecondaryContainer, in: Circle())
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 8) {
                    Text(verbatim: wallet.name).skyText(.bodyLarge, bold: true).foregroundStyle(Color.skyOnSurface)
                    if wallet.id == model.active?.id {
                        Text("wallet_active_badge")
                            .skyText(.labelSmall)
                            .foregroundStyle(Color.skyPrimary)
                            .padding(.horizontal, 7)
                            .padding(.vertical, 2)
                            .background(Color.skySecondaryContainer, in: .sky(SkyRadius.extraSmall))
                    }
                }
                Text(verbatim: "\(shortAddress(wallet.receiveAddresses.first ?? "", head: 6, tail: 3)) · \(addressCount(wallet))")
                    .skyText(.bodySmall)
                    .foregroundStyle(Color.skyOnSurfaceVariant)
                    .padding(.top, 3)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            MaterialIcon(MI.outlinedMoreVert, size: 17).foregroundStyle(Color.skyOnSurfaceVariant)
        }
        .padding(16)
        .background(Color.skySurfaceVariant, in: .sky(SkyRadius.large))
        .contentShape(RoundedRectangle(cornerRadius: SkyRadius.large))
    }

    private func openActions(_ wallet: WalletMeta) {
        let created = Date(timeIntervalSince1970: TimeInterval(wallet.createdAtMs) / 1000)
        dialogs.presentSheet {
            VStack(alignment: .leading, spacing: 0) {
                Text(verbatim: wallet.name).skyText(.titleMedium).padding(.horizontal, 20)
                Text(verbatim: L10n.format("wallet_created_on", addressCount(wallet), wallFormat("d MMMM yyyy", created)))
                    .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                    .padding(.horizontal, 20).padding(.vertical, 5)
                Spacer().frame(height: 10)
                if wallet.id != model.active?.id {
                    SheetAction(icon: MI.outlinedCheckCircle, title: L10n.key("wallet_use"), identifier: "wallet-action-use") {
                        dialogs.dismissSheet()
                        model.useWallet(wallet.id)
                    }
                }
                SheetAction(icon: MI.outlinedEdit, title: L10n.key("wallet_rename"), identifier: "wallet-action-rename") {
                    dialogs.dismissSheet()
                    dialogs.showCustom { WalletRenameDialog(current: wallet.name) { model.renameWallet(wallet.id, name: $0) } }
                }
                // The reveal screen asks for Face ID or the passcode itself, then reads the phrase.
                SheetAction(icon: MI.outlinedVisibility, title: L10n.key("wallet_reveal_action"), subtitle: L10n.key("wallet_reveal_action_sub_ios"),
                            identifier: "wallet-action-reveal") {
                    dialogs.dismissSheet()
                    model.path.append(.reveal(wallet.id))
                }
                SheetAction(icon: MI.outlinedDeleteOutline, title: L10n.key("wallet_remove_action"), subtitle: L10n.key("wallet_remove_action_sub"),
                            destructive: true, identifier: "wallet-action-remove") {
                    dialogs.dismissSheet()
                    dialogs.show(SkyDialog(title: Text(verbatim: L10n.format("wallet_remove_title", wallet.name)),
                                           message: Text("wallet_remove_body"),
                                           actions: [SkyDialog.Action(label: Text("wallet_remove_keep")),
                                                     SkyDialog.Action(label: Text("wallet_remove_confirm"), destructive: true) {
                                                         model.removeWallet(wallet.id)
                                                     }]))
                }
            }
            .padding(.bottom, 20)
        }
    }

    private func addressCount(_ wallet: WalletMeta) -> String {
        let n = wallet.receiveAddresses.count
        return n == 1 ? L10n.text("wallet_address_count_one") : L10n.format("wallet_address_count_many", n)
    }
}

/// A sheet's action row: a 19 pt icon, a bold title, an optional line under it.
private struct SheetAction: View {
    let icon: String
    let title: LocalizedStringKey
    var subtitle: LocalizedStringKey? = nil
    var destructive = false
    var identifier: String = ""
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(alignment: .top, spacing: 14) {
                MaterialIcon(icon, size: 19).foregroundStyle(destructive ? Color.skyError : Color.skyOnSurfaceVariant)
                VStack(alignment: .leading, spacing: 0) {
                    Text(title).skyText(.bodyLarge, bold: true).foregroundStyle(destructive ? Color.skyError : Color.skyOnSurface)
                    if let subtitle {
                        Text(subtitle).skyText(.bodySmall)
                            .foregroundStyle(destructive ? Color.skyError.opacity(0.8) : Color.skyOnSurfaceVariant)
                            .padding(.top, 3)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(.horizontal, 20)
            .padding(.vertical, 14)
            .contentShape(Rectangle())
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface))
        .accessibilityIdentifier(identifier)
    }
}

/// Rename: one field, Save while it holds a name.
private struct WalletRenameDialog: View {
    let current: String
    let save: (String) -> Void
    @State private var text = ""

    var body: some View {
        DialogFrame(title: Text("wallet_rename_title"), confirm: L10n.key("save"),
                    enabled: !text.trimmingCharacters(in: .whitespaces).isEmpty, onConfirm: { save(text) }) {
            SkyOutlinedTextField(text: $text, notch: .skyContainerHigh)
        }
        .onAppear { text = current }
    }
}

/// The phrase in the clear: read from the Keychain only once the device-owner check has passed
/// (back to the list if it does not), covered whenever it could be captured, gone after two minutes.
struct WalletRevealView: View {
    @EnvironmentObject private var model: WalletModel
    let walletId: String
    @State private var seed: String?
    @State private var unavailable = false
    @State private var secondsLeft = 120

    var body: some View {
        WalletScreen(title: Text("wallet_seed_title")) {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    HStack(alignment: .top, spacing: 10) {
                        MaterialIcon(MI.outlinedErrorOutline, size: 17)
                        Text(verbatim: L10n.format("wallet_reveal_warning", model.allWallets.first { $0.id == walletId }?.name ?? ""))
                            .skyText(.bodySmall)
                    }
                    .foregroundStyle(Color.skyOnErrorContainer)
                    .padding(.horizontal, 15)
                    .padding(.vertical, 14)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color.skyErrorContainer, in: .sky(14))
                    // iOS's own word on what protects the words here (Android says screenshots are off).
                    SeedPrivacyNote().padding(.top, 12)
                    Spacer().frame(height: 20)
                    if unavailable {
                        Text("wallet_seed_unavailable").skyText(.bodyLarge).foregroundStyle(Color.skyError)
                    } else if let seed {
                        SeedGrid(words: seed.split(separator: " ").map(String.init))
                        Text(verbatim: L10n.format("wallet_reveal_autohide_ios", String(format: "%d:%02d", secondsLeft / 60, secondsLeft % 60)))
                            .skyText(.bodySmall)
                            .foregroundStyle(Color.skyOnSurfaceVariant)
                            .multilineTextAlignment(.center)
                            .frame(maxWidth: .infinity)
                            .padding(.top, 16)
                    }
                    Button { close() } label: { Text("wallet_reveal_hide").frame(maxWidth: .infinity) }
                        .buttonStyle(TonalButtonStyle(height: 52))
                        .padding(.top, 22)
                        .padding(.bottom, 24)
                        .accessibilityIdentifier("wallet-reveal-hide")
                }
                .padding(.horizontal, 20)
            }
        }
        .modifier(SeedPrivacy())
        .task {
            let name = model.allWallets.first { $0.id == walletId }?.name ?? ""
            guard await WalletAuth.confirm(reason: L10n.format("wallet_bio_reveal_subtitle", name)) else {
                close()
                return
            }
            seed = model.revealSeed(walletId)
            unavailable = seed == nil
            guard seed != nil else { return }
            while secondsLeft > 0 {
                do { try await Task.sleep(for: .seconds(1)) } catch { return }
                secondsLeft -= 1
            }
            close()
        }
        .onDisappear { seed = nil }
    }

    private func close() {
        seed = nil
        if case .reveal = model.path.last { model.path.removeLast() }
    }
}

/// What the add screen can add: a fiber chain, or an ERC-20 on Ethereum.
private enum AddCoinKind: Hashable { case fiber, erc20 }

/// Add a Fibercoin (name, ticker, node URL) or an ERC-20 token (name, ticker, contract,
/// decimals), with an optional badge picked from Photos.
struct WalletAddCoinView: View {
    @EnvironmentObject private var model: WalletModel
    @State private var kind = AddCoinKind.fiber
    @State private var name = ""
    @State private var ticker = ""
    @State private var icon: String?
    @State private var node = ""
    @State private var contract = ""
    @State private var decimals = "18"
    @State private var photo: PhotosPickerItem?
    @State private var picking = false

    var body: some View {
        WalletScreen(title: Text("wallet_add_coin_title")) {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    HStack(spacing: 8) {
                        kindChip(L10n.key("wallet_add_kind_fiber"), .fiber)
                        kindChip(L10n.key("wallet_add_kind_erc20"), .erc20)
                    }
                    Text(kind == .fiber ? L10n.key("wallet_add_coin_body") : L10n.key("wallet_add_token_body"))
                        .skyText(.bodyLarge).foregroundStyle(Color.skyOnSurfaceVariant)
                        .padding(.top, 18)
                        .padding(.bottom, 24)
                    LabeledField(label: L10n.key("wallet_add_coin_name"), hint: L10n.key("wallet_add_coin_name_hint"),
                                 text: $name, identifier: "wallet-add-name")
                    LabeledField(label: L10n.key("wallet_add_coin_ticker"), hint: L10n.key("wallet_add_coin_ticker_hint"),
                                 text: $ticker, identifier: "wallet-add-ticker")
                    iconRow
                    if kind == .fiber {
                        LabeledField(label: L10n.key("wallet_add_coin_node"), hint: L10n.key("wallet_add_coin_node_hint"),
                                     text: $node, keyboard: .URL, identifier: "wallet-add-node")
                    } else {
                        LabeledField(label: L10n.key("wallet_add_token_contract"), hint: L10n.key("wallet_add_token_contract_hint"),
                                     text: $contract, identifier: "wallet-add-contract")
                        LabeledField(label: L10n.key("wallet_add_token_decimals"), hint: L10n.key("wallet_add_token_decimals_hint"),
                                     text: $decimals, keyboard: .numberPad, identifier: "wallet-add-decimals")
                    }
                    Button {
                        let done = { model.path.removeAll() }
                        if kind == .fiber {
                            model.addFiberCoin(name: name, ticker: ticker, nodeUrl: node, icon: icon, onDone: done)
                        } else {
                            model.addErc20Token(name: name, ticker: ticker, contract: contract, decimals: decimals, icon: icon, onDone: done)
                        }
                    } label: { Text("wallet_add_coin_save").frame(maxWidth: .infinity) }
                    .buttonStyle(FilledButtonStyle(height: 52))
                    .disabled(!complete)
                    .padding(.top, 12)
                    .padding(.bottom, 24)
                    .accessibilityIdentifier("wallet-add-save")
                }
                .padding(.horizontal, 20)
            }
        }
        .photosPicker(isPresented: $picking, selection: $photo, matching: .images)
        .onChange(of: photo) { item in
            guard let item else { return }
            Task {
                if let data = try? await item.loadTransferable(type: Data.self) {
                    model.importCoinIcon(data) { icon = $0 }
                }
                photo = nil
            }
        }
    }

    private var complete: Bool {
        let filled = { (s: String) in !s.trimmingCharacters(in: .whitespaces).isEmpty }
        guard filled(name), filled(ticker) else { return false }
        return kind == .fiber ? filled(node) : filled(contract) && filled(decimals)
    }

    private func kindChip(_ label: LocalizedStringKey, _ value: AddCoinKind) -> some View {
        let selected = kind == value
        return Button { kind = value } label: {
            Text(label).skyText(.labelLarge)
                .foregroundStyle(selected ? Color.skyPrimary : Color.skyOnSurfaceVariant)
                .padding(.horizontal, 15)
                .padding(.vertical, 9)
                .background(selected ? Color.skySecondaryContainer : Color.skyContainerHighest, in: .sky(10))
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: 10))))
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    /// The badge the new coin will wear: the picked image, or the ticker's letters while there is none.
    private var iconRow: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("wallet_add_coin_icon").skyText(.labelLarge).foregroundStyle(Color.skyOnSurfaceVariant).padding(.bottom, 7)
            HStack(spacing: 12) {
                Button { picking = true } label: { iconPreview }.buttonStyle(PressStyle())
                Button { picking = true } label: { Text("wallet_add_coin_icon_pick") }.buttonStyle(.tonal)
                if icon != nil {
                    Button { icon = nil } label: { Text("wallet_add_coin_icon_clear") }.buttonStyle(.skyText)
                }
            }
        }
        .padding(.bottom, 16)
    }

    private var iconPreview: some View {
        Group {
            if let icon, let image = CoinIcons.image(icon, in: model.store) {
                Image(uiImage: image).resizable().scaledToFill()
            } else {
                let letters = String(ticker.trimmingCharacters(in: .whitespaces).uppercased().prefix(3))
                Text(verbatim: letters.isEmpty ? "ABC" : letters)
                    .skyText(.labelSmall)
                    .foregroundStyle(Color.skyPrimary)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(Color.skyContainerHighest)
            }
        }
        .frame(width: 46, height: 46)
        .clipShape(Circle())
        .overlay(Circle().strokeBorder(Color.skyOutlineVariant, lineWidth: 1))
    }
}

/// A field under its own label (labelLarge), Android's LabeledField: 12 pt corners, a placeholder.
private struct LabeledField: View {
    let label: LocalizedStringKey
    let hint: LocalizedStringKey
    @Binding var text: String
    var keyboard: UIKeyboardType = .default
    let identifier: String

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(label).skyText(.labelLarge).foregroundStyle(Color.skyOnSurfaceVariant).padding(.bottom, 7)
            SkyOutlinedTextField(placeholder: hint, text: $text, keyboard: keyboard, identifier: identifier, radius: SkyRadius.small)
        }
        .padding(.bottom, 16)
    }
}

/// Which node this coin's wallet talks to (Android: ui/wallet/WalletNode.kt). Keys never leave the
/// phone, but the node sees which addresses are asked about together: hence the transport note.
/// The Ethereum family reads history from an indexer apart from its node, so there the screen holds
/// both, saved and reset together.
struct WalletNodeView: View {
    @EnvironmentObject private var model: WalletModel
    @State private var url = ""
    @State private var indexer = ""

    var body: some View {
        let coin = model.coin
        let shippedIndexer = model.defaultIndexerUrl
        let hasIndexer = shippedIndexer != nil
        let changed = Self.typed(url) != coin.nodeUrl || (hasIndexer && Self.typed(indexer) != coin.indexerUrl)
        let shipped = coin.nodeUrl == model.defaultNodeUrl && Self.typed(url) == model.defaultNodeUrl
            && (!hasIndexer || (coin.indexerUrl == shippedIndexer && Self.typed(indexer) == shippedIndexer))
        WalletScreen(title: Text("wallet_node_title")) {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: L10n.format(hasIndexer ? "wallet_node_body_indexer" : "wallet_node_body", coin.name))
                        .skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                        .padding(.top, 6)
                        .padding(.bottom, 18)
                    // Said where the choice is made: an address typed here decides whether the
                    // address book travels in the clear.
                    urlField(label: L10n.key("wallet_node_field"), hint: nodeHint(coin.kind), text: $url,
                             placeholder: model.defaultNodeUrl, identifier: "wallet-node-url")
                    if let shippedIndexer {
                        urlField(label: L10n.key("wallet_indexer_field"), hint: L10n.key("wallet_indexer_hint"), text: $indexer,
                                 placeholder: shippedIndexer, identifier: "wallet-indexer-url")
                            .padding(.top, 20)
                    }
                    Button {
                        model.setNodeUrl(url, indexerUrl: hasIndexer ? indexer : nil) { popNode() }
                    } label: { Text(hasIndexer ? L10n.key("wallet_node_save_all") : L10n.key("wallet_node_save")).frame(maxWidth: .infinity) }
                    .buttonStyle(FilledButtonStyle(height: 50))
                    .disabled(Self.blank(url) || (hasIndexer && Self.blank(indexer)) || !changed)
                    .padding(.top, 20)
                    .accessibilityIdentifier("wallet-node-save")
                    if !shipped {
                        Button {
                            model.setNodeUrl("", indexerUrl: hasIndexer ? "" : nil) { popNode() }
                        } label: {
                            Text(verbatim: hasIndexer ? L10n.text("wallet_node_default_all") : L10n.format("wallet_node_default", model.defaultNodeUrl))
                                .frame(maxWidth: .infinity)
                        }
                        .buttonStyle(.skyText)
                        .padding(.top, 4)
                        .accessibilityIdentifier("wallet-node-default")
                    }
                }
                .padding(.horizontal, 20)
            }
        }
        // Seeded from the addresses in force, so the fields open on what is actually used.
        .onAppear {
            url = model.coin.nodeUrl
            indexer = model.coin.indexerUrl ?? ""
        }
    }

    /// As the store keeps it, so a trailing slash is not a change.
    private static func typed(_ value: String) -> String {
        var trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.hasSuffix("/") { trimmed.removeLast() }
        return trimmed
    }

    private static func blank(_ value: String) -> Bool {
        value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    /// What kind of server the node field takes; Skycoin and fiber chains say it in the body.
    private func nodeHint(_ kind: CoinKind) -> LocalizedStringKey? {
        switch kind {
        case .btc: L10n.key("wallet_node_hint_btc")
        case .eth, .erc20: L10n.key("wallet_node_hint_eth")
        case .skyFiber: nil
        }
    }

    /// One address field: its label, what kind of server it takes, and whether it is encrypted.
    private func urlField(label: LocalizedStringKey, hint: LocalizedStringKey?, text: Binding<String>,
                          placeholder: String, identifier: String) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(label).skyText(.labelLarge).foregroundStyle(Color.skyOnSurfaceVariant).padding(.bottom, 7)
            SkyOutlinedTextField(placeholder: LocalizedStringKey(placeholder), text: text, keyboard: .URL,
                                 identifier: identifier, radius: SkyRadius.small)
            if let hint {
                Text(hint).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 6)
            }
            transportNote(text.wrappedValue)
        }
    }

    private func transportNote(_ url: String) -> some View {
        let cleartext = url.trimmingCharacters(in: .whitespaces).lowercased().hasPrefix("http://")
        return HStack(alignment: .top, spacing: 9) {
            MaterialIcon(cleartext ? MI.outlinedLockOpen : MI.outlinedLock, size: 16)
            Text(cleartext ? L10n.key("wallet_node_cleartext") : L10n.key("wallet_node_encrypted")).skyText(.bodySmall)
        }
        .foregroundStyle(cleartext ? Color.skyWarning : Color.skyOnSurfaceVariant)
        .padding(.horizontal, 14)
        .padding(.vertical, 13)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(cleartext ? Color.skyWarning.opacity(0.12) : Color.skySurfaceVariant, in: .sky(SkyRadius.small))
        .padding(.top, 14)
    }

    private func popNode() {
        if model.path.last == .node { model.path.removeLast() }
    }
}
