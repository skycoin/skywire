import AVFoundation
import AVKit
import CoreClient
import SwiftUI

/// Bubble colours for one side.
struct BubbleColors {
    let bg: Color
    let fg: Color
    let meta: Color
    let accent: Color

    static func of(mine: Bool) -> BubbleColors {
        mine
            ? BubbleColors(bg: .skyPrimary, fg: .skyOnPrimary, meta: .skyOnPrimary.opacity(0.72), accent: .skyOnPrimary)
            : BubbleColors(bg: .skyContainerHighest, fg: .skyOnSurface, meta: .skyOnSurfaceVariant, accent: .skyPrimary)
    }
}

/// One message (Android: ui/chat/MessageViews.kt). A tap or a long press opens its sheet.
struct MessageRow: View {
    let key: String
    let m: ChatMessage
    let group: Bool
    let highlighted: Bool
    let onTap: () -> Void
    @EnvironmentObject private var repo: ChatRepository

    var body: some View {
        let mine = m.mine
        let colors = BubbleColors.of(mine: mine)
        let media = m.file.map { $0.isVideoMessage || ($0.isImage && (!$0.url.isEmpty || ChatStore.hasUpload($0.localPath))) } ?? false
        HStack {
            if mine { Spacer(minLength: 40) }
            VStack(alignment: mine ? .trailing : .leading, spacing: 2) {
                if group && !mine {
                    Text(verbatim: repo.displayName(m.from)).skyText(.labelMedium).foregroundStyle(chatKeyColor(m.from)).padding(.leading, 10)
                }
                VStack(alignment: .leading, spacing: 0) {
                    if let reply = m.reply {
                        QuoteView(reply: reply, colors: colors)
                    } else if m.forwarded {
                        Text(verbatim: "↪ " + (m.forwardFrom.isEmpty ? L10n.text("chat_forwarded") : L10n.format("chat_forwarded_from", m.forwardFrom)))
                            .skyText(.labelSmall).italic().foregroundStyle(colors.meta).padding(.bottom, 4)
                    }
                    if let f = m.file {
                        FileContent(key: key, m: m, f: f, colors: colors)
                        UploadProgress(msgKey: m.key, colors: colors)
                    } else {
                        Text(verbatim: ChatText.shown(m.text)).skyText(.bodyLarge).foregroundStyle(colors.fg).textSelection(.enabled)
                    }
                    if !media { MetaLine(key: key, m: m, group: group, colors: colors) }
                }
                .padding(media ? 0 : 10)
                .background(media ? Color.clear : colors.bg, in: BubbleShape(mine: mine))
                if media { MetaLine(key: key, m: m, group: group, colors: BubbleColors.of(mine: false)) }
            }
            .frame(maxWidth: 310, alignment: mine ? .trailing : .leading)
            if !mine { Spacer(minLength: 40) }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 2)
        .background(highlighted ? Color.skyPrimary.opacity(0.16) : .clear)
        .animation(.easeInOut(duration: 0.3), value: highlighted)
        .contentShape(Rectangle())
        .onTapGesture(perform: onTap)
        .onLongPressGesture(perform: onTap)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("msg-\(m.key)")
    }
}

private struct BubbleShape: Shape {
    let mine: Bool

    func path(in rect: CGRect) -> Path {
        let big: CGFloat = 18
        let small: CGFloat = 4
        var corners = UIRectCorner.allCorners
        corners.remove(mine ? .bottomRight : .bottomLeft)
        let path = UIBezierPath(roundedRect: rect, byRoundingCorners: corners, cornerRadii: CGSize(width: big, height: big))
        let tail = UIBezierPath(roundedRect: CGRect(x: mine ? rect.maxX - big : rect.minX, y: rect.maxY - big, width: big, height: big),
                                byRoundingCorners: mine ? .bottomRight : .bottomLeft, cornerRadii: CGSize(width: small, height: small))
        path.append(tail)
        return Path(path.cgPath)
    }
}

private struct QuoteView: View {
    let reply: ReplyRef
    let colors: BubbleColors
    @EnvironmentObject private var repo: ChatRepository
    @Environment(\.quoteJump) private var jump

