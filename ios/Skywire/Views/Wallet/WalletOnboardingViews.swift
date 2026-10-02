import SwiftUI
import UIKit
import WalletCore

// Create and restore (Android: ui/wallet/WalletOnboarding.kt).

/// The phrase backup: the numbered grid, covered whenever it could be
/// captured, no copy anywhere.
struct WalletSeedView: View {
    @EnvironmentObject private var model: WalletModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                Text("wallet_seed_body").foregroundStyle(.secondary)
                SeedPrivacyNote()
                SeedGrid(words: model.draft.seed?.split(separator: " ").map(String.init) ?? [])
                Button {
                    model.path.append(.verify)
                } label: {
                    Text("wallet_seed_continue").font(.body.weight(.semibold)).frame(maxWidth: .infinity, minHeight: 36)
                }
                .buttonStyle(.borderedProminent)
                .tint(.skywire)
                .accessibilityIdentifier("wallet-seed-continue")
            }
            .padding(20)
        }
        .navigationTitle(Text("wallet_seed_title"))
        .navigationBarTitleDisplayMode(.inline)
        .modifier(SeedPrivacy())
    }
}

/// What protects the words on this screen, said where the words are — in
/// iOS's terms, not Android's ("screenshots are disabled" is not something
/// iOS lets an app promise).
struct SeedPrivacyNote: View {
    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "eye.slash").font(.footnote)
            Text("wallet_seed_secure_ios").font(.footnote)
        }
        .foregroundStyle(.secondary)
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 12).fill(Color(.tertiarySystemFill)))
    }
}

/// The three-word quiz. Wrong fields name their position; all right
/// activates the wallet.
struct WalletVerifyView: View {
    @EnvironmentObject private var model: WalletModel
    @State private var answers: [Int: String] = [:]
    @State private var errors: [Int: String] = [:]

    var body: some View {
        Form {
            Section {
                Text("wallet_verify_body").foregroundStyle(.secondary)
            }
            ForEach(model.draft.quizPositions, id: \.self) { pos in
                Section {
                    TextField(L10n.text("wallet_verify_hint"), text: Binding(
                        get: { answers[pos] ?? "" },
                        set: { answers[pos] = $0; errors[pos] = nil }
                    ))
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("wallet-quiz-\(pos)")
                    if let error = errors[pos] {
                        Label(error, systemImage: "exclamationmark.circle").font(.footnote).foregroundStyle(.red)
                    }
                } header: {
                    Text(L10n.format("wallet_verify_word", pos))
                }
            }
            Section {
                Button {
                    errors = [:]
                    let wrong = model.submitQuiz(answers) {
                        model.path.removeAll()
                    }
                    for pos in wrong {
                        let typed = answers[pos]?.trimmingCharacters(in: .whitespaces) ?? ""
                        errors[pos] = typed.isEmpty
                            ? L10n.format("wallet_verify_missing", pos)
                            : L10n.format("wallet_verify_wrong", pos)
                    }
                } label: {
                    Text("wallet_verify_activate").font(.body.weight(.semibold)).frame(maxWidth: .infinity)
                }
                .accessibilityIdentifier("wallet-quiz-activate")
                Button {
                    // Back to the words: the screen under this one.
                    if model.path.last == .verify { model.path.removeLast() }
                } label: {
                    Text("wallet_verify_again").frame(maxWidth: .infinity)
                }
            }
        }
        .navigationTitle(Text("wallet_verify_title"))
        .navigationBarTitleDisplayMode(.inline)
        .modifier(SeedPrivacy())
    }
}

/// Restore: word by word with wordlist suggestions, or one paste of the
/// whole phrase.
struct WalletRestoreView: View {
    @EnvironmentObject private var model: WalletModel
    @State private var words: [String] = []
    @State private var input = ""
    @State private var error: String?
    @FocusState private var focused: Bool

    private var target: Int { words.count > 12 ? 24 : 12 }

    var body: some View {
        Form {
            Section {
                HStack {
                    Text(L10n.format("wallet_restore_progress", min(words.count + 1, target), target)).foregroundStyle(.secondary)
                    Spacer()
                    Button("wallet_restore_paste") { paste() }
                        .font(.body.weight(.semibold))
                        .accessibilityIdentifier("wallet-restore-paste")
                }
                ProgressView(value: Double(words.count), total: Double(target))
            }
            if let error {
                Section {
                    Label(error, systemImage: "exclamationmark.circle").font(.footnote).foregroundStyle(.red)
                        .accessibilityIdentifier("wallet-restore-error")
                }
            }
            if !words.isEmpty {
                Section {
                    // Entered words as numbered chips; tapping one takes it out.
                    WordChips(words: words) { words.remove(at: $0) }
                }
            }
            Section {
                TextField(L10n.text("wallet_restore_hint"), text: $input)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .focused($focused)
                    .onSubmit { addWord(input) }
                    .onChange(of: input) { new in
                        if new.hasSuffix(" ") && !new.trimmingCharacters(in: .whitespaces).isEmpty { addWord(new) }
                    }
                    .accessibilityIdentifier("wallet-restore-input")
                let suggestions = input.trimmingCharacters(in: .whitespaces).isEmpty ? [] : Bip39.suggestions(prefix: input, limit: 4)
                if !suggestions.isEmpty {
                    HStack(spacing: 8) {
                        ForEach(suggestions, id: \.self) { s in
                            Button(s) { addWord(s) }
                                .buttonStyle(.bordered)
                                .tint(.skywire)
                        }
                    }
                }
            } footer: {
                Text("wallet_restore_wordlist_note")
            }
            Section {
                Button {
                    finish()
                } label: {
                    HStack {
                        Spacer()
                        if model.restoring {
                            ProgressView().controlSize(.small)
                            Text("wallet_restore_scanning")
                        } else {
                            Text("wallet_restore_action").font(.body.weight(.semibold))
                        }
                        Spacer()
                    }
                }
                .disabled(model.restoring)
                .accessibilityIdentifier("wallet-restore-action")
            }
        }
        .navigationTitle(Text("wallet_restore_title"))
        .navigationBarTitleDisplayMode(.inline)
        .modifier(SeedPrivacy())
        .onAppear { focused = true }
    }

    /// Takes what was typed as words: one, or several at once when a
    /// phrase is typed or pasted into the field whole.
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

/// Numbered word chips that wrap across lines.
private struct WordChips: View {
    let words: [String]
    let onRemove: (Int) -> Void

    var body: some View {
        FlowLayout(spacing: 8) {
            ForEach(Array(words.enumerated()), id: \.offset) { index, word in
                Button {
                    onRemove(index)
                } label: {
                    HStack(spacing: 6) {
                        Text("\(index + 1)").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                        Text(word).font(.subheadline.weight(.semibold)).foregroundStyle(.primary)
                    }
                    .padding(.horizontal, 11)
                    .padding(.vertical, 7)
                    .background(Capsule().fill(Color(.tertiarySystemFill)))
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("wallet-restore-word-\(index + 1)")
            }
        }
        .padding(.vertical, 4)
    }
}

/// Lays children out left to right, wrapping to a new line when a row is
/// full (the chips of a 24-word phrase).
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
