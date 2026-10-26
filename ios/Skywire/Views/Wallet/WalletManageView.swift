import PhotosUI
import SwiftUI
import UniformTypeIdentifiers
import WalletCore

/// The selected coin's wallets (Android: ui/wallet/WalletManage.kt): switch, rename, reveal,
/// remove, add.
struct WalletManageView: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs
    /// The wallet whose addresses the system's save dialog is writing.
    @State private var exportTarget: WalletMeta?

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
        // The system's own save dialog: the user picks the place, and the app needs no access to
        // the files around it.
        .fileExporter(isPresented: Binding(get: { exportTarget != nil }, set: { if !$0 { exportTarget = nil } }),
                      document: AddressesDocument(text: exportTarget.map(addressesCsv) ?? ""),
                      contentType: .commaSeparatedText,
                      defaultFilename: "\(exportTarget?.name ?? "addresses").csv") { result in
            exportTarget = nil
            if case .success = result {
                dialogs.snackbar(Text("wallet_export_done"))
            } else if case let .failure(error) = result, (error as? CocoaError)?.code != .userCancelled {
                dialogs.snackbar(Text("wallet_export_failed"))
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
                SheetAction(icon: MI.outlinedFileDownload, title: L10n.key("wallet_export_action"), subtitle: L10n.key("wallet_export_action_sub"),
                            identifier: "wallet-action-export") {
                    dialogs.dismissSheet()
                    exportTarget = wallet
                }
                if wallet.derivation == .bip44 {
                    SheetAction(icon: MI.outlinedKey, title: L10n.key("wallet_xpub_action"), subtitle: L10n.key("wallet_xpub_action_sub"),
                                identifier: "wallet-action-xpub") {
                        dialogs.dismissSheet()
                        if let keys = model.extendedPublicKeys(wallet.id) {
                            dialogs.showCustom { XpubDialog(keys: keys) }
                        }
                    }
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
    @State private var passphrase = ""
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
                        // A wallet made with a passphrase needs it as much as the words to be restored.
                        if !passphrase.isEmpty {
                            Text(verbatim: L10n.text("wallet_passphrase_reveal_label").uppercased())
                                .skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 18)
                            Text(verbatim: passphrase).skyText(.bodyLarge, bold: true)
                                .padding(.horizontal, 14)
                                .padding(.vertical, 13)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .background(Color.skySurfaceVariant, in: .sky(12))
                                .padding(.top, 8)
                                .accessibilityIdentifier("wallet-reveal-passphrase")
                        }
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
            passphrase = model.revealPassphrase(walletId)
            unavailable = seed == nil
            guard seed != nil else { return }
            while secondsLeft > 0 {
                do { try await Task.sleep(for: .seconds(1)) } catch { return }
                secondsLeft -= 1
            }
            close()
        }
        .onDisappear {
            seed = nil
            passphrase = ""
        }
    }

    private func close() {
        seed = nil
        passphrase = ""
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
                    // A fiber coin's node names its coin, so these may be left for it to fill.
                    LabeledField(label: L10n.key("wallet_add_coin_name"),
                                 hint: kind == .fiber ? L10n.key("wallet_add_coin_from_node") : L10n.key("wallet_add_coin_name_hint"),
                                 text: $name, identifier: "wallet-add-name")
                    LabeledField(label: L10n.key("wallet_add_coin_ticker"),
                                 hint: kind == .fiber ? L10n.key("wallet_add_coin_from_node") : L10n.key("wallet_add_coin_ticker_hint"),
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
        // A fiber coin's name and ticker may come from its node.
        if kind == .fiber { return filled(node) }
        return filled(name) && filled(ticker) && filled(contract) && filled(decimals)
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

/// Which nodes this coin's wallet talks to, and in what order (Android: ui/wallet/WalletNode.kt).
/// The first is asked first and the shipped address rides behind the list as the fallback. Keys
/// never leave the phone, but a node sees which addresses are asked about together: hence the
/// transport note. The Ethereum family reads history from an indexer apart from its node, so there
/// the screen holds that address too, saved on its own.
struct WalletNodeView: View {
    @EnvironmentObject private var model: WalletModel
    @EnvironmentObject private var dialogs: SkyDialogs
    @State private var add = ""
    @State private var indexer = ""
    @State private var error: String?
    @State private var checking = false

    var body: some View {
        let coin = model.coin
        let nodes = model.nodeUrls
        WalletScreen(title: Text("wallet_node_title")) {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: L10n.format(model.defaultIndexerUrl != nil ? "wallet_node_body_indexer" : "wallet_node_body", coin.name))
                        .skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                        .padding(.top, 6)
                        .padding(.bottom, 10)
                    Text("wallet_node_list_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.bottom, 14)
                    if coin.kind == .skyFiber {
                        Button { model.path.append(.nodeInfo) } label: {
                            HStack(spacing: 12) {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text("wallet_nodeinfo_title").skyText(.bodyLarge, bold: true).foregroundStyle(Color.skyOnSurface)
                                    Text("wallet_nodeinfo_open_sub").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                                }
                                .frame(maxWidth: .infinity, alignment: .leading)
                                MaterialIcon(MI.outlinedKeyboardArrowRight, size: 18).foregroundStyle(Color.skyOnSurfaceVariant)
                            }
                            .padding(.horizontal, 16)
                            .padding(.vertical, 13)
                            .background(Color.skySurfaceVariant, in: .sky(14))
                            .contentShape(RoundedRectangle(cornerRadius: 14))
                        }
                        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: 14))))
                        .padding(.bottom, 14)
                        .accessibilityIdentifier("wallet-nodeinfo-open")
                    }
                    // Every change saves at once: the order on screen is the order the wallet asks in.
                    ForEach(Array(nodes.enumerated()), id: \.element) { i, url in
                        NodeRow(position: i + 1, url: url, last: i == nodes.count - 1,
                                onUp: { move(i, by: -1) }, onDown: { move(i, by: 1) },
                                onRemove: { model.saveNodeUrls(nodes.enumerated().filter { $0.offset != i }.map(\.element)) })
                    }
                    // The fallback: fixed at the end, always there, not removable.
                    ShippedNodeRow(url: model.defaultNodeUrl)
                    SkyOutlinedTextField(placeholder: L10n.key("wallet_node_field"),
                                         text: Binding(get: { add }, set: { add = $0; error = nil }), keyboard: .URL,
                                         identifier: "wallet-node-add-url", radius: SkyRadius.small)
                        .padding(.top, 18)
                    if let error {
                        Text(verbatim: error).skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 6)
                            .accessibilityIdentifier("wallet-node-error")
                    }
                    Button { addNode() } label: {
                        Text(checking ? L10n.key("wallet_node_checking") : L10n.key("wallet_node_add")).frame(maxWidth: .infinity)
                    }
                        .buttonStyle(FilledButtonStyle(height: 50))
                        .disabled(Self.typed(add).isEmpty || checking)
                        .padding(.top, 10)
                        .accessibilityIdentifier("wallet-node-add")
                    NodeTransportNote(url: Self.typed(add))
                    if let shippedIndexer = model.defaultIndexerUrl {
                        indexerField(shippedIndexer)
                    }
                }
                .padding(.horizontal, 20)
                .padding(.bottom, 24)
            }
        }
        // Seeded from the address in force, so the field opens on what is actually used.
        .onAppear { indexer = model.coin.indexerUrl ?? "" }
    }

    @ViewBuilder private func indexerField(_ shipped: String) -> some View {
        Text("wallet_indexer_field").skyText(.labelLarge).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 20).padding(.bottom, 7)
        SkyOutlinedTextField(placeholder: LocalizedStringKey(shipped), text: $indexer, keyboard: .URL,
                             identifier: "wallet-indexer-url", radius: SkyRadius.small)
        Text("wallet_indexer_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 6)
        Button { model.saveNodeUrls(model.nodeUrls, indexerUrl: Self.typed(indexer)) } label: {
            Text("wallet_node_save").frame(maxWidth: .infinity)
        }
        .buttonStyle(FilledButtonStyle(height: 50))
        .disabled(Self.typed(indexer).isEmpty || Self.typed(indexer) == model.coin.indexerUrl)
        .padding(.top, 12)
        .accessibilityIdentifier("wallet-indexer-save")
    }

    private func addNode() {
        let url = Self.typed(add)
        guard !url.isEmpty else { return }
        if !WalletStore.isHTTPURL(url) {
            error = L10n.text("wallet_node_invalid")
        } else if url == model.defaultNodeUrl || model.nodeUrls.contains(url) {
            error = L10n.text("wallet_node_duplicate")
        } else {
            // Asked first, as skycoin-web does: a node that does not answer, or is too old, never
            // joins the list the wallet asks in.
            error = nil
            checking = true
            let coin = model.coin
            Task {
                let result = await model.checkNode(url)
                checking = false
                switch result {
                case .success(let info):
                    dialogs.showCustom {
                        NodeCheckDialog(coin: coin, info: info) {
                            add = ""
                            model.saveNodeUrls(model.nodeUrls + [url])
                        }
                    }
                case .failure(let refused):
                    error = refused.message
                }
            }
        }
    }

    private func move(_ index: Int, by step: Int) {
        var next = model.nodeUrls
        let to = index + step
        guard next.indices.contains(to) else { return }
        next.swapAt(index, to)
        model.saveNodeUrls(next)
    }

    /// As the store keeps it, so a trailing slash or a key in hex is not a change.
    static func typed(_ value: String) -> String {
        var trimmed = WalletMesh.normalize(value)
        if trimmed.hasSuffix("/") { trimmed.removeLast() }
        return trimmed
    }
}

