import SwiftUI
import UIKit
import WalletCore

// Create and restore (Android: ui/wallet/WalletOnboarding.kt).

/// The phrase backup: the numbered grid, covered whenever it could be captured, no copy.
struct WalletSeedView: View {
    @EnvironmentObject private var model: WalletModel
    @State private var passphraseAgain = ""

    var body: some View {
        WalletScreen(title: Text("wallet_seed_title")) {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    let words = model.draft.seed?.split(separator: " ").map(String.init) ?? []
                    Text("wallet_seed_body").skyText(.bodyLarge).foregroundStyle(Color.skyOnSurfaceVariant)
                    SeedPrivacyNote().padding(.top, 16)
                    // A new length is a new phrase: the words shown are always the ones the wallet gets.
                    SkySegmentedRow(items: [12, 24], selected: words.count, label: {
                        Text($0 == 24 ? L10n.key("wallet_seed_words_24") : L10n.key("wallet_seed_words_12"))
                    }, onSelect: { count in
                        if count != words.count { model.startCreate(words: count, keepChoices: true) }
                    })
                    .padding(.top, 16)
                    .accessibilityIdentifier("wallet-seed-length")
                    DerivationChoice()
                    SeedGrid(words: words).padding(.top, 20)
                    PassphraseFields(confirm: true, again: $passphraseAgain)
                    Button { model.path.append(.verify) } label: { Text("wallet_seed_continue").frame(maxWidth: .infinity) }
                        .buttonStyle(FilledButtonStyle(height: 52))
                        .disabled(!PassphraseFields.usable(model.draft, confirm: true, again: passphraseAgain))
                        .padding(.top, 28)
                        .padding(.bottom, 24)
                        .accessibilityIdentifier("wallet-seed-continue")
                }
                .padding(.horizontal, 20)
            }
        }
        .modifier(SeedPrivacy())
    }
}

/// Two kinds of wallet, as skycoin-web offers them: for fiber coins the classic deterministic one
/// or BIP 44, for Bitcoin native segwit or legacy P2PKH. The phrase is the same; the addresses are
/// not, so a restore has to name the kind the wallet was made as.
struct DerivationChoice: View {
    @EnvironmentObject private var model: WalletModel

    var body: some View {
        let kind = model.draft.coin.kind
        if kind == .skyFiber || kind == .btc {
            let btc = kind == .btc
            VStack(alignment: .leading, spacing: 8) {
                Text(verbatim: L10n.text("wallet_derivation_title").uppercased()).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
                SkySegmentedRow(items: [Derivation.standard, .bip44], selected: model.draft.derivation, label: { d in
                    switch (btc, d) {
                    case (true, .bip44): Text("wallet_derivation_btc_legacy")
                    case (true, _): Text("wallet_derivation_btc_segwit")
                    case (false, .bip44): Text("wallet_derivation_bip44")
                    case (false, _): Text("wallet_derivation_standard")
                    }
                }, onSelect: model.setDraftDerivation)
                .accessibilityIdentifier("wallet-derivation")
                Text(btc ? L10n.key("wallet_derivation_note_btc") : L10n.key("wallet_derivation_note"))
                    .skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
            }
            .padding(.top, 18)
        }
    }
}

/// The optional BIP 39 passphrase, as skycoin-web offers it. Every wallet but the classic fiber
/// one runs the phrase through BIP 39, and there it changes every key. Creating asks for it twice,
/// since a typo would lose the wallet.
struct PassphraseFields: View {
    @EnvironmentObject private var model: WalletModel
    let confirm: Bool
    @Binding var again: String

    /// Whether what is typed can be used: no passphrase where it has no part, or a confirmed one.
    static func usable(_ draft: DraftState, confirm: Bool, again: String) -> Bool {
        !applies(draft) || !confirm || draft.passphrase == again
    }

    static func applies(_ draft: DraftState) -> Bool {
        !(draft.coin.kind == .skyFiber && draft.derivation == .standard)
    }

