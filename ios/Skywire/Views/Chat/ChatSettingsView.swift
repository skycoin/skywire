import AVFoundation
import CoreClient
import SwiftUI
import UniformTypeIdentifiers
import UserNotifications

/// Notifications, the ringback tone, privacy and the archive (Android: ui/chat/ChatSettingsScreen.kt).
struct ChatSettingsView: View {
    let onBack: () -> Void
    @EnvironmentObject private var repo: ChatRepository
    @ObservedObject private var store = ChatRepository.shared.store
    @Environment(\.scenePhase) private var scenePhase
    @State private var allowed: Bool?
    @State private var ring: RingbackInfo?
    @State private var ringError = false
    @State private var ringStatus: String?
    @State private var ringPlayer: AVAudioPlayer?
    @State private var pickingRing = false
    @State private var pickingArchive = false
    @State private var transfer: String?

    var body: some View {
        VStack(spacing: 0) {
            SkyTopBar(title: Text("chat_settings"), onBack: onBack)
            ScrollView {
                VStack(alignment: .leading, spacing: 4) {
                    notifications
                    if repo.voiceAvailable { ringback }
                    if let privacy = repo.privacy { privacySection(privacy) }
                    archive
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 8)
            }
        }
        .background(Color.skyBackground)
        .task(id: scenePhase) {
            let status = await UNUserNotificationCenter.current().notificationSettings().authorizationStatus
            allowed = status == .authorized || status == .provisional || status == .ephemeral
        }
        .task(id: repo.voiceAvailable) { if repo.voiceAvailable { await reloadRing() } }
        .onDisappear { ringPlayer?.stop() }
        .fileImporter(isPresented: $pickingRing, allowedContentTypes: [.audio]) { result in
            guard case let .success(url) = result else { return }
            ringStatus = L10n.text("chat_ring_saving")
            Task {
                let error = await repo.setRingback(url)
                ringStatus = error.map { L10n.format("chat_ring_save_failed", $0) } ?? L10n.text("chat_ring_saved")
                await reloadRing()
            }
        }
        .fileImporter(isPresented: $pickingArchive, allowedContentTypes: [.json]) { result in
            guard case let .success(url) = result else { return }
            transfer = L10n.text("chat_import_reading")
            Task { transfer = await repo.importArchive(url) }
        }
    }

    private func section(_ text: String) -> some View {
        Text(verbatim: text).skyText(.titleMedium).foregroundStyle(Color.skyPrimary).padding(.top, 12).padding(.bottom, 6)
    }