/// What a node reports about itself, as skycoin-web shows it before switching to it.
private struct NodeCheckDialog: View {
    @EnvironmentObject private var dialogs: SkyDialogs
    let coin: CoinSpec
    let info: NodeInfo
    let onUse: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("wallet_node_check_title").skyText(.headlineSmall).foregroundStyle(Color.skyOnSurface)
            VStack(alignment: .leading, spacing: 0) {
                row(L10n.key("wallet_node_check_coin"), info.coinName ?? coin.name)
                if let version = info.version { row(L10n.key("wallet_node_check_version"), version) }
                if let height = info.height { row(L10n.key("wallet_node_check_height"), Amounts.groupThousands(String(height))) }
                if let burn = info.burnFactor { row(L10n.key("wallet_node_check_burn"), "1/\(burn)") }
                if let pubkey = info.pubkey { row(L10n.key("wallet_node_check_pubkey"), shortAddress(pubkey, head: 10, tail: 8)) }
                // A fiber node answers for its own chain; another coin's node would show that chain's balances.
                if coin.kind == .skyFiber, let other = info.coinName, other.lowercased() != coin.name.lowercased() {
                    Text(verbatim: L10n.format("wallet_node_other_coin", other, coin.name))
                        .skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 10)
                        .accessibilityIdentifier("wallet-node-other-coin")
                }
            }
            .padding(.top, 16)
            HStack(spacing: 8) {
                Spacer()
                Button { dialogs.dismiss() } label: { Text("cancel") }.buttonStyle(.skyText)
                Button {
                    dialogs.dismiss()
                    onUse()
                } label: { Text("wallet_node_check_use") }
                    .buttonStyle(.skyText)
                    .accessibilityIdentifier("wallet-node-check-use")
            }
            .padding(.top, 24)
        }
    }

    private func row(_ label: LocalizedStringKey, _ value: String) -> some View {
        HStack(alignment: .firstTextBaseline) {
            Text(label).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).frame(maxWidth: .infinity, alignment: .leading)
            Text(verbatim: value).skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyOnSurface).fixedSize()
        }
        .padding(.vertical, 6)
    }
}

