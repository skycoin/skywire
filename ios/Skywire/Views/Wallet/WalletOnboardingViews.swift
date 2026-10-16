import SwiftUI
import UIKit
import WalletCore

// Create and restore (Android: ui/wallet/WalletOnboarding.kt).

/// The phrase backup: the numbered grid, covered whenever it could be captured, no copy.
struct WalletSeedView: View {
    @EnvironmentObject private var model: WalletModel

    var body: some View {
        WalletScreen(title: Text("wallet_seed_title")) {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    Text("wallet_seed_body").skyText(.bodyLarge).foregroundStyle(Color.skyOnSurfaceVariant)
                    SeedPrivacyNote().padding(.top, 16)
                    SeedGrid(words: model.draft.seed?.split(separator: " ").map(String.init) ?? []).padding(.top, 20)
                    Button { model.path.append(.verify) } label: { Text("wallet_seed_continue").frame(maxWidth: .infinity) }
                        .buttonStyle(FilledButtonStyle(height: 52))
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
    @State private var words: [String] = []
    @State private var input = ""
    @State private var error: String?

    private var target: Int { words.count > 12 ? 24 : 12 }

    var body: some View {
        WalletScreen(title: Text("wallet_restore_title")) {
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    HStack {
                        Text(verbatim: L10n.format("wallet_restore_progress", min(words.count + 1, target), target))
                            .skyText(.bodyLarge).foregroundStyle(Color.skyOnSurfaceVariant)
                        Spacer()
                        Button { paste() } label: { Text("wallet_restore_paste") }
                            .buttonStyle(.skyText)
                            .accessibilityIdentifier("wallet-restore-paste")
                    }
                    ProgressBar(value: Double(words.count) / Double(target)).padding(.top, 6)
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
                    // Android's FlowRow keeps its 18 pt top padding even with no chips.
                    WordChips(words: words) { words.remove(at: $0) }.padding(.top, 18)
                    SkyOutlinedTextField(placeholder: L10n.key("wallet_restore_hint"), text: $input,
                                         identifier: "wallet-restore-input", radius: SkyRadius.small)
                        .onSubmit { addWord(input) }
                        .onChange(of: input) { new in
                            if new.hasSuffix(" ") && !new.trimmingCharacters(in: .whitespaces).isEmpty { addWord(new) } else { error = nil }
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
                                Text("wallet_restore_action")
                            }
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(FilledButtonStyle(height: 52))
                    .disabled(model.restoring)
                    .padding(.top, 26)
                    .accessibilityIdentifier("wallet-restore-action")
                }
                .padding(.horizontal, 20)
                .padding(.bottom, 24)
            }
        }
        .modifier(SeedPrivacy())
    }

    /// Takes what was typed as words: one, or several when a phrase is typed or pasted whole.
    private func addWord(_ text: String) {
        let typed = text.lowercased().split(whereSeparator: { $0.isWhitespace }).map(String.init)
        guard !typed.isEmpty else { return }
        words.append(contentsOf: typed.prefix(24 - words.count))
        input = ""
        error = nil
    }

    private func paste() {
        let text = UIPasteboard.general.string ?? ""
        let pasted = text.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            .split(whereSeparator: { $0.isWhitespace }).map(String.init)
        if pasted.count == 12 || pasted.count == 24 {
            words = pasted
            error = nil
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
            error = L10n.text("wallet_restore_checksum")
            return
        }
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

/// Lays children out left to right, wrapping when a row is full (a 24-word phrase's chips).
struct FlowLayout: Layout {
    var spacing: CGFloat = 8

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, rowHeight: CGFloat = 0, widest: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x > 0 && x + size.width > width {
                y += rowHeight + spacing
                x = 0
                rowHeight = 0
            }
            x += size.width + spacing
            rowHeight = max(rowHeight, size.height)
            widest = max(widest, x - spacing)
        }
        return CGSize(width: min(widest, width), height: y + rowHeight)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, rowHeight: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x > bounds.minX && x + size.width > bounds.maxX {
                y += rowHeight + spacing
                x = bounds.minX
                rowHeight = 0
            }
            view.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
            x += size.width + spacing
            rowHeight = max(rowHeight, size.height)
        }
    }
}