    var body: some View {
        let draft = model.draft
        if Self.applies(draft) {
            let mismatch = confirm && draft.passphrase != again && !again.isEmpty
            VStack(alignment: .leading, spacing: 8) {
                Text(verbatim: L10n.text("wallet_passphrase_title").uppercased()).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
                SkyOutlinedTextField(placeholder: L10n.key("wallet_passphrase_hint"),
                                     // A closure, not the method: Swift 6.3's IRGen crashes on the method's isolated(any) thunk.
                                     text: Binding(get: { model.draft.passphrase }, set: { model.setDraftPassphrase($0) }),
                                     identifier: "wallet-passphrase", radius: SkyRadius.small, secure: true)
                if confirm && !draft.passphrase.isEmpty {
                    SkyOutlinedTextField(placeholder: L10n.key("wallet_passphrase_confirm"), text: $again, isError: mismatch,
                                         identifier: "wallet-passphrase-again", radius: SkyRadius.small, secure: true)
                }
                Text(mismatch ? L10n.key("wallet_passphrase_mismatch") : L10n.key("wallet_passphrase_note"))
                    .skyText(.bodySmall).foregroundStyle(mismatch ? Color.skyError : Color.skyOnSurfaceVariant)
            }
            .padding(.top, 22)
        }
    }
}

/// What protects the words here, in iOS's terms: iOS lets no app promise that screenshots are off.
struct SeedPrivacyNote: View {
    var body: some View {
        HStack(spacing: 8) {
            MaterialIcon(MI.outlinedScreenshotMonitor, size: 16)
            Text("wallet_seed_secure_ios").skyText(.bodySmall)
        }
        .foregroundStyle(Color.skyOnSurfaceVariant)
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.skyContainerHighest, in: .sky(SkyRadius.small))
    }
}

/// The three-word quiz. Wrong fields name their position; all right activates the wallet.
struct WalletVerifyView: View {
    @EnvironmentObject private var model: WalletModel
    @State private var answers: [Int: String] = [:]
    @State private var errors: [Int: String] = [:]

    var body: some View {
        WalletScreen(title: Text("wallet_verify_title")) {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    Text("wallet_verify_body").skyText(.bodyLarge).foregroundStyle(Color.skyOnSurfaceVariant).padding(.bottom, 26)
                    ForEach(model.draft.quizPositions, id: \.self) { pos in
                        VStack(alignment: .leading, spacing: 0) {
                            Text(verbatim: L10n.format("wallet_verify_word", pos)).skyText(.labelLarge).foregroundStyle(Color.skyOnSurfaceVariant)
                                .padding(.bottom, 7)
                            SkyOutlinedTextField(placeholder: L10n.key("wallet_verify_hint"), text: Binding(
                                get: { answers[pos] ?? "" },
                                set: { answers[pos] = $0; errors[pos] = nil }
                            ), isError: errors[pos] != nil, identifier: "wallet-quiz-\(pos)", radius: SkyRadius.small)
                            if let error = errors[pos] {
                                HStack(spacing: 6) {
                                    MaterialIcon(MI.outlinedErrorOutline, size: 14)
                                    Text(verbatim: error).skyText(.bodySmall)
                                }
                                .foregroundStyle(Color.skyError)
                                .padding(.top, 7)
                            }
                        }
                        .padding(.bottom, 14)
                    }
                    Button { activate() } label: { Text("wallet_verify_activate").frame(maxWidth: .infinity) }
                        .buttonStyle(FilledButtonStyle(height: 52))
                        .padding(.top, 16)
                        .accessibilityIdentifier("wallet-quiz-activate")
                    Button {
                        // Back to the words: the screen under this one.
                        if model.path.last == .verify { model.path.removeLast() }
                    } label: { Text("wallet_verify_again").frame(maxWidth: .infinity) }
                        .buttonStyle(.skyText)
                        .padding(.top, 6)
                        .padding(.bottom, 24)
                }
                .padding(.horizontal, 20)
            }
        }
        .modifier(SeedPrivacy())
    }

    private func activate() {
        errors = [:]
        let wrong = model.submitQuiz(answers) { model.path.removeAll() }
        for pos in wrong {
            let typed = answers[pos]?.trimmingCharacters(in: .whitespaces) ?? ""
            errors[pos] = typed.isEmpty ? L10n.format("wallet_verify_missing", pos) : L10n.format("wallet_verify_wrong", pos)
        }
    }
}

