import AVFoundation
import CoreClient
import PhotosUI
import SwiftUI

/// One morphing button: send with text, mic or camera without (Android:
/// ui/chat/Composer.kt, index.html composer). A tap on the mic or camera
/// switches what it records; holding it records, sliding left cancels and
/// sliding up locks the take so the finger can lift.
struct Composer: View {
    let key: String
    @EnvironmentObject private var repo: ChatRepository
    @EnvironmentObject private var nav: ChatNav
    @ObservedObject private var store = ChatRepository.shared.store
    @Environment(\.scenePhase) private var scenePhase
    @State private var text = ""
    @State private var phase = Phase.idle
    @State private var recVideo = false
    @State private var locked = false
    @State private var slide: CGSize = .zero
    @State private var elapsed: Int64 = 0
    @State private var levels: [Float] = []
    @State private var holdTask: Task<Void, Never>?
    @State private var fired = false
    @State private var released = false
    @State private var photos: [PhotosPickerItem] = []
    @State private var pickingPhotos = false
    @State private var pickingFiles = false
    @State private var voice = VoiceRecorder()
    @StateObject private var camera = VideoNoteRecorder()

    private enum Phase {
        case idle, opening, recording
    }

    private static let cancelDx: CGFloat = 90
    private static let lockDy: CGFloat = 56