    var body: some View {
        let who = reply.sender == Skychat.me || reply.sender == repo.myPk
            ? L10n.text("chat_you")
            : (reply.sender.isEmpty ? L10n.text("chat_message") : repo.displayName(reply.sender))
        Button { jump(reply) } label: {
            HStack(spacing: 8) {
                Rectangle().fill(colors.accent).frame(width: 3)
                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: who).skyText(.labelMedium).foregroundStyle(colors.accent).lineLimit(1)
                    Text(verbatim: ChatText.shown(reply.preview)).skyText(.bodySmall).foregroundStyle(colors.fg.opacity(0.85)).lineLimit(1)
                }
                .padding(.vertical, 3)
                Spacer(minLength: 0)
            }
            .frame(height: 40)
            .background(colors.fg.opacity(0.10), in: .sky(8))
        }
        .buttonStyle(.plain)
        .padding(.bottom, 6)
    }
}

private struct MetaLine: View {
    let key: String
    let m: ChatMessage
    let group: Bool
    let colors: BubbleColors
    @EnvironmentObject private var repo: ChatRepository

    var body: some View {
        HStack(spacing: 4) {
            Text(verbatim: Self.clock(m.ts)).skyText(.labelSmall).foregroundStyle(colors.meta)
            if m.mine && key != Skychat.savedKey { tick(Skychat.tickStatus(m, group: group)) }
        }
        .padding(.top, 2)
        .padding(.horizontal, 4)
        .frame(maxWidth: .infinity, alignment: .trailing)
    }

    static func clock(_ ms: Int64) -> String {
        let formatter = DateFormatter()
        formatter.dateFormat = "HH:mm"
        return formatter.string(from: Date(timeIntervalSince1970: Double(ms) / 1000))
    }

    @ViewBuilder private func tick(_ status: String) -> some View {
        switch status {
        case "queued":
            MaterialIcon(MI.roundedSchedule, size: 14).foregroundStyle(Color.skyWarning).accessibilityElement(children: .ignore).accessibilityLabel(Text("chat_tick_queued")).accessibilityAddTraits(.isImage)
        case "pending":
            MaterialIcon(MI.roundedRadioButtonUnchecked, size: 14).foregroundStyle(colors.meta).accessibilityElement(children: .ignore).accessibilityLabel(Text("chat_tick_pending")).accessibilityAddTraits(.isImage)
        case "sent":
            MaterialIcon(MI.roundedDone, size: 14).foregroundStyle(colors.meta).accessibilityElement(children: .ignore).accessibilityLabel(Text("chat_tick_sent")).accessibilityAddTraits(.isImage)
        case "received":
            MaterialIcon(MI.roundedDoneAll, size: 14).foregroundStyle(colors.meta).accessibilityElement(children: .ignore).accessibilityLabel(Text("chat_tick_received")).accessibilityAddTraits(.isImage)
        case "read":
            MaterialIcon(MI.roundedDoneAll, size: 14).foregroundStyle(Color(hex: 0x7DF2B9)).accessibilityElement(children: .ignore).accessibilityLabel(Text("chat_tick_read")).accessibilityAddTraits(.isImage)
        case "failed":
            let retryable = m.file.map { ChatStore.hasUpload($0.localPath) } ?? true
            if retryable {
                Button { repo.resend(key, m.key) } label: {
                    Text(verbatim: "⚠ " + (m.file == nil ? L10n.text("chat_resend") : L10n.text("chat_retry")))
                        .skyText(.labelSmall, bold: true).foregroundStyle(Color.skyError)
                        .padding(.horizontal, 6).background(Color.skyErrorContainer, in: .sky(6))
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("msg-resend")
            } else {
                MaterialIcon(MI.roundedWarning, size: 14).foregroundStyle(Color.skyError).accessibilityElement(children: .ignore).accessibilityLabel(Text("chat_transfer_failed")).accessibilityAddTraits(.isImage)
            }
        default:
            EmptyView()
        }
    }
}

private struct UploadProgress: View {
    let msgKey: String
    let colors: BubbleColors
    @EnvironmentObject private var repo: ChatRepository