/// Restore: word by word with wordlist suggestions, or one paste of the whole phrase.
struct WalletRestoreView: View {
    @EnvironmentObject private var model: WalletModel
    @State private var noAgain = ""
    /// A classic fiber wallet takes any text as its seed; skycoin-web allows it after a warning.
    @State private var customMode = false
    @State private var customText = ""
    @State private var customAccepted = false
    @State private var words: [String] = []
    @State private var input = ""
    @State private var error: String?
    /// The phrase failed its checksum but reads as an older wallet's seed: the next press restores it.
    @State private var legacyOffer = false

    private var target: Int { words.count > 12 ? 24 : 12 }

    var body: some View {
        WalletScreen(title: Text("wallet_restore_title")) {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    HStack {
                        // Until a 13th word settles it, the phrase may be either length.
                        Text(verbatim: L10n.format("wallet_restore_progress", min(words.count + 1, target), target == 24 ? "24" : "12/24"))
                            .skyText(.bodyLarge).foregroundStyle(Color.skyOnSurfaceVariant)
                        Spacer()
                        Button { paste() } label: { Text("wallet_restore_paste") }
                            .buttonStyle(.skyText)
                            .accessibilityIdentifier("wallet-restore-paste")
                    }
                    ProgressBar(value: Double(words.count) / Double(target)).padding(.top, 6)
                    DerivationChoice()
                    PassphraseFields(confirm: false, again: $noAgain)
                    if let error {
                        HStack(alignment: .top, spacing: 9) {
                            MaterialIcon(MI.outlinedErrorOutline, size: 16)
                            Text(verbatim: error).skyText(.bodySmall)
                        }
                        .foregroundStyle(Color.skyOnErrorContainer)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 13)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Color.skyErrorContainer, in: .sky(SkyRadius.small))
                        .padding(.top, 16)
                        .accessibilityIdentifier("wallet-restore-error")
                    }
                    if customMode && anyText {
                        customSeed
                    } else {
                        wordEntry
                    }
                }
                .padding(.horizontal, 20)
                .padding(.bottom, 24)
            }
        }
        .modifier(SeedPrivacy())
    }

    private var anyText: Bool { model.draft.coin.kind == .skyFiber && model.draft.derivation == .standard }

    /// A non-standard seed: exactly as it was written, after a warning the user accepts.
    @ViewBuilder private var customSeed: some View {
        SkyOutlinedTextField(placeholder: L10n.key("wallet_restore_custom_hint"),
                             text: Binding(get: { customText }, set: { customText = $0; customAccepted = false }),
                             identifier: "wallet-restore-custom", radius: SkyRadius.small, minLines: 2)
            .padding(.top, 18)
        Text("wallet_restore_custom_warning").skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 12)
        Button { customAccepted.toggle() } label: {
            HStack(spacing: 8) {
                SkyCheckbox(checked: customAccepted)
                Text("wallet_restore_custom_accept").skyText(.bodyMedium).foregroundStyle(Color.skyOnSurface)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface))
        .padding(.top, 8)
        .accessibilityAddTraits(customAccepted ? .isSelected : [])
        .accessibilityIdentifier("wallet-restore-custom-accept")
        Button {
            // Exactly as typed: no trimming or case folding, since every byte is part of the seed.
            model.restoreWallet(customText, exact: true) { model.path.removeAll() }
        } label: {
            Text(model.restoring ? L10n.key("wallet_restore_scanning") : L10n.key("wallet_restore_action")).frame(maxWidth: .infinity)
        }
        .buttonStyle(FilledButtonStyle(height: 52))
        .disabled(!customAccepted || customText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.restoring)
        .padding(.top, 18)
        .accessibilityIdentifier("wallet-restore-custom-action")
        Button { customMode = false } label: { Text("wallet_restore_custom_back").frame(maxWidth: .infinity) }
            .buttonStyle(.skyText)
            .padding(.top, 6)
    }

    @ViewBuilder private var wordEntry: some View {
        // Android's FlowRow keeps its 18 pt top padding even with no chips.
        WordChips(words: words) { words.remove(at: $0); legacyOffer = false }.padding(.top, 18)
        SkyOutlinedTextField(placeholder: L10n.key("wallet_restore_hint"), text: $input,
                             identifier: "wallet-restore-input", radius: SkyRadius.small)
            .onSubmit { addWord(input) }
            .onChange(of: input) { new in
                if new.hasSuffix(" ") && !new.trimmingCharacters(in: .whitespaces).isEmpty {
                    addWord(new)
                } else {
                    error = nil
                    legacyOffer = false
                }
            }
            .padding(.top, 16)
        let suggestions = input.trimmingCharacters(in: .whitespaces).isEmpty ? [] : Bip39.suggestions(prefix: input, limit: 4)
        if !suggestions.isEmpty {
            HStack(spacing: 8) {
                ForEach(suggestions, id: \.self) { word in
                    Button { addWord(word) } label: {
                        Text(verbatim: word).skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyPrimary)
                            .padding(.horizontal, 14).padding(.vertical, 9)
                            .background(Color.skySecondaryContainer, in: .sky(18))
                    }
                    .buttonStyle(PressStyle())
                }
            }
            .padding(.top, 12)
        }
        Text("wallet_restore_wordlist_note").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 14)
        Button { finish() } label: {
            HStack(spacing: 10) {
                if model.restoring {
                    MaterialSpinner(size: 16, stroke: 2, color: .skyOnSurface.opacity(0.38))
                    Text("wallet_restore_scanning")
                } else {
                    // The second press of the same button is the confirmation: the
                    // offer above it says what is being agreed to.
                    Text(legacyOffer ? L10n.key("wallet_restore_anyway") : L10n.key("wallet_restore_action"))
                }
            }
            .frame(maxWidth: .infinity)
        }
        .buttonStyle(FilledButtonStyle(height: 52))
        .disabled(model.restoring)
        .padding(.top, 26)
        .accessibilityIdentifier("wallet-restore-action")
        if anyText {
            Button { customMode = true } label: { Text("wallet_restore_custom_open").frame(maxWidth: .infinity) }
                .buttonStyle(.skyText)
                .padding(.top, 6)
                .accessibilityIdentifier("wallet-restore-custom-open")
        }
    }

    /// Takes what was typed as words: one, or several when a phrase is typed or pasted whole.
    private func addWord(_ text: String) {
        let typed = text.lowercased().split(whereSeparator: { $0.isWhitespace }).map(String.init)
        guard !typed.isEmpty else { return }
        words.append(contentsOf: typed.prefix(24 - words.count))
        input = ""
        error = nil
        legacyOffer = false
    }

    private func paste() {
        let text = UIPasteboard.general.string ?? ""
        let pasted = text.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            .split(whereSeparator: { $0.isWhitespace }).map(String.init)
        if pasted.count == 12 || pasted.count == 24 {
            words = pasted
            error = nil
            legacyOffer = false
        } else if !pasted.isEmpty {
            error = L10n.format("wallet_restore_short", pasted.count)
        }
    }

    private func finish() {
        guard words.count == 12 || words.count == 24 else {
            error = L10n.format("wallet_restore_short", words.count)
            return
        }
        let phrase = words.joined(separator: " ")
        guard Bip39.validate(phrase) else {
            // A 24-word phrase of list words without a checksum is how the older wallets wrote
            // seeds, and refusing them strands those wallets. Offer the restore with the typo risk
            // said plainly; 12-word phrases were never written without a checksum. Only the fiber
            // chains derive from the phrase itself.
            let legacy = words.count == 24 && Bip39.legacyWords(phrase) && anyText
            if legacy && legacyOffer {
                legacyOffer = false
                model.restoreWallet(phrase, legacy: true) { model.path.removeAll() }
            } else if legacy {
                error = L10n.text("wallet_restore_legacy")
                legacyOffer = true
            } else {
                error = L10n.text("wallet_restore_checksum")
                legacyOffer = false
            }
            return
        }
        legacyOffer = false
        model.restoreWallet(phrase) { model.path.removeAll() }
    }
}

