import CoreClient
import PhotosUI
import SwiftUI

// Pieces every chat screen shares (Android: ui/chat/ChatWidgets.kt, ChatDialogs.kt
// and the small helpers of ChatHome.kt and GroupScreens.kt).

/// A stable colour for a key, so a person keeps the same tint everywhere.
func chatKeyColor(_ key: String) -> Color {
    var h: Int32 = 0
    for unit in key.utf16 { h = h &* 31 &+ Int32(unit) }
    let hue = Double(Int(UInt32(bitPattern: h) >> 1) % 360) / 360
    return Color(hue: hue, saturation: 0.55, brightness: 0.78)
}

/// First letters of a name, or the first two hex digits of a key.
func chatInitials(_ label: String, key: String) -> String {
    let name = label.trimmingCharacters(in: .whitespaces)
    if !name.isEmpty && !name.hasPrefix(String(key.prefix(8))) {
        return name.split(separator: " ").prefix(2).compactMap { $0.first.map(String.init) }.joined().uppercased()
    }
    return String(key.prefix(2)).uppercased()
}

enum AvatarKind {
    case person, group, channel, saved
}

/// A round avatar: `load` supplies the picture (local first, then published),
/// and until it does, or when there is none, a tinted disc with initials.
struct ChatAvatar: View {
    let key: String
    let label: String
    let size: CGFloat
    var kind: AvatarKind = .person
    /// Anything that changes when the picture may have.
    var version: Int = 0
    var online: Bool?
    var load: (@MainActor () async -> Data?)?

    @State private var image: UIImage?

    private var cacheKey: String { "avatar:\(key):\(version)" }

    var body: some View {
        ZStack(alignment: .bottomTrailing) {
            if let image = image ?? ChatImages.cached(cacheKey) {
                Image(uiImage: image).resizable().scaledToFill().frame(width: size, height: size).clipShape(Circle())
            } else {
                Circle().fill(kind == .saved ? Color.skyPrimary : chatKeyColor(key)).frame(width: size, height: size)
                    .overlay {
                        switch kind {
                        case .saved: MaterialIcon(MI.roundedBookmark, size: size * 0.5).foregroundStyle(.white)
                        case .group: MaterialIcon(MI.roundedGroups, size: size * 0.55).foregroundStyle(.white)
                        case .channel: MaterialIcon(MI.roundedCampaign, size: size * 0.55).foregroundStyle(.white)
                        case .person:
                            Text(verbatim: chatInitials(label, key: key))
                                .font(.system(size: size * 0.38, weight: .bold))
                                .foregroundStyle(.white)
                        }
                    }
            }
            if let online {
                Circle().fill(online ? Color.skySuccess : Color(hex: 0x9AA5B5))
                    .frame(width: size * 0.28, height: size * 0.28)
                    .overlay(Circle().stroke(Color.skyBackground, lineWidth: 2))
            }
        }
        .frame(width: size, height: size)
        .accessibilityHidden(true)
        .task(id: cacheKey) {
            if ChatImages.cached(cacheKey) != nil { return }
            image = nil
            guard let load, let bytes = await load() else { return }
            let decoded = await Task.detached { ChatImages.decode(bytes, maxEdge: 256) }.value
            guard let decoded else { return }
            ChatImages.remember(cacheKey, decoded)
            image = decoded
        }
    }
}

/// Bars of a voice message; `progress` 0…1 colours the played part. Tap seeks.
struct Waveform: View {
    let peaks: [Float]?
    let progress: Double
    let played: Color
    let rest: Color
    var onSeek: ((Double) -> Void)?

    static let fallback: [Float] = (0..<36).map { 0.25 + 0.2 * abs(sin(Float($0) * 0.7)) }

    var body: some View {
        GeometryReader { geo in
            let bars = peaks ?? Self.fallback
            let gap: CGFloat = 2
            let n = CGFloat(max(1, bars.count))
            let w = max(1, (geo.size.width - gap * (n - 1)) / n)
            ZStack(alignment: .topLeading) {
                ForEach(Array(bars.enumerated()), id: \.offset) { i, value in
                    let h = max(w, CGFloat(min(1, max(0.06, value))) * geo.size.height)
                    RoundedRectangle(cornerRadius: w / 2)
                        .fill((Double(i) + 0.5) / Double(n) <= progress ? played : rest)
                        .frame(width: w, height: h)
                        .offset(x: CGFloat(i) * (w + gap), y: (geo.size.height - h) / 2)
                }
            }
            .contentShape(Rectangle())
            .gesture(DragGesture(minimumDistance: 0).onEnded { value in
                onSeek?(min(1, max(0, value.location.x / max(1, geo.size.width))))
            }, including: onSeek == nil ? .none : .all)
        }
    }
}