    var body: some View {
        if let upload = repo.uploads[msgKey] {
            VStack(alignment: .leading, spacing: 2) {
                ProgressView(value: upload.total > 0 ? Double(upload.sent) / Double(upload.total) : 0).tint(colors.accent)
                Text(verbatim: "\(Skychat.humanSize(upload.sent)) / \(Skychat.humanSize(upload.total))").skyText(.labelSmall).foregroundStyle(colors.meta)
            }
            .frame(minWidth: 160)
            .padding(.top, 4)
        }
    }
}

/// Chooses the renderer by name, as every skychat UI does (index.html renderFile).
private struct FileContent: View {
    let key: String
    let m: ChatMessage
    let f: ChatFile
    let colors: BubbleColors

    var body: some View {
        let hasBytes = !f.url.isEmpty || ChatStore.hasUpload(f.localPath)
        if hasBytes && f.isImage {
            ImageThumb(f: f)
        } else if hasBytes && f.isVoiceMessage {
            VoicePlayer(m: m, f: f, colors: colors, voice: true)
        } else if hasBytes && f.isVideoMessage {
            RoundVideo(m: m, f: f, square: f.isSquareVideoMessage)
        } else if hasBytes && f.isAudio {
            VoicePlayer(m: m, f: f, colors: colors, voice: false)
        } else if hasBytes && f.isVideo {
            VideoCard(f: f, colors: colors)
        } else {
            FileCard(m: m, f: f, colors: colors)
        }
    }
}

private struct FileCard: View {
    let m: ChatMessage
    let f: ChatFile
    let colors: BubbleColors
    @EnvironmentObject private var repo: ChatRepository

    var body: some View {
        HStack(spacing: 10) {
            MaterialIcon(MI.roundedInsertDriveFile).foregroundStyle(colors.fg)
                .frame(width: 40, height: 40).background(colors.fg.opacity(0.12), in: .sky(10))
            VStack(alignment: .leading, spacing: 0) {
                Text(verbatim: f.isVoiceMessage ? L10n.text("chat_voice_message") : (f.isVideoMessage ? L10n.text("chat_video_message") : f.name))
                    .skyText(.bodyMedium).foregroundStyle(colors.fg).lineLimit(2)
                let failed = f.status == "failed"
                Text(verbatim: failed ? L10n.text("chat_transfer_failed_lower") : Skychat.humanSize(f.size))
                    .skyText(.labelSmall).foregroundStyle(colors.meta)
                if f.url.isEmpty && !f.from.isEmpty && !f.id.isEmpty {
                    Button { repo.requestFile(m) } label: { Text("chat_rerequest").skyText(.labelMedium).foregroundStyle(colors.accent) }
                        .buttonStyle(.plain)
                }
            }
            if f.url.hasPrefix("/files/") {
                Button { repo.download(m) } label: { MaterialIcon(MI.roundedDownload).foregroundStyle(colors.fg).frame(width: 40, height: 40) }
                    .accessibilityLabel(Text("chat_download"))
            }
        }
        .frame(minWidth: 180, alignment: .leading)
    }
}

// MARK: Media

/// Our own copy when we still hold it, else the served `/files/…` path.
func chatMediaSource(_ f: ChatFile) -> String {
    ChatStore.hasUpload(f.localPath) ? "up:" + f.localPath : f.url
}

/// The bytes of a media source on disk: our own copy, or a cached download.
@MainActor
func chatMediaFile(_ source: String) async -> URL? {
    if source.hasPrefix("up:") { return ChatStore.uploadURL(String(source.dropFirst(3))) }
    return await ChatRepository.shared.media.get(source)
}

private struct ImageThumb: View {
    let f: ChatFile
    @EnvironmentObject private var nav: ChatNav
    @State private var image: UIImage?