/// LinearProgressIndicator, determinate, 3 pt: primary over secondaryContainer with Material's gap.
struct ProgressBar: View {
    let value: Double

    var body: some View {
        GeometryReader { geometry in
            let filled = geometry.size.width * min(max(value, 0), 1)
            HStack(spacing: 4) {
                if filled > 0 { Capsule().fill(Color.skyPrimary).frame(width: filled) }
                Capsule().fill(Color.skySecondaryContainer)
            }
        }
        .frame(height: 3)
    }
}

/// Numbered word chips that wrap; tapping one takes it out.
private struct WordChips: View {
    let words: [String]
    let onRemove: (Int) -> Void

    var body: some View {
        FlowLayout(spacing: 8) {
            ForEach(Array(words.enumerated()), id: \.offset) { index, word in
                Button { onRemove(index) } label: {
                    HStack(spacing: 7) {
                        Text(verbatim: "\(index + 1)").skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
                        Text(verbatim: word).skyText(.bodyMedium, bold: true).foregroundStyle(Color.skyOnSurface)
                    }
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
                    .background(Color.skySurfaceVariant, in: .sky(20))
                }
                .buttonStyle(PressStyle())
                .accessibilityIdentifier("wallet-restore-word-\(index + 1)")
            }
        }
    }
}

