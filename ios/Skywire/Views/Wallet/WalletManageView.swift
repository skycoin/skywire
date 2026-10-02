import PhotosUI
import SwiftUI

/// The selected coin's wallets (Android: ui/wallet/WalletManage.kt): switch,
/// rename, reveal, remove, add.
struct WalletManageView: View {
    @EnvironmentObject private var model: WalletModel
    @State private var actionWallet: WalletMeta?
    @State private var renameTarget: WalletMeta?
    @State private var renameText = ""
    @State private var removeTarget: WalletMeta?

    var body: some View {
        List {
            Section {
                ForEach(model.coinWallets) { wallet in
                    Button { actionWallet = wallet } label: { row(wallet) }
                        .buttonStyle(.plain)
                        .accessibilityIdentifier("wallet-row-\(wallet.receiveAddresses.first ?? wallet.id)")
                }
            }
            Section {
                Button("wallet_manage_create") {
                    model.startCreate()
                    model.path.append(.create)
                }
                .accessibilityIdentifier("wallet-manage-create")
                Button("wallet_intro_restore") {
                    model.startRestore()
                    model.path.append(.restore)
                }
                .accessibilityIdentifier("wallet-manage-restore")
            }
        }
        .navigationTitle(Text("wallet_wallets_title"))
        .navigationBarTitleDisplayMode(.inline)
        .confirmationDialog(
            actionWallet?.name ?? "",
            isPresented: Binding(get: { actionWallet != nil }, set: { if !$0 { actionWallet = nil } }),
            titleVisibility: .visible,
            presenting: actionWallet
        ) { wallet in
            if wallet.id != model.active?.id {
                Button("wallet_use") { model.useWallet(wallet.id) }
            }
            Button("wallet_rename") {
                renameText = wallet.name
                renameTarget = wallet
            }
            // The reveal screen asks for Face ID / the passcode itself, once it
            // is on screen, and reads the phrase only after that passes.
            Button("wallet_reveal_action") { model.path.append(.reveal(wallet.id)) }
            Button("wallet_remove_action", role: .destructive) { removeTarget = wallet }
            Button("cancel", role: .cancel) {}
        } message: { wallet in
            Text(L10n.format("wallet_created_on", addressCount(wallet),
                             Date(timeIntervalSince1970: TimeInterval(wallet.createdAtMs) / 1000).formatted(date: .long, time: .omitted)))
        }
        .alert(Text("wallet_rename_title"), isPresented: Binding(get: { renameTarget != nil }, set: { if !$0 { renameTarget = nil } })) {
            TextField(L10n.text("wallet_rename_title"), text: $renameText)
            Button("cancel", role: .cancel) {}
            Button("save") {
                if let wallet = renameTarget, !renameText.trimmingCharacters(in: .whitespaces).isEmpty {
                    model.renameWallet(wallet.id, name: renameText)
                }
            }
        }
        .alert(
            Text(L10n.format("wallet_remove_title", removeTarget?.name ?? "")),
            isPresented: Binding(get: { removeTarget != nil }, set: { if !$0 { removeTarget = nil } }),
            presenting: removeTarget
        ) { wallet in
            Button("wallet_remove_confirm", role: .destructive) { model.removeWallet(wallet.id) }
            Button("wallet_remove_keep", role: .cancel) {}
        } message: { _ in
            Text("wallet_remove_body")
        }
    }

    private func row(_ wallet: WalletMeta) -> some View {
        HStack(spacing: 13) {
            Text(String(wallet.name.prefix(2)).uppercased())
                .font(.footnote.weight(.semibold))
                .foregroundStyle(Color.skywire)
                .frame(width: 38, height: 38)
                .background(Circle().fill(Color.skywire.opacity(0.15)))
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 8) {
                    Text(wallet.name).font(.body.weight(.semibold))
                    if wallet.id == model.active?.id {
                        Text("wallet_active_badge")
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(Color.skywire)
                            .padding(.horizontal, 7).padding(.vertical, 2)
                            .background(RoundedRectangle(cornerRadius: 8).fill(Color.skywire.opacity(0.15)))
                    }
                }
                Text("\(shortAddress(wallet.receiveAddresses.first ?? "", head: 6, tail: 3)) · \(addressCount(wallet))")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Image(systemName: "ellipsis").foregroundStyle(.secondary)
        }
        .contentShape(Rectangle())
    }

    private func addressCount(_ wallet: WalletMeta) -> String {
        let n = wallet.receiveAddresses.count
        return n == 1 ? L10n.text("wallet_address_count_one") : L10n.format("wallet_address_count_many", n)
    }
}