    var body: some View {
        let source = chatMediaSource(f)
        let shown = image ?? ChatImages.cached("thumb:" + source)
        let ratio = shown.map { min(2, max(0.5, $0.size.width / max(1, $0.size.height))) } ?? 1.3
        Button { nav.push(.image(source: source, name: f.name)) } label: {
            ZStack {
                Color.skyContainerHighest
                if let shown {
                    Image(uiImage: shown).resizable().scaledToFill()
                } else {
                    MaterialSpinner(size: 24, stroke: 2)
                }
            }
            .frame(width: 240, height: 240 / ratio)
            .clipShape(RoundedRectangle(cornerRadius: SkyRadius.medium))
        }
        .buttonStyle(.plain)
        .accessibilityLabel(Text(verbatim: f.name))
        .task(id: source) {
            let cacheKey = "thumb:" + source
            if ChatImages.cached(cacheKey) != nil { return }
            var file: URL?
            if let thumb = f.thumbPath, !source.hasPrefix("up:") { file = await ChatRepository.shared.media.get(thumb) }
            if file == nil { file = await chatMediaFile(source) }
            guard let file else { return }
            let decoded = await Task.detached { ChatImages.decode(file: file, maxEdge: 640) }.value
            guard let decoded else { return }
            ChatImages.remember(cacheKey, decoded)
            image = decoded
        }
    }
}

@MainActor
private var peakCache: [String: (peaks: [Float], durationMs: Int64)] = [:]

/// Voice messages get bars; attached audio gets its name over the bar.
private struct VoicePlayer: View {
    let m: ChatMessage
    let f: ChatFile
    let colors: BubbleColors
    let voice: Bool
    @ObservedObject private var player = ChatAudioPlayer.shared
    @ObservedObject private var store = ChatRepository.shared.store
    @State private var file: URL?
    @State private var peaks: (peaks: [Float], durationMs: Int64)?
    @State private var loading = false

    var body: some View {
        let source = chatMediaSource(f)
        let mine = player.key == m.key
        let duration = mine && player.durationMs > 0 ? player.durationMs : (peaks?.durationMs ?? 0)
        let progress = mine && duration > 0 ? Double(player.positionMs) / Double(duration) : 0
        HStack(spacing: 10) {
            Button { Task { await play(source) } } label: {
                ZStack {
                    Circle().fill(colors.accent)
                    if loading {
                        MaterialSpinner(size: 20, stroke: 2, color: colors.bg)
                    } else {
                        MaterialIcon(mine && player.playing ? MI.roundedPause : MI.roundedPlayArrow)
                            .foregroundStyle(colors.accent == colors.fg ? colors.bg : .white)
                    }
                }
                .frame(width: 42, height: 42)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(Text(mine && player.playing ? L10n.key("chat_pause") : L10n.key("chat_play")))
            VStack(alignment: .leading, spacing: 2) {
                if !voice {
                    Text(verbatim: f.name).skyText(.bodySmall).foregroundStyle(colors.fg).lineLimit(1)
                }
                Waveform(
                    peaks: voice ? peaks?.peaks : [Float](repeating: 0.35, count: 48),
                    progress: progress, played: colors.fg, rest: colors.fg.opacity(0.35)
                ) { fraction in
                    if mine {
                        player.seek(m.key, Float(fraction))
                    } else if let file {
                        player.setSpeed(store.state.speed)
                        player.play(m.key, file, at: Float(fraction))
                    }
                }
                .frame(height: voice ? 30 : 10)
                HStack {
                    let shown = mine && (player.playing || player.positionMs > 0) ? player.positionMs : duration
                    Text(verbatim: ChatText.clock(shown) + " · " + Skychat.humanSize(f.size)).skyText(.labelSmall).foregroundStyle(colors.meta)
                    Spacer(minLength: 0)
                    SpeedPill(colors: colors)
                }
            }
            .frame(width: 170)
        }
        .task(id: source) {
            if let cached = peakCache[source] { peaks = cached }
            guard voice else { return }
            file = await chatMediaFile(source)
            guard peaks == nil, let file else { return }
            if let computed = await Task.detached(operation: { AudioPeaks.compute(file, bars: 48) }).value {
                peakCache[source] = computed
                peaks = computed
            }
        }
    }

    private func play(_ source: String) async {
        if file == nil {
            loading = true
            file = await chatMediaFile(source)
            loading = false
        }
        guard let file else {
            ChatRepository.shared.note(L10n.text("chat_cannot_play"), error: true)
            return
        }
        player.setSpeed(store.state.speed)
        player.toggle(m.key, file)
    }
}

private struct SpeedPill: View {
    let colors: BubbleColors
    @ObservedObject private var store = ChatRepository.shared.store