/// Lays children out left to right, wrapping when a row is full (a 24-word phrase's chips,
/// the mail screen's buttons); `centered` centres each row, as Compose's FlowRow can.
struct FlowLayout: Layout {
    var spacing: CGFloat = 8
    var lineSpacing: CGFloat?
    var centered = false

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        let lines = rows(subviews, width: width)
        let widest = lines.map(\.width).max() ?? 0
        let height = lines.map(\.height).reduce(0, +) + CGFloat(max(0, lines.count - 1)) * (lineSpacing ?? spacing)
        return CGSize(width: centered && width.isFinite ? width : min(widest, width), height: height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var y = bounds.minY
        for line in rows(subviews, width: bounds.width) {
            var x = centered ? bounds.minX + (bounds.width - line.width) / 2 : bounds.minX
            for index in line.indices {
                let size = subviews[index].sizeThatFits(.unspecified)
                subviews[index].place(at: CGPoint(x: x, y: y + (line.height - size.height) / 2), proposal: ProposedViewSize(size))
                x += size.width + spacing
            }
            y += line.height + (lineSpacing ?? spacing)
        }
    }

    private struct Line {
        var indices: [Int] = []
        var width: CGFloat = 0
        var height: CGFloat = 0
    }

    private func rows(_ subviews: Subviews, width: CGFloat) -> [Line] {
        var lines: [Line] = []
        var line = Line()
        for (index, view) in subviews.enumerated() {
            let size = view.sizeThatFits(.unspecified)
            if !line.indices.isEmpty && line.width + spacing + size.width > width {
                lines.append(line)
                line = Line()
            }
            line.width += (line.indices.isEmpty ? 0 : spacing) + size.width
            line.height = max(line.height, size.height)
            line.indices.append(index)
        }
        if !line.indices.isEmpty { lines.append(line) }
        return lines
    }
}