/// The phrase in the clear: read from the Keychain only once the device-owner
/// check has passed (back to the list if it does not), covered whenever it
/// could be captured, gone after two minutes.
struct WalletRevealView: View {
    @EnvironmentObject private var model: WalletModel
    let walletId: String
    @State private var seed: String?
    @State private var unavailable = false
    @State private var secondsLeft = 120

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                HStack(alignment: .top, spacing: 10) {
                    Image(systemName: "exclamationmark.triangle.fill")
                    Text(L10n.format("wallet_reveal_warning", model.allWallets.first { $0.id == walletId }?.name ?? ""))
                        .font(.footnote)
                }
                .foregroundStyle(.red)
                .padding(14)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(RoundedRectangle(cornerRadius: 14).fill(Color.red.opacity(0.1)))
                SeedPrivacyNote()
                if unavailable {
                    Text("wallet_seed_unavailable").foregroundStyle(.red)
                } else if let seed {
                    SeedGrid(words: seed.split(separator: " ").map(String.init))
                    Text(L10n.format("wallet_reveal_autohide", String(format: "%d:%02d", secondsLeft / 60, secondsLeft % 60)))
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity)
                }
                Button { close() } label: {
                    Text("wallet_reveal_hide").font(.body.weight(.semibold)).frame(maxWidth: .infinity, minHeight: 36)
                }
                .buttonStyle(.bordered)
                .tint(.skywire)
                .accessibilityIdentifier("wallet-reveal-hide")
            }
            .padding(20)
        }
        .navigationTitle(Text("wallet_seed_title"))
        .navigationBarTitleDisplayMode(.inline)
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

/// Add a Fibercoin (name, ticker, node URL) or an ERC-20 token (name,
/// ticker, contract, decimals), with an optional badge picked from Photos.
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

    var body: some View {
        Form {
            Section {
                Picker(selection: $kind) {
                    Text("wallet_add_kind_fiber").tag(AddCoinKind.fiber)
                    Text("wallet_add_kind_erc20").tag(AddCoinKind.erc20)
                } label: {
                    EmptyView()
                }
                .pickerStyle(.segmented)
                Text(kind == .fiber ? L10n.key("wallet_add_coin_body") : L10n.key("wallet_add_token_body"))
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }
            Section {
                TextField(L10n.text("wallet_add_coin_name_hint"), text: $name)
                    .accessibilityIdentifier("wallet-add-name")
            } header: {
                Text("wallet_add_coin_name")
            }
            Section {
                TextField(L10n.text("wallet_add_coin_ticker_hint"), text: $ticker)
                    .textInputAutocapitalization(.characters)
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("wallet-add-ticker")
            } header: {
                Text("wallet_add_coin_ticker")
            }
            Section {
                HStack(spacing: 12) {
                    iconPreview
                    PhotosPicker(selection: $photo, matching: .images) {
                        Text("wallet_add_coin_icon_pick")
                    }
                    .buttonStyle(.bordered)
                    if icon != nil {
                        Button("wallet_add_coin_icon_clear") { icon = nil }
                            .buttonStyle(.borderless)
                    }
                }
            } header: {
                Text("wallet_add_coin_icon")
            }
            if kind == .fiber {
                Section {
                    TextField(L10n.text("wallet_add_coin_node_hint"), text: $node)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .accessibilityIdentifier("wallet-add-node")
                } header: {
                    Text("wallet_add_coin_node")
                }
            } else {
                Section {
                    TextField(L10n.text("wallet_add_token_contract_hint"), text: $contract)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .font(.body.monospaced())
                        .accessibilityIdentifier("wallet-add-contract")
                } header: {
                    Text("wallet_add_token_contract")
                }
                Section {
                    TextField(L10n.text("wallet_add_token_decimals_hint"), text: $decimals)
                        .keyboardType(.numberPad)
                        .accessibilityIdentifier("wallet-add-decimals")
                } header: {
                    Text("wallet_add_token_decimals")
                }
            }
            Section {
                Button {
                    let done = { model.path.removeAll() }
                    if kind == .fiber {
                        model.addFiberCoin(name: name, ticker: ticker, nodeUrl: node, icon: icon, onDone: done)
                    } else {
                        model.addErc20Token(name: name, ticker: ticker, contract: contract, decimals: decimals, icon: icon, onDone: done)
                    }
                } label: {
                    Text("wallet_add_coin_save").font(.body.weight(.semibold)).frame(maxWidth: .infinity)
                }
                .disabled(!complete)
                .accessibilityIdentifier("wallet-add-save")
            }
        }
        .navigationTitle(Text("wallet_add_coin_title"))
        .navigationBarTitleDisplayMode(.inline)
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

    /// The badge the new coin will wear: the picked image, or the ticker's
    /// letters while there is none.
    private var iconPreview: some View {
        Group {
            if let icon, let image = CoinIcons.image(icon, in: model.store) {
                Image(uiImage: image).resizable().scaledToFill()
            } else {
                let letters = String(ticker.trimmingCharacters(in: .whitespaces).uppercased().prefix(3))
                Text(letters.isEmpty ? "ABC" : letters)
                    .font(.caption.weight(.bold))
                    .foregroundStyle(Color.skywire)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(Color(.tertiarySystemFill))
            }
        }
        .frame(width: 46, height: 46)
        .clipShape(Circle())
    }
}