    var body: some View {
        let speed = store.state.speed
        let label = (speed.truncatingRemainder(dividingBy: 1) == 0 ? String(Int(speed)) : String(speed)) + "×"
        Button {
            let speeds = ChatAudioPlayer.speeds
            let next = speeds[((speeds.firstIndex(of: speed) ?? 0) + 1) % speeds.count]
            store.update { $0.speed = next }
            ChatAudioPlayer.shared.setSpeed(next)
        } label: {
            Text(verbatim: label).skyText(.labelSmall).foregroundStyle(speed != 1 ? colors.bg : colors.fg)
                .padding(.horizontal, 6)
                .background(speed != 1 ? colors.fg : colors.fg.opacity(0.15), in: .sky(8))
        }
        .buttonStyle(.plain)
    }
}

/// Only one recorded video plays at a time.
@MainActor
private final class RoundVideoNow: ObservableObject {
    static let shared = RoundVideoNow()
    @Published var key: String?
}

/// A round (or square) video message: plays in place on tap, the ring is its progress.
private struct RoundVideo: View {
    let m: ChatMessage
    let f: ChatFile
    let square: Bool
    @EnvironmentObject private var nav: ChatNav
    @ObservedObject private var now = RoundVideoNow.shared
    @State private var player: AVPlayer?
    @State private var web: WebMediaElement?
    @State private var file: URL?
    @State private var playing = false
    @State private var position: Double = 0
    @State private var duration: Double = 0

    private static let side: CGFloat = 200

    private var loaded: Bool { player != nil || web != nil }

    var body: some View {
        let source = chatMediaSource(f)
        ZStack {
            if loaded {
                Group {
                    if let player {
                        PlayerLayerView(player: player)
                    } else if let web {
                        WebVideoSurface(element: web).allowsHitTesting(false)
                    }
                }
                .frame(width: Self.side - 8, height: Self.side - 8)
                .clipShape(square ? AnyShape(RoundedRectangle(cornerRadius: 18)) : AnyShape(Circle()))
                .contentShape(Circle())
                .onTapGesture { toggle() }
                if !square {
                    Circle()
                        .trim(from: 0, to: duration > 0 ? position / duration : 0)
                        .stroke(Color.skyPrimary, style: StrokeStyle(lineWidth: 3, lineCap: .round))
                        .rotationEffect(.degrees(-90))
                        .frame(width: Self.side - 3, height: Self.side - 3)
                        .allowsHitTesting(false)
                }
                if !playing {
                    MaterialIcon(MI.roundedPlayArrow).foregroundStyle(.white).padding(6)
                        .background(Color.black.opacity(0.45), in: Circle()).allowsHitTesting(false)
                }
                Text(verbatim: ChatText.clock(Int64((playing || position > 0 ? position : duration) * 1000)))
                    .skyText(.labelSmall).foregroundStyle(.white).padding(.horizontal, 5)
                    .background(Color.black.opacity(0.45), in: .sky(8))
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomLeading)
                    .padding(.leading, 14).padding(.bottom, 10)
                    .allowsHitTesting(false)
                Button {
                    pause()
                    if let file { nav.push(.video(source: "file:" + file.path, name: f.name, start: position)) }
                } label: {
                    MaterialIcon(MI.roundedFullscreen, size: 22).foregroundStyle(.white).frame(width: 30, height: 30)
                        .background(Color.black.opacity(0.45), in: Circle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel(Text("chat_full_screen"))
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomTrailing)
                .padding(.trailing, 10).padding(.bottom, 6)
            } else {
                (square ? AnyShape(RoundedRectangle(cornerRadius: 18)) : AnyShape(Circle())).fill(Color.black)
                    .frame(width: Self.side, height: Self.side)
                MaterialSpinner(size: 28, stroke: 2, color: .white)
            }
        }
        .frame(width: Self.side, height: Self.side)
        .task(id: source) {
            guard let file = await chatMediaFile(source) else { return }
            self.file = file
            if WebMedia.needs(f.name) {
                let element = WebMediaElement(video: true)
                duration = await element.load(file) ?? 0
                web = element
                return
            }
            let item = AVPlayerItem(url: file)
            let player = AVPlayer(playerItem: item)
            player.actionAtItemEnd = .pause
            self.player = player
            duration = (try? await item.asset.load(.duration).seconds) ?? 0
        }
        .task(id: playing) {
            while playing {
                if let player {
                    position = player.currentTime().seconds
                } else if let web, let state = await web.state() {
                    position = state.position
                    if state.duration > 0 { duration = state.duration }
                }
                if duration > 0 && position >= duration - 0.05 {
                    playing = false
                    position = 0
                    await player?.seek(to: .zero)
                    await web?.seek(0)
                }
                try? await Task.sleep(for: .milliseconds(50))
            }
        }
        .onChange(of: now.key) { key in
            if key != m.key && playing { pause() }
        }
        .onDisappear { pause() }
    }