// MARK: Text helpers

enum ChatText {
    /// Localises the stock captions that travel in English (index.html _shownText).
    static func shown(_ text: String) -> String {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if t == "🎤 Voice message" { return "🎤 " + L10n.text("chat_voice_message") }
        if t == "📹 Video message" { return "📹 " + L10n.text("chat_video_message") }
        if let match = t.range(of: #"^📞 (Missed call|Incoming call|Outgoing call)( · \d+:\d\d)?$"#, options: .regularExpression) {
            let body = String(t[match].dropFirst(2))
            for outcome in ["Missed call", "Incoming call", "Outgoing call"] where body.hasPrefix(outcome) {
                return "📞 " + outcomeLabel(outcome) + body.dropFirst(outcome.count)
            }
        }
        return text
    }

    static func outcomeLabel(_ outcome: String) -> String {
        switch outcome {
        case "Missed call": L10n.text("call_missed_title")
        case "Incoming call": L10n.text("chat_call_incoming")
        case "Outgoing call": L10n.text("chat_call_outgoing")
        default: outcome
        }
    }

    /// `↑ `/`↓ ` and the shown text, the way list rows preview (index.html updatePreview).
    static func preview(_ m: ChatMessage, own: Bool, group: Bool = false, sender: String = "") -> String {
        let text: String
        if let f = m.file {
            if f.isVoiceMessage {
                text = "🎤 " + L10n.text("chat_voice_message")
            } else if f.isVideoMessage {
                text = "📹 " + L10n.text("chat_video_message")
            } else if !f.name.isEmpty && f.name != "file" {
                text = "📎 " + f.name
            } else {
                text = shown(m.text)
            }
        } else {
            text = shown(m.text)
        }
        if !own { return text }
        if m.mine { return "↑ " + text }
        return group ? "\(sender): \(text)" : "↓ " + text
    }

    /// The clock today, the weekday this week, the date before.
    static func listTime(_ ms: Int64) -> String {
        let date = Date(timeIntervalSince1970: Double(ms) / 1000)
        let calendar = Calendar.current
        let formatter = DateFormatter()
        formatter.locale = L10n.locale
        if calendar.isDateInToday(date) {
            formatter.dateFormat = "HH:mm"
        } else if let week = calendar.date(byAdding: .day, value: -6, to: calendar.startOfDay(for: Date())), date >= week {
            formatter.dateFormat = "EEE"
        } else {
            formatter.dateStyle = .short
        }
        return formatter.string(from: date)
    }

    static func clock(_ ms: Int64) -> String {
        let s = max(0, ms / 1000)
        return String(format: "%d:%02d", s / 60, s % 60)
    }

    /// `role · N members · kind · state`, the line under a group's name (index.html _groupSubtitle).
    static func groupSubtitle(_ g: GroupInfo) -> String {
        var parts: [String] = []
        if g.role.isEmpty || g.role == "member" {
            parts.append(L10n.text("chat_role_member"))
        } else if g.role == "owner" {
            parts.append(L10n.text("chat_role_owner"))
        } else {
            parts.append(g.role)
        }
        let n = String(g.members.count)
        parts.append(g.members.count == 1 ? L10n.format("chat_members_one", n) : L10n.format("chat_members_many", n))
        if g.isChannel {
            parts.append(L10n.text("chat_kind_channel"))
        } else if g.approval {
            parts.append(L10n.text("chat_kind_private"))
        } else {
            parts.append(L10n.text("chat_kind_public"))
        }
        if g.status == "awaiting_approval" {
            parts.append(L10n.text("chat_state_waiting"))
        } else if g.status == "denied" {
            parts.append(L10n.text("chat_state_declined"))
        } else if g.status == "banned" {
            parts.append(L10n.text("chat_state_banned"))
        } else if g.readOnly {
            parts.append(L10n.text("chat_state_read_only"))
        } else if g.canPost == false {
            parts.append(L10n.text("chat_state_restricted"))
        }
        return parts.joined(separator: " · ")
    }

    static func humanDuration(_ seconds: Int64) -> String {
        let s = abs(seconds)
        if s < 60 { return L10n.text("chat_dur_under_minute") }
        if s < 3600 { return L10n.format("chat_dur_min", String(s / 60)) }
        if s < 86400 {
            let hours = s / 3600
            return hours == 1 ? L10n.format("chat_dur_hour", String(hours)) : L10n.format("chat_dur_hours", String(hours))
        }
        let days = s / 86400
        return days == 1 ? L10n.format("chat_dur_day", String(days)) : L10n.format("chat_dur_days", String(days))
    }
}