/// Which node this coin's wallet talks to (Android: ui/wallet/WalletNode.kt).
/// Every balance, transaction list and broadcast of a fiber coin goes to one
/// daemon; the escape from a blocked or throttled one is to say where else to
/// look. Nothing secret is handed to whoever is named here — keys never leave
/// the phone and signing is local — but it does see which addresses are
/// asked about together, which is why the transport is spelled out.
struct WalletNodeView: View {
    @EnvironmentObject private var model: WalletModel
    @State private var url = ""

    var body: some View {
        Form {
            Section {
                Text(L10n.format("wallet_node_body", model.coin.name)).font(.subheadline).foregroundStyle(.secondary)
            }
            Section {
                TextField(model.defaultNodeUrl, text: $url)
                    .keyboardType(.URL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("wallet-node-url")
                // Said where the choice is made: an address typed here decides
                // whether the address book travels in the clear.
                let cleartext = url.trimmingCharacters(in: .whitespaces).lowercased().hasPrefix("http://")
                Label(cleartext ? L10n.key("wallet_node_cleartext") : L10n.key("wallet_node_encrypted"),
                      systemImage: cleartext ? "lock.open" : "lock")
                    .font(.footnote)
                    .foregroundStyle(cleartext ? Color.warning : .secondary)
            } header: {
                Text("wallet_node_field")
            }
            Section {
                Button {
                    model.setNodeUrl(url) { popNode() }
                } label: {
                    Text("wallet_node_save").font(.body.weight(.semibold)).frame(maxWidth: .infinity)
                }
                .disabled(url.trimmingCharacters(in: .whitespaces).isEmpty || url.trimmingCharacters(in: .whitespaces) == model.coin.nodeUrl)
                .accessibilityIdentifier("wallet-node-save")
                if model.coin.nodeUrl != model.defaultNodeUrl || url.trimmingCharacters(in: .whitespaces) != model.defaultNodeUrl {
                    Button {
                        model.setNodeUrl("") { popNode() }
                    } label: {
                        Text(L10n.format("wallet_node_default", model.defaultNodeUrl)).frame(maxWidth: .infinity)
                    }
                }
            }
        }
        .navigationTitle(Text("wallet_node_title"))
        .navigationBarTitleDisplayMode(.inline)
        // Seeded from the address in force, so the field opens on what is
        // actually used rather than on an empty box.
        .onAppear { url = model.coin.nodeUrl }
    }

    private func popNode() {
        if model.path.last == .node { model.path.removeLast() }
    }
}