    private func pause() {
        player?.pause()
        if let web { Task { await web.pause() } }
        playing = false
    }

    private func toggle() {
        guard loaded else { return }
        if playing {
            pause()
        } else {
            ChatAudioPlayer.shared.stop()
            now.key = m.key
            let rate = ChatRepository.shared.store.state.speed
            if let player {
                player.play()
                player.rate = rate
            } else if let web {
                Task { await web.play(rate: rate) }
            }
            playing = true
        }
    }
}

/// An AVPlayer's picture, filling its frame.
struct PlayerLayerView: UIViewRepresentable {
    let player: AVPlayer

    final class LayerView: UIView {
        override static var layerClass: AnyClass { AVPlayerLayer.self }
        var playerLayer: AVPlayerLayer { layer as! AVPlayerLayer }
    }

    func makeUIView(context: Context) -> LayerView {
        let view = LayerView()
        view.playerLayer.videoGravity = .resizeAspectFill
        view.playerLayer.player = player
        return view
    }

    func updateUIView(_ view: LayerView, context: Context) {
        view.playerLayer.player = player
    }
}

/// An attached film: a card that opens the full-screen player.
private struct VideoCard: View {
    let f: ChatFile
    let colors: BubbleColors
    @EnvironmentObject private var nav: ChatNav

    var body: some View {
        Button { nav.push(.video(source: chatMediaSource(f), name: f.name, start: 0)) } label: {
            HStack(spacing: 10) {
                MaterialIcon(MI.roundedPlayArrow, size: 32).foregroundStyle(.white)
                    .frame(width: 54, height: 54).background(Color.black, in: .sky(12))
                VStack(alignment: .leading, spacing: 0) {
                    Text(verbatim: f.name).skyText(.bodyMedium).foregroundStyle(colors.fg).lineLimit(2)
                    Text(verbatim: Skychat.humanSize(f.size)).skyText(.labelSmall).foregroundStyle(colors.meta)
                }
            }
            .frame(minWidth: 200, alignment: .leading)
        }
        .buttonStyle(.plain)
    }
}

// MARK: Viewers

/// The full image with pinch and double-tap zoom (index.html lightbox: 1×–6×, double tap 2.5×).
struct ImageViewer: View {
    let source: String
    let name: String
    let onBack: () -> Void
    @State private var image: UIImage?
    @State private var failed = false
    @State private var scale: CGFloat = 1
    @State private var base: CGFloat = 1
    @State private var offset: CGSize = .zero
    @State private var drag: CGSize = .zero