    var body: some View {
        VStack(spacing: 0) {
            if phase != .idle && recVideo { cameraPreview }
            HStack(alignment: .bottom, spacing: 6) {
                if phase == .idle {
                    Menu {
                        Button { pickingPhotos = true } label: { Label(L10n.text("chat_attach_photo"), image: MI.roundedImage) }
                        Button { pickingFiles = true } label: { Label(L10n.text("chat_attach_file"), image: MI.roundedInsertDriveFile) }
                    } label: {
                        MaterialIcon(MI.roundedAttachFile).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 44, height: 48)
                    }
                    .accessibilityLabel(Text("chat_attach"))
                    .accessibilityIdentifier("composer-attach")
                    TextField(text: $text, axis: .vertical) { Text("chat_type_message") }
                        .skyText(.bodyLarge)
                        .lineLimit(1...5)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 12)
                        .frame(minHeight: 48)
                        .background(Color.skyContainerHighest, in: .sky(24))
                        .accessibilityIdentifier("composer-field")
                        .onChange(of: text) { nav.drafts[key] = $0 }
                } else {
                    recordingBar
                }
                actionButton
            }
            .padding(6)
        }
        .background(Color.skyBackground)
        .onAppear { text = nav.drafts[key] ?? "" }
        .photosPicker(isPresented: $pickingPhotos, selection: $photos, matching: .any(of: [.images, .videos]))
        .onChange(of: photos) { items in
            guard !items.isEmpty else { return }
            photos = []
            Task { await attach(items) }
        }
        .fileImporter(isPresented: $pickingFiles, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
            guard case let .success(urls) = result else { return }
            for url in urls {
                do {
                    repo.sendFile(key, staged: try ChatRepository.stage(url))
                } catch {
                    repo.note(L10n.text("chat_import_unreadable"), error: true)
                }
            }
            nav.setReply(key, nil)
        }
        .task(id: phase == .recording) {
            while phase == .recording && !Task.isCancelled {
                elapsed = recVideo ? camera.elapsedMs : voice.elapsedMs
                if !recVideo {
                    levels.append(voice.sample())
                    if levels.count > 120 { levels.removeFirst() }
                }
                try? await Task.sleep(for: .milliseconds(55))
            }
        }
        // Leaving the app ends an unlocked take and sends what was recorded.
        .onChange(of: scenePhase) { now in
            if now != .active && phase != .idle && !locked { finish(cancel: false) }
        }
        .onDisappear {
            if phase != .idle {
                _ = voice.stop(discard: true)
                camera.release()
            }
        }
    }

    private var hasText: Bool { !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    private var video: Bool { store.state.mediaMode == "video" }

    // MARK: The button

    private var actionButton: some View {
        ZStack {
            if phase != .idle && !locked {
                MaterialIcon(MI.roundedLock, size: 20)
                    .foregroundStyle(-slide.height > Self.lockDy / 2 ? Color.skyPrimary : Color.skyOnSurfaceVariant)
                    .frame(width: 32, height: 32)
                    .background(Color.skyContainerHigh, in: Circle())
                    .offset(y: -64)
            }
            Circle()
                .fill(phase != .idle ? Color.skyError : Color.skyPrimary)
                .frame(width: 48, height: 48)
                .overlay {
                    MaterialIcon(hasText || locked ? MI.roundedSend : (video ? MI.roundedCameraAlt : MI.roundedMic)).foregroundStyle(.white)
                }
                .offset(x: locked ? 0 : min(0, slide.width), y: locked ? 0 : min(0, slide.height))
                .gesture(DragGesture(minimumDistance: 0)
                    .onChanged { value in pressChanged(value) }
                    .onEnded { _ in pressEnded() })
                .accessibilityLabel(Text(buttonLabel))
                .accessibilityAddTraits(.isButton)
                .accessibilityAction { if hasText { send() } }
                .accessibilityIdentifier("composer-send")
        }
        .frame(width: 48, height: 48)
    }

    private var buttonLabel: String {
        if hasText { return L10n.text("chat_send") }
        if locked { return recVideo ? L10n.text("chat_rec_send_video") : L10n.text("chat_rec_send_voice") }
        return video ? L10n.text("chat_rec_mode_video_hint") : L10n.text("chat_rec_mode_voice_hint")
    }

    @State private var pressing = false

    private func pressChanged(_ value: DragGesture.Value) {
        if !pressing {
            pressing = true
            if hasText || (locked && phase == .recording) { return }
            released = false
            fired = false
            holdTask = Task {
                try? await Task.sleep(for: .milliseconds(220))
                guard !Task.isCancelled, pressing else { return }
                fired = true
                start(asVideo: store.state.mediaMode == "video")
            }
            return
        }
        guard phase != .idle, !locked else { return }
        slide = CGSize(width: min(0, value.translation.width), height: min(0, value.translation.height))
        if slide.width <= -Self.cancelDx {
            finish(cancel: true)
            pressing = false
            fired = false
        } else if slide.height <= -Self.lockDy {
            locked = true
            slide = .zero
        }
    }

    private func pressEnded() {
        let was = pressing
        pressing = false
        guard was else { return }
        if hasText {
            send()
            return
        }
        if locked && phase == .recording && !fired {
            finish(cancel: false)
            return
        }
        released = true
        if !fired {
            holdTask?.cancel()
            // A tap switches what the button records.
            let toVideo = store.state.mediaMode != "video"
            store.update { $0.mediaMode = toVideo ? "video" : "mic" }
            repo.note(toVideo ? L10n.text("chat_rec_mode_video") : L10n.text("chat_rec_mode_voice"))
        } else if !locked && phase != .idle {
            finish(cancel: false)
        }
        fired = false
    }

    private func send() {
        repo.sendText(key, text, reply: nav.replies[key])
        text = ""
        nav.drafts[key] = nil
        nav.setReply(key, nil)
    }

    // MARK: Recording

    private func start(asVideo: Bool) {
        Task {
            guard await Self.permit(.audio) else {
                repo.note(L10n.text("chat_rec_mic_denied"), error: true)
                return
            }
            if asVideo {
                guard await Self.permit(.video) else {
                    repo.note(L10n.text("chat_rec_camera_denied"), error: true)
                    return
                }
            }
            ChatAudioPlayer.shared.stop()
            recVideo = asVideo
            if asVideo {
                phase = .opening
                do {
                    try camera.bind(front: store.state.videoFacingFront)
                } catch {
                    repo.note(L10n.format("chat_rec_camera_error", error.localizedDescription), error: true)
                    camera.release()
                    reset()
                    return
                }
                if released && !locked {
                    // The finger lifted while the camera opened: nothing to send.
                    camera.release()
                    reset()
                    repo.note(L10n.text("chat_rec_camera_ready"))
                    return
                }
                do {
                    try camera.start(square: store.state.videoSquare)
                    phase = .recording
                } catch {
                    repo.note(L10n.format("chat_rec_camera_error", error.localizedDescription), error: true)
                    camera.release()
                    reset()
                }
            } else {
                do {
                    try voice.start()
                    phase = .recording
                    if released && !locked { finish(cancel: false) }
                } catch {
                    repo.note(L10n.format("chat_rec_mic_error", error.localizedDescription), error: true)
                }
            }
        }
    }

    /// Asks for the microphone or camera the first time; true when allowed.
    private static func permit(_ media: AVMediaType) async -> Bool {
        switch AVCaptureDevice.authorizationStatus(for: media) {
        case .authorized: return true
        case .notDetermined: return await AVCaptureDevice.requestAccess(for: media)
        default: return false
        }
    }

    private func reset() {
        phase = .idle
        locked = false
        slide = .zero
        elapsed = 0
        levels = []
    }

    private func finish(cancel: Bool) {
        let wasVideo = recVideo
        let wasLocked = locked
        Task {
            let staged: String?
            if wasVideo {
                staged = await camera.stop(discard: cancel)
                camera.release()
            } else {
                staged = voice.stop(discard: cancel)
            }
            reset()
            if cancel {
                switch (wasLocked, wasVideo) {
                case (true, true): repo.note(L10n.text("chat_rec_video_discarded"))
                case (true, false): repo.note(L10n.text("chat_rec_voice_discarded"))
                case (false, true): repo.note(L10n.text("chat_rec_video_cancelled"))
                case (false, false): repo.note(L10n.text("chat_rec_voice_cancelled"))
                }
            } else if let staged {
                repo.sendFile(key, staged: staged, caption: wasVideo ? "📹 Video message" : "🎤 Voice message", kind: wasVideo ? .video : .voice)
            } else {
                repo.note(wasVideo ? L10n.text("chat_rec_hold_video") : L10n.text("chat_rec_hold_voice"))
            }
        }
    }

    private var cameraPreview: some View {
        HStack(spacing: 8) {
            Button {
                let front = !camera.front
                store.update { $0.videoFacingFront = front }
                try? camera.bind(front: front)
            } label: {
                MaterialIcon(MI.roundedFlipCameraAndroid).foregroundStyle(Color.skyOnSurface).frame(width: 44, height: 44)
            }
            .accessibilityLabel(Text("chat_switch_camera"))
            CameraPreview(session: camera.session)
                .frame(width: 220, height: 220)
                .clipShape(store.state.videoSquare ? AnyShape(RoundedRectangle(cornerRadius: 24)) : AnyShape(Circle()))
            Button {
                store.update { $0.videoSquare.toggle() }
            } label: {
                MaterialIcon(store.state.videoSquare ? MI.roundedRadioButtonUnchecked : MI.roundedCropSquare)
                    .foregroundStyle(Color.skyOnSurface).frame(width: 44, height: 44)
            }
            .accessibilityLabel(Text(store.state.videoSquare ? L10n.key("chat_rec_round") : L10n.key("chat_rec_square")))
        }
        .padding(.top, 8)
    }

    private var recordingBar: some View {
        let nearCancel = slide.width < -Self.cancelDx / 2
        return HStack(spacing: 8) {
            if locked {
                Button { finish(cancel: true) } label: {
                    MaterialIcon(MI.roundedDelete).foregroundStyle(Color.skyError).frame(width: 44, height: 44)
                }
                .accessibilityLabel(Text("chat_rec_delete"))
            } else {
                Circle().fill(Color.skyError).frame(width: 10, height: 10).padding(.leading, 12)
            }
            Text(verbatim: ChatText.clock(elapsed)).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurface)
            GeometryReader { geo in
                let bar: CGFloat = 3
                let gap: CGFloat = 2
                let shown = levels.suffix(Int(geo.size.width / (bar + gap)))
                HStack(alignment: .center, spacing: gap) {
                    Spacer(minLength: 0)
                    ForEach(Array(shown.enumerated()), id: \.offset) { _, v in
                        Capsule().fill(nearCancel ? Color.skyError : Color.skyPrimary)
                            .frame(width: bar, height: max(2, CGFloat(sqrt(v)) * geo.size.height))
                    }
                }
                .frame(maxHeight: .infinity)
            }
            .frame(height: 28)
            Text(verbatim: locked ? L10n.text("chat_rec_tap_send") : "‹ " + L10n.text("chat_rec_slide_cancel"))
                .skyText(.labelMedium)
                .foregroundStyle(nearCancel ? Color.skyError : Color.skyOnSurfaceVariant)
                .offset(x: slide.width / 2)
                .padding(.trailing, 8)
        }
        .frame(minHeight: 48)
        .frame(maxWidth: .infinity)
    }

    private func attach(_ items: [PhotosPickerItem]) async {
        for item in items {
            guard let movie = try? await item.loadTransferable(type: PickedMedia.self) else {
                repo.note(L10n.text("chat_import_unreadable"), error: true)
                continue
            }
            repo.sendFile(key, staged: movie.staged)
        }
        nav.setReply(key, nil)
    }
}

/// A picked photo or video, copied into the upload folder under its own name.
private struct PickedMedia: Transferable {
    let staged: String

    static var transferRepresentation: some TransferRepresentation {
        FileRepresentation(importedContentType: .item) { received in
            PickedMedia(staged: try ChatRepository.stage(received.file))
        }
    }
}

/// The camera's live picture for a video message.
private struct CameraPreview: UIViewRepresentable {
    let session: AVCaptureSession

    final class PreviewView: UIView {
        override static var layerClass: AnyClass { AVCaptureVideoPreviewLayer.self }
        var previewLayer: AVCaptureVideoPreviewLayer { layer as! AVCaptureVideoPreviewLayer }
    }

    func makeUIView(context: Context) -> PreviewView {
        let view = PreviewView()
        view.previewLayer.session = session
        view.previewLayer.videoGravity = .resizeAspectFill
        return view
    }

    func updateUIView(_ view: PreviewView, context: Context) {}
}