/// Plain http to a host on the internet. A .dmsg host is reached over dmsg, which encrypts it end to end.
private func cleartextNode(_ url: String) -> Bool {
    url.trimmingCharacters(in: .whitespaces).lowercased().hasPrefix("http://") && !WalletMesh.isMeshURL(url)
}

/// One of the user's nodes: its priority, its address, and the reorder and remove controls.
private struct NodeRow: View {
    let position: Int
    let url: String
    let last: Bool
    let onUp: () -> Void
    let onDown: () -> Void
    let onRemove: () -> Void

    var body: some View {
        let cleartext = cleartextNode(url)
        HStack(spacing: 0) {
            Text(verbatim: "\(position)").skyText(.labelLarge, bold: true).foregroundStyle(Color.skyPrimary)
            Text(verbatim: WalletMesh.shortURL(url)).skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyOnSurface).lineLimit(1).truncationMode(.middle)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 10)
                .accessibilityIdentifier("wallet-node-\(position)")
            MaterialIcon(cleartext ? MI.outlinedLockOpen : MI.outlinedLock, size: 14)
                .foregroundStyle(cleartext ? Color.skyWarning : Color.skyOnSurfaceVariant)
            control(MI.outlinedKeyboardArrowUp, L10n.key("wallet_node_up"), "wallet-node-up-\(position)", enabled: position > 1, onUp)
            control(MI.outlinedKeyboardArrowDown, L10n.key("wallet_node_down"), "wallet-node-down-\(position)", enabled: !last, onDown)
            control(MI.outlinedClose, L10n.key("wallet_node_remove"), "wallet-node-remove-\(position)", enabled: true, onRemove)
        }
        .padding(.leading, 14)
        .padding(.trailing, 2)
        .padding(.vertical, 2)
        .background(Color.skySurfaceVariant, in: .sky(SkyRadius.small))
        .padding(.vertical, 5)
    }

    private func control(_ icon: String, _ label: LocalizedStringKey, _ identifier: String, enabled: Bool, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            MaterialIcon(icon, size: 18).frame(width: 44, height: 44).contentShape(Rectangle())
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(Circle())))
        .foregroundStyle(enabled ? Color.skyOnSurface : Color.skyOnSurface.opacity(0.38))
        .disabled(!enabled)
        .accessibilityLabel(label)
        .accessibilityIdentifier(identifier)
    }
}