    var body: some View {
        ZStack(alignment: .top) {
            Color.black.ignoresSafeArea()
            if let image {
                Image(uiImage: image)
                    .resizable()
                    .scaledToFit()
                    .scaleEffect(scale)
                    .offset(x: offset.width + drag.width, y: offset.height + drag.height)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .contentShape(Rectangle())
                    .gesture(MagnificationGesture()
                        .onChanged { scale = min(6, max(1, base * $0)) }
                        .onEnded { _ in
                            base = scale
                            if scale == 1 { offset = .zero }
                        })
                    .simultaneousGesture(DragGesture()
                        .onChanged { if scale > 1 { drag = $0.translation } }
                        .onEnded { value in
                            if scale > 1 { offset.width += value.translation.width; offset.height += value.translation.height }
                            drag = .zero
                        })
                    .onTapGesture(count: 2) {
                        withAnimation {
                            if scale > 1 {
                                scale = 1
                                offset = .zero
                            } else {
                                scale = 2.5
                            }
                            base = scale
                        }
                    }
                    .onTapGesture { if scale == 1 { onBack() } }
                    .accessibilityLabel(Text(verbatim: name))
            } else if failed {
                Text("chat_cannot_open").foregroundStyle(.white).frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                MaterialSpinner(color: .white).frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            ViewerBar(name: name, onBack: onBack)
        }
        .task(id: source) {
            guard let file = await chatMediaFile(source) else {
                failed = true
                return
            }
            image = await Task.detached { ChatImages.decode(file: file, maxEdge: 2560) }.value
            failed = image == nil
        }
    }
}

/// The full-screen player: our own copies and cached clips play from disk, served ones stream with our credential.
struct VideoViewer: View {
    let source: String
    let name: String
    let start: Double
    let onBack: () -> Void
    @State private var player: AVPlayer?
    @State private var web: WebMediaElement?

    var body: some View {
        ZStack(alignment: .top) {
            Color.black.ignoresSafeArea()
            if let player {
                VideoPlayer(player: player).ignoresSafeArea(edges: .bottom).padding(.top, 52)
            } else if let web {
                WebVideoSurface(element: web).ignoresSafeArea(edges: .bottom).padding(.top, 52)
            } else {
                MaterialSpinner(color: .white).frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            ViewerBar(name: name, onBack: onBack)
        }
        .task(id: source) {
            ChatAudioPlayer.shared.stop()
            if WebMedia.needs(name) {
                await playWeb()
                return
            }
            let asset: AVURLAsset
            if source.hasPrefix("file:") {
                asset = AVURLAsset(url: URL(fileURLWithPath: String(source.dropFirst(5))))
            } else if source.hasPrefix("up:"), let url = ChatStore.uploadURL(String(source.dropFirst(3))) {
                asset = AVURLAsset(url: url)
            } else if let cached = ChatRepository.shared.media.cachedFile(source) {
                asset = AVURLAsset(url: cached)
            } else if let base = ChatRepository.shared.skychatURL, let url = URL(string: base.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/")) + source),
                      let secret = try? SkychatOrigin.password() {
                asset = AVURLAsset(url: url, options: ["AVURLAssetHTTPHeaderFieldsKey": ["Authorization": SkychatClient.authorization(password: secret)]])
            } else {
                ChatRepository.shared.note(L10n.text("chat_cannot_play"), error: true)
                return
            }
            let player = AVPlayer(playerItem: AVPlayerItem(asset: asset))
            player.rate = 0
            if start > 0 { await player.seek(to: CMTime(seconds: start, preferredTimescale: 600)) }
            self.player = player
            player.play()
            player.rate = ChatRepository.shared.store.state.speed
        }
        .onAppear { Orientation.setVideoFullScreen(true) }
        .onDisappear {
            player?.pause()
            web?.stop()
            Orientation.setVideoFullScreen(false)
        }
    }

    /// WebM through WebKit, from a file on disk.
    private func playWeb() async {
        let file: URL?
        if source.hasPrefix("file:") {
            file = URL(fileURLWithPath: String(source.dropFirst(5)))
        } else {
            file = await chatMediaFile(source)
        }
        let element = WebMediaElement(video: true, controls: true)
        guard let file, await element.load(file) != nil else {
            ChatRepository.shared.note(L10n.text("chat_cannot_play"), error: true)
            return
        }
        web = element
        if start > 0 { await element.seek(start) }
        await element.play(rate: ChatRepository.shared.store.state.speed)
    }
}

private struct ViewerBar: View {
    let name: String
    let onBack: () -> Void

    var body: some View {
        HStack(spacing: 8) {
            Button(action: onBack) {
                MaterialIcon(MI.roundedArrowBack).foregroundStyle(.white).frame(width: 44, height: 44)
            }
            .accessibilityLabel(Text("chat_close"))
            .accessibilityIdentifier("viewer-close")
            Text(verbatim: name).skyText(.titleSmall).foregroundStyle(.white).lineLimit(1)
            Spacer(minLength: 0)
        }
        .padding(4)
        .background(Color.black.opacity(0.4))
    }
}