// MARK: Small views

struct ChatHint: View {
    let text: String

    var body: some View {
        Text(verbatim: text)
            .skyText(.bodyMedium)
            .foregroundStyle(Color.skyOnSurfaceVariant)
            .multilineTextAlignment(.center)
            .frame(maxWidth: .infinity)
            .padding(32)
    }
}

struct ChatHelp: View {
    let text: String

    var body: some View {
        Text(verbatim: text)
            .skyText(.bodySmall)
            .foregroundStyle(Color.skyOnSurfaceVariant)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.vertical, 4)
    }
}

struct ChatSectionTitle: View {
    let text: String

    var body: some View {
        Text(verbatim: text)
            .skyText(.labelLarge)
            .foregroundStyle(Color.skyPrimary)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.leading, 20)
            .padding(.top, 14)
            .padding(.bottom, 4)
    }
}

struct ChatCheckRow: View {
    let label: String
    let checked: Bool
    let onChange: (Bool) -> Void

    var body: some View {
        Button {
            onChange(!checked)
        } label: {
            HStack(spacing: 12) {
                SkyCheckbox(checked: checked)
                Text(verbatim: label).skyText(.bodyLarge).foregroundStyle(Color.skyOnSurface)
                Spacer(minLength: 0)
            }
            .padding(.vertical, 8)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(checked ? .isSelected : [])
    }
}

struct ChatSwitchRow: View {
    let title: String
    var sub: String?
    let checked: Bool
    var enabled = true
    let onChange: (Bool) -> Void

    var body: some View {
        Toggle(isOn: Binding(get: { checked }, set: { onChange($0) })) {
            VStack(alignment: .leading, spacing: 2) {
                Text(verbatim: title).skyText(.bodyLarge).foregroundStyle(Color.skyOnSurface)
                if let sub {
                    Text(verbatim: sub).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
            }
        }
        .toggleStyle(.skySwitch)
        .disabled(!enabled)
        .padding(.vertical, 6)
    }
}

struct ChatDivider: View {
    var body: some View {
        Rectangle().fill(Color.skyOutlineVariant).frame(height: 1).padding(.vertical, 12)
    }
}

/// A plain row: avatar, title and an optional line under it.
struct ChatSimpleRow: View {
    let key: String
    let title: String
    let sub: String
    let kind: AvatarKind
    var load: (@MainActor () async -> Data?)?
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 12) {
                ChatAvatar(key: key, label: title, size: 40, kind: kind, load: load)
                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: title).skyText(.titleSmall).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                    if !sub.isEmpty {
                        Text(verbatim: sub).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(1)
                    }
                }
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

/// A list's search field.
struct ChatSearchField: View {
    let placeholder: String
    @Binding var text: String
    var focus = false
    @FocusState private var focused: Bool

    var body: some View {
        HStack(spacing: 8) {
            MaterialIcon(MI.roundedSearch, size: 20).foregroundStyle(Color.skyOnSurfaceVariant)
            TextField(text: $text) { Text(verbatim: placeholder) }
                .skyText(.bodyLarge)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .focused($focused)
        }
        .padding(.horizontal, 14)
        .frame(height: 48)
        .background(Color.skyContainerHighest, in: .sky(SkyRadius.large))
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .onAppear { if focus { focused = true } }
    }
}

extension SkyDialogs {
    /// Yes or no before something that cannot be undone.
    func confirm(_ text: String, confirm label: String, cancel: String? = nil, danger: Bool = true, _ action: @escaping () -> Void) {
        show(SkyDialog(message: Text(verbatim: text), actions: [
            SkyDialog.Action(label: cancel.map { Text(verbatim: $0) } ?? Text("cancel")),
            SkyDialog.Action(label: Text(verbatim: label), destructive: danger, handler: action),
        ]))
    }
}

// MARK: QR

/// An address to show as a QR code; `inviteGid` offers the invite link instead (admins).
struct ChatQrSpec {
    let address: String
    let title: String
    let help: String
    var inviteGid: String?
}

struct ChatQrDialog: View {
    let spec: ChatQrSpec
    @ObservedObject var repo: ChatRepository
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(verbatim: spec.title).skyText(.headlineSmall).foregroundStyle(Color.skyOnSurface)
            if spec.address.count <= 600 {
                QRCodeImage(content: spec.address)
                    .frame(width: 232, height: 232)
                    .padding(8)
                    .background(Color.white, in: .sky(SkyRadius.small))
                    .frame(maxWidth: .infinity)
            } else {
                Text("chat_qr_too_long").skyText(.bodySmall)
            }
            HStack(alignment: .center, spacing: 4) {
                Text(verbatim: spec.address).skyText(.bodySmall, mono: true).foregroundStyle(Color.skyOnSurface).textSelection(.enabled)
                Spacer(minLength: 0)
                Button {
                    UIPasteboard.general.string = spec.address
                    repo.note(L10n.text("chat_address_copied"))
                } label: {
                    MaterialIcon(MI.roundedContentCopy).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 44, height: 44)
                }
                .accessibilityLabel(Text("chat_copy_address"))
            }
            Text(verbatim: spec.help).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
            HStack {
                Spacer()
                if let gid = spec.inviteGid {
                    Button {
                        Task {
                            if let link = await repo.inviteLink(gid) {
                                UIPasteboard.general.string = link
                                repo.note(L10n.text("chat_invite_copied"))
                            }
                        }
                    } label: { Text("chat_copy_invite_instead") }
                    .buttonStyle(.skyText)
                }
                Button { dialogs.dismiss() } label: { Text("chat_close") }
                    .buttonStyle(.skyText)
            }
        }
    }
}