/// The shipped address, fixed as the last resort.
private struct ShippedNodeRow: View {
    let url: String

    var body: some View {
        HStack(spacing: 10) {
            MaterialIcon(MI.outlinedLock, size: 14)
            Text(verbatim: url).skyText(.bodyMedium).lineLimit(1).truncationMode(.middle).frame(maxWidth: .infinity, alignment: .leading)
            Text("wallet_node_shipped").skyText(.labelSmall)
        }
        .foregroundStyle(Color.skyOnSurfaceVariant)
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
        .background(Color.skySurfaceVariant.opacity(0.5), in: .sky(SkyRadius.small))
        .padding(.vertical, 5)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("wallet-node-shipped")
    }
}

/// Whether what is typed will be encrypted on the wire, said plainly.
private struct NodeTransportNote: View {
    let url: String

    var body: some View {
        let cleartext = !url.isEmpty && cleartextNode(url)
        HStack(alignment: .top, spacing: 9) {
            MaterialIcon(cleartext ? MI.outlinedLockOpen : MI.outlinedLock, size: 16)
            if WalletMesh.isMeshURL(url) {
                Text("wallet_node_mesh").skyText(.bodySmall)
            } else {
                Text(cleartext ? L10n.key("wallet_node_cleartext") : L10n.key("wallet_node_encrypted")).skyText(.bodySmall)
            }
        }
        .foregroundStyle(cleartext ? Color.skyWarning : Color.skyOnSurfaceVariant)
        .padding(.horizontal, 14)
        .padding(.vertical, 13)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(cleartext ? Color.skyWarning.opacity(0.12) : Color.skySurfaceVariant, in: .sky(SkyRadius.small))
        .padding(.top, 14)
    }
}

/// Saved addresses: one per line, receive then change, as skycoin-web writes them. No keys.
func addressesCsv(_ wallet: WalletMeta) -> String {
    (wallet.receiveAddresses + wallet.changeAddresses).joined(separator: "\n")
}

/// The CSV the save dialog writes.
struct AddressesDocument: FileDocument {
    static let readableContentTypes: [UTType] = [.commaSeparatedText]
    let text: String

    init(text: String) { self.text = text }

    init(configuration: ReadConfiguration) throws {
        text = configuration.file.regularFileContents.map { String(decoding: $0, as: UTF8.self) } ?? ""
    }

    func fileWrapper(configuration: WriteConfiguration) throws -> FileWrapper {
        FileWrapper(regularFileWithContents: Data(text.utf8))
    }
}

/// A BIP 44 wallet's account xpub and its receive and change chain xpubs; a tap copies one.
private struct XpubDialog: View {
    @EnvironmentObject private var dialogs: SkyDialogs
    let keys: ExtendedPublicKeys
    @State private var copied: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("wallet_xpub_action").skyText(.headlineSmall).foregroundStyle(Color.skyOnSurface)
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    Text("wallet_xpub_note").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                    let rows = [(L10n.text("wallet_xpub_account"), keys.account), (L10n.text("wallet_xpub_receive"), keys.receive),
                                (L10n.text("wallet_xpub_change"), keys.change)]
                    ForEach(Array(rows.enumerated()), id: \.offset) { index, row in
                        let (label, xpub) = row
                        Text(verbatim: label.uppercased()).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 14)
                        Button {
                            UIPasteboard.general.string = xpub
                            copied = xpub
                        } label: {
                            Text(verbatim: xpub).skyText(.bodySmall, mono: true).foregroundStyle(Color.skyOnSurface)
                                .multilineTextAlignment(.leading)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(10)
                                .background(Color.skyContainerHighest, in: .sky(10))
                        }
                        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: 10))))
                        .padding(.top, 4)
                        .accessibilityIdentifier("wallet-xpub-\(index)")
                        if copied == xpub {
                            Text("wallet_xpub_copied").skyText(.bodySmall).foregroundStyle(Color.skyPrimary).padding(.top, 4)
                        }
                    }
                }
            }
            .frame(maxHeight: 420)
            .fixedSize(horizontal: false, vertical: true)
            .padding(.top, 16)
            HStack {
                Spacer()
                Button { dialogs.dismiss() } label: { Text("wallet_done") }.buttonStyle(.skyText)
            }
            .padding(.top, 24)
        }
    }
}