    @ViewBuilder private var notifications: some View {
        section(L10n.text("chat_notifications"))
        if let allowed {
            Text(allowed ? L10n.key("chat_notif_allowed") : L10n.key("chat_notif_not_allowed")).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
            if !allowed {
                Button {
                    if let url = URL(string: UIApplication.openNotificationSettingsURLString) { UIApplication.shared.open(url) }
                } label: { Text("chat_allow_notifications") }
                .buttonStyle(.skyText)
            }
        }
        ChatSwitchRow(title: L10n.text("chat_notif_enable"), sub: L10n.text("chat_notif_enable_sub"), checked: store.notif.enabled) { on in
            store.updateNotif { $0.enabled = on }
        }
        ChatSwitchRow(title: L10n.text("chat_notif_preview"), sub: L10n.text("chat_notif_preview_sub"), checked: store.notif.preview, enabled: store.notif.enabled) { on in
            store.updateNotif { $0.preview = on }
        }
        Text("chat_muted_list").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface).padding(.top, 12)
        if store.notif.muted.isEmpty {
            Text("chat_no_muted").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
        }
        ForEach(store.notif.muted.sorted(), id: \.self) { key in
            HStack {
                let label = repo.groups.first { $0.id == key }.map { $0.name.isEmpty ? key : $0.name } ?? repo.displayName(key)
                Text(verbatim: label).skyText(.bodyLarge).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                Spacer(minLength: 0)
                Button { store.updateNotif { $0.muted.remove(key) } } label: { Text("chat_unmute") }.buttonStyle(.skyText)
            }
        }
    }

    @ViewBuilder private var ringback: some View {
        ChatDivider()
        section(L10n.text("chat_ringback"))
        Text("chat_ringback_sub").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
        Group {
            if ringError {
                Text("chat_ring_unreadable")
            } else if let ring, ring.set {
                Text(verbatim: L10n.format("chat_ring_yours", Skychat.humanSize(ring.size), ring.type))
            } else {
                Text("chat_ring_plain")
            }
        }
        .skyText(.bodyMedium).foregroundStyle(Color.skyOnSurface).padding(.vertical, 6)
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                Button { pickingRing = true } label: { Text("chat_ring_choose") }.buttonStyle(.outlined)
                Button { Task { await toggleRing() } } label: { Text(ringPlayer != nil ? L10n.key("chat_stop") : L10n.key("chat_play")) }
                    .buttonStyle(.outlined)
                    .disabled(ring?.set != true)
                Button {
                    Task {
                        let error = await repo.clearRingback()
                        ringStatus = error.map { L10n.format("chat_ring_clear_failed", $0) } ?? L10n.text("chat_ring_cleared")
                        await reloadRing()
                    }
                } label: { Text("chat_ring_plain_button") }
                .buttonStyle(.outlined)
                .disabled(ring?.set != true)
            }
        }
        if let ringStatus {
            Text(verbatim: ringStatus).skyText(.bodySmall).foregroundStyle(Color.skyOnSurface).padding(.top, 4)
        }
    }

    @ViewBuilder private func privacySection(_ p: PrivacyState) -> some View {
        ChatDivider()
        section(L10n.text("chat_privacy"))
        ChatSwitchRow(title: L10n.text("chat_approval"), sub: L10n.text("chat_approval_sub"), checked: p.approval) { repo.setApproval($0) }
        Text("chat_blocked").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface).padding(.top, 12)
        if p.blocked.isEmpty {
            Text("chat_nobody_blocked").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
        }
        ForEach(p.blocked, id: \.self) { pk in
            HStack {
                Text(verbatim: repo.displayName(pk)).skyText(.bodyLarge).foregroundStyle(Color.skyOnSurface).lineLimit(1)
                Spacer(minLength: 0)
                Button { repo.setBlocked(pk, false) } label: { Text("chat_unblock") }.buttonStyle(.skyText)
            }
        }
    }

    @ViewBuilder private var archive: some View {
        ChatDivider()
        section(L10n.text("chat_transfer"))
        Text("chat_transfer_help").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
        HStack(spacing: 8) {
            Button {
                transfer = L10n.text("chat_export_preparing")
                Task { if await repo.exportArchive() { transfer = nil } }
            } label: { Text("chat_export") }
            .buttonStyle(.outlined)
            .accessibilityIdentifier("chat-export")
            Button { pickingArchive = true } label: { Text("chat_import") }
                .buttonStyle(.outlined)
                .accessibilityIdentifier("chat-import")
        }
        .padding(.top, 8)
        if let transfer {
            Text(verbatim: transfer).skyText(.bodySmall).foregroundStyle(Color.skyOnSurface).padding(.top, 6)
        }
        Text("chat_transfer_keys").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8).padding(.bottom, 24)
    }

    private func reloadRing() async {
        let info = await repo.ringbackInfo()
        ring = info
        ringError = info == nil
    }

    private func toggleRing() async {
        if let player = ringPlayer {
            player.stop()
            ringPlayer = nil
            return
        }
        guard let bytes = try? await repo.api.bytes("/voice/my-ringback?t=\(Int(Date().timeIntervalSince1970))"),
              let player = try? AVAudioPlayer(data: bytes)
        else {
            ringStatus = L10n.text("chat_ring_cannot_play")
            return
        }
        if !VoiceCalls.shared.state.busy {
            try? AVAudioSession.sharedInstance().setCategory(.playback)
            try? AVAudioSession.sharedInstance().setActive(true)
        }
        player.play()
        ringPlayer = player
        Task {
            try? await Task.sleep(for: .seconds(player.duration + 0.2))
            if ringPlayer === player { ringPlayer = nil }
        }
    }
}