extension SkyDialogs {
    func showQr(_ spec: ChatQrSpec, repo: ChatRepository) {
        showCustom { ChatQrDialog(spec: spec, repo: repo) }
    }
}

// MARK: Avatar picker

/// Which picture an avatar pick is for: the published one, a contact's local one, or a group's.
enum AvatarTarget {
    case me, contact, group

    var maxBytes: Int {
        switch self {
        case .me, .group: 32 * 1024
        case .contact: 64 * 1024
        }
    }

    var caption: String {
        switch self {
        case .me: L10n.format("chat_avatar_caption_me", "256×256")
        case .contact: L10n.format("chat_avatar_caption_contact", "256×256")
        case .group: L10n.format("chat_avatar_caption_group", "256×256")
        }
    }
}

/// Pick, centre-crop to 256×256, encode under the target's cap, then "Use this picture?"
/// (index.html _pickAvatar / _cropToSquare / confirmAvatar).
struct AvatarPickerButton<Label: View>: View {
    let target: AvatarTarget
    let onPicked: (Data) -> Void
    @ViewBuilder let label: Label
    @EnvironmentObject private var dialogs: SkyDialogs
    @State private var item: PhotosPickerItem?
    @State private var picking = false

    var body: some View {
        Button { picking = true } label: { label }
            .buttonStyle(.plain)
            .photosPicker(isPresented: $picking, selection: $item, matching: .images)
            .onChange(of: item) { picked in
                guard let picked else { return }
                item = nil
                Task { await stage(picked) }
            }
    }

    private func stage(_ picked: PhotosPickerItem) async {
        let repo = ChatRepository.shared
        guard let data = try? await picked.loadTransferable(type: Data.self) else {
            repo.note(L10n.text("chat_avatar_unreadable"), error: true)
            return
        }
        if data.count > 12 * 1024 * 1024 {
            repo.note(L10n.text("chat_avatar_too_large"), error: true)
            return
        }
        let target = target
        let encoded = await Task.detached { () -> Data? in
            guard let image = ChatImages.decode(data, maxEdge: 1024) else { return nil }
            return ChatImages.encodeAvatar(image, maxEdge: 256, maxBytes: target.maxBytes)
        }.value
        guard let encoded, let preview = UIImage(data: encoded) else {
            repo.note(L10n.text("chat_avatar_too_complex"), error: true)
            return
        }
        dialogs.showCustom {
            AvatarConfirm(preview: preview, caption: target.caption) {
                dialogs.dismiss()
                onPicked(encoded)
            }
        }
    }
}

private struct AvatarConfirm: View {
    let preview: UIImage
    let caption: String
    let use: () -> Void
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("chat_avatar_confirm").skyText(.headlineSmall).foregroundStyle(Color.skyOnSurface)
            HStack(spacing: 16) {
                Image(uiImage: preview).resizable().frame(width: 120, height: 120).clipShape(Circle())
                Image(uiImage: preview).resizable().frame(width: 44, height: 44).clipShape(Circle())
            }
            .frame(maxWidth: .infinity)
            Text(verbatim: caption).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
            HStack {
                Spacer()
                Button { dialogs.dismiss() } label: { Text("cancel") }.buttonStyle(.skyText)
                Button { use() } label: { Text("chat_use_it") }.buttonStyle(.skyText)
            }
        }
    }
}
