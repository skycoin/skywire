@preconcurrency import AVFoundation
import CoreClient
import CryptoKit
import Foundation
import ImageIO
import UIKit

/// Local copies of served chat files (`/files/…`, `/thumb/…`): voice messages
/// and round videos play from disk, thumbnails decode from it. Bounded,
/// oldest first out (Android: chat/ChatMediaCache.kt).
actor ChatMediaCache {
    private let api: SkychatClient
    nonisolated let dir: URL
    private var inflight: [String: Task<URL?, Never>] = [:]

    static let maxBytes: Int64 = 256 * 1024 * 1024

    init(api: SkychatClient) {
        self.api = api
        dir = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0].appendingPathComponent("chat-media", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    nonisolated func cachedFile(_ path: String) -> URL? {
        let file = fileFor(path)
        let size = (try? FileManager.default.attributesOfItem(atPath: file.path)[.size] as? Int64) ?? 0
        return size > 0 ? file : nil
    }

    /// The served file at `path` on disk, downloading it once if needed; nil when it cannot be had.
    func get(_ path: String) async -> URL? {
        if let file = cachedFile(path) {
            try? FileManager.default.setAttributes([.modificationDate: Date()], ofItemAtPath: file.path)
            return file
        }
        if let running = inflight[path] { return await running.value }
        let target = fileFor(path)
        let api = api
        let task = Task<URL?, Never> {
            do {
                try await api.download(path, to: target)
                return target
            } catch {
                ChatRepository.log.debug("fetch \(path, privacy: .public) failed: \(error.localizedDescription, privacy: .public)")
                return nil
            }
        }
        inflight[path] = task
        let result = await task.value
        inflight[path] = nil
        if result != nil { prune() }
        return result
    }

    nonisolated func drop(_ path: String) {
        try? FileManager.default.removeItem(at: fileFor(path))
    }

    nonisolated private func fileFor(_ path: String) -> URL {
        let digest = SHA256.hash(data: Data(path.utf8)).prefix(16).map { String(format: "%02x", $0) }.joined()
        let ext = String(((path as NSString).lastPathComponent as NSString).pathExtension.prefix(8).filter { $0.isLetter || $0.isNumber })
        return dir.appendingPathComponent(ext.isEmpty ? digest : digest + "." + ext)
    }

    private func prune() {
        let keys: [URLResourceKey] = [.fileSizeKey, .contentModificationDateKey]
        guard let files = try? FileManager.default.contentsOfDirectory(at: dir, includingPropertiesForKeys: keys) else { return }
        let entries = files.compactMap { url -> (URL, Int64, Date)? in
            guard let values = try? url.resourceValues(forKeys: Set(keys)), let size = values.fileSize else { return nil }
            return (url, Int64(size), values.contentModificationDate ?? .distantPast)
        }
        var total = entries.reduce(0) { $0 + $1.1 }
        guard total > Self.maxBytes else { return }
        for (url, size, _) in entries.sorted(by: { $0.2 < $1.2 }) {
            try? FileManager.default.removeItem(at: url)
            total -= size
            if total <= Self.maxBytes * 3 / 4 { break }
        }
    }
}

/// Decoding, a small memory cache, and the avatar encoder the profile and group meta share.
enum ChatImages {
    nonisolated(unsafe) private static let cache: NSCache<NSString, UIImage> = {
        let cache = NSCache<NSString, UIImage>()
        cache.totalCostLimit = 24 * 1024 * 1024
        return cache
    }()

    static func cached(_ key: String) -> UIImage? { cache.object(forKey: key as NSString) }

    static func remember(_ key: String, _ image: UIImage) {
        cache.setObject(image, forKey: key as NSString, cost: Int(image.size.width * image.size.height * image.scale * image.scale * 4))
    }

    /// `data` no larger than `maxEdge` pixels on its longer side, upright.
    static func decode(_ data: Data, maxEdge: Int) -> UIImage? {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil) else { return nil }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: maxEdge,
        ]
        guard let image = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else { return nil }
        return UIImage(cgImage: image)
    }

    static func decode(file: URL, maxEdge: Int) -> UIImage? {
        (try? Data(contentsOf: file)).flatMap { decode($0, maxEdge: maxEdge) }
    }

    /// A square avatar under `maxBytes`: centre-cropped, scaled to at most
    /// `maxEdge`, PNG when that fits and JPEG at falling quality otherwise,
    /// the web page's steps, so the cap is never the user's problem.
    static func encodeAvatar(_ source: UIImage, maxEdge: Int, maxBytes: Int) -> Data? {
        let pixels = CGSize(width: source.size.width * source.scale, height: source.size.height * source.scale)
        var edge = min(maxEdge, Int(min(pixels.width, pixels.height)))
        while edge >= 48 {
            let square = squareCrop(source, edge: edge)
            if let png = square.pngData(), png.count <= maxBytes { return png }
            for quality in [0.92, 0.85, 0.75, 0.65, 0.55, 0.45] {
                if let jpeg = square.jpegData(compressionQuality: quality), jpeg.count <= maxBytes { return jpeg }
            }
            edge = edge * 3 / 4
        }
        return nil
    }

    private static func squareCrop(_ source: UIImage, edge: Int) -> UIImage {
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        let size = CGSize(width: edge, height: edge)
        return UIGraphicsImageRenderer(size: size, format: format).image { context in
            // White first, as the web page does, so a transparent picture is not black as JPEG.
            UIColor.white.setFill()
            context.fill(CGRect(origin: .zero, size: size))
            let side = min(source.size.width, source.size.height)
            let scale = CGFloat(edge) / side
            let drawn = CGSize(width: source.size.width * scale, height: source.size.height * scale)
            source.draw(in: CGRect(x: (CGFloat(edge) - drawn.width) / 2, y: (CGFloat(edge) - drawn.height) / 2, width: drawn.width, height: drawn.height))
        }
    }
}

/// Waveform bars for a voice message: decode to PCM and keep each bucket's peak.
enum AudioPeaks {
    /// `bars` values in 0…1 and the clip's length in ms, or nil when it does not decode.
    static func compute(_ url: URL, bars: Int) -> (peaks: [Float], durationMs: Int64)? {
        guard let file = try? AVAudioFile(forReading: url) else { return nil }
        let format = file.processingFormat
        let stride = max(1, Int(format.sampleRate / 100))
        guard let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: 16384) else { return nil }
        var samples: [Float] = []
        var acc: Float = 0
        var n = 0
        while file.framePosition < file.length {
            do {
                try file.read(into: buffer)
            } catch {
                break
            }
            guard buffer.frameLength > 0, let channel = buffer.floatChannelData?[0] else { break }
            for i in 0..<Int(buffer.frameLength) {
                acc = max(acc, abs(channel[i]))
                n += 1
                if n >= stride {
                    samples.append(acc)
                    acc = 0
                    n = 0
                }
            }
        }
        guard !samples.isEmpty else { return nil }
        let duration = Int64(Double(file.length) / format.sampleRate * 1000)
        return (reduce(samples, bars: bars), duration)
    }

    static func reduce(_ samples: [Float], bars: Int) -> [Float] {
        var out = [Float](repeating: 0, count: bars)
        guard !samples.isEmpty else { return out }
        for i in 0..<bars {
            let from = i * samples.count / bars
            let to = max(from + 1, (i + 1) * samples.count / bars)
            out[i] = samples[from..<min(to, samples.count)].max() ?? 0
        }
        guard let top = out.max(), top > 0 else { return out }
        return out.map { min(1, max(0.06, $0 / top)) }
    }
}

/// One clip at a time: voice messages and attached audio (Android: chat/ChatAudioPlayer.kt).
@MainActor
final class ChatAudioPlayer: NSObject, ObservableObject, AVAudioPlayerDelegate {
    static let shared = ChatAudioPlayer()
    static let speeds: [Float] = [0.75, 1, 1.25, 1.5, 2]

    @Published private(set) var key: String?
    @Published private(set) var playing = false
    @Published private(set) var positionMs: Int64 = 0
    @Published private(set) var durationMs: Int64 = 0

    private var player: AVAudioPlayer?
    /// WebM clips, which AVFoundation cannot open (WebMedia).
    private var web: WebMediaElement?
    private var webFile: URL?
    private var ticker: Task<Void, Never>?
    private var speed: Float = 1

    func toggle(_ key: String, _ file: URL) {
        if self.key == key, player != nil || webFile != nil {
            if playing { pause() } else { resume() }
            return
        }
        play(key, file, at: nil)
    }

    func play(_ key: String, _ file: URL, at fraction: Float?) {
        stop()
        guard let player = WebMedia.needs(file.lastPathComponent) ? nil : try? AVAudioPlayer(contentsOf: file) else {
            playWeb(key, file, at: fraction)
            return
        }
        if !VoiceCalls.shared.state.busy {
            try? AVAudioSession.sharedInstance().setCategory(.playback, mode: .spokenAudio)
            try? AVAudioSession.sharedInstance().setActive(true)
        }
        player.delegate = self
        player.enableRate = true
        player.rate = speed
        if let fraction { player.currentTime = player.duration * Double(fraction) }
        self.player = player
        self.key = key
        durationMs = Int64(player.duration * 1000)
        player.play()
        playing = true
        tick()
    }

    /// The WebKit route: the same controls over a media element.
    private func playWeb(_ key: String, _ file: URL, at fraction: Float?) {
        let element = web ?? WebMediaElement(video: false)
        element.attachHidden()
        web = element
        webFile = file
        self.key = key
        playing = true
        Task {
            guard let duration = await element.load(file), self.key == key else {
                if self.key == key { stop() }
                ChatRepository.shared.note(L10n.text("chat_cannot_play"), error: true)
                return
            }
            durationMs = Int64(duration * 1000)
            if let fraction { await element.seek(duration * Double(fraction)) }
            await element.play(rate: speed)
            tick()
        }
    }

    func pause() {
        player?.pause()
        if webFile != nil, let web { Task { await web.pause() } }
        playing = false
        report()
    }

    func resume() {
        if let player {
            player.play()
        } else if webFile != nil, let web {
            Task { await web.play(rate: speed) }
        } else {
            return
        }
        playing = true
        tick()
    }

    func seek(_ key: String, _ fraction: Float) {
        guard self.key == key else { return }
        let f = Double(min(1, max(0, fraction)))
        if let player {
            player.currentTime = player.duration * f
            report()
        } else if webFile != nil, let web {
            positionMs = Int64(Double(durationMs) * f)
            Task { await web.seek(Double(durationMs) / 1000 * f) }
        }
    }

    func setSpeed(_ value: Float) {
        speed = Self.speeds.contains(value) ? value : 1
        player?.rate = speed
        if webFile != nil, let web { Task { await web.setRate(speed) } }
    }

    func stop() {
        ticker?.cancel()
        ticker = nil
        player?.stop()
        player = nil
        if webFile != nil { web?.stop() }
        webFile = nil
        key = nil
        playing = false
        positionMs = 0
        durationMs = 0
    }

    private func report() {
        guard let player else { return }
        positionMs = Int64(player.currentTime * 1000)
    }

    private func tick() {
        ticker?.cancel()
        ticker = Task { [weak self] in
            while !Task.isCancelled {
                guard let self, self.playing else { return }
                if self.webFile != nil, let web = self.web {
                    guard let state = await web.state(), self.webFile != nil else { return }
                    self.positionMs = Int64(state.position * 1000)
                    if state.duration > 0 { self.durationMs = Int64(state.duration * 1000) }
                    if !state.playing && state.duration > 0 && state.position >= state.duration - 0.05 {
                        self.stop()
                        return
                    }
                } else {
                    self.report()
                }
                try? await Task.sleep(for: .milliseconds(100))
            }
        }
    }

    nonisolated func audioPlayerDidFinishPlaying(_ player: AVAudioPlayer, successfully flag: Bool) {
        Task { @MainActor in self.stop() }
    }
}

/// Records a voice message into the upload folder (Android: chat/VoiceRecorder.kt).
/// Named `voice-<stamp>.m4a`: the prefix is how every skychat UI tells a
/// recorded clip from an attached file (index.html isVoiceMessage). AAC in
/// MP4, which every browser the desktop UI runs in plays.
@MainActor
final class VoiceRecorder {
    private var recorder: AVAudioRecorder?
    private var startedAt = Date()
    private(set) var file: URL?

    static let minimum: TimeInterval = 0.5

    var recording: Bool { recorder != nil }
    var elapsedMs: Int64 { recorder == nil ? 0 : Int64(Date().timeIntervalSince(startedAt) * 1000) }

    func start() throws {
        _ = stop(discard: true)
        let dir = ChatStore.uploads.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let out = dir.appendingPathComponent("voice-\(Skychat.clipStamp()).m4a")
        let session = AVAudioSession.sharedInstance()
        try session.setCategory(.playAndRecord, mode: .default, options: [.defaultToSpeaker])
        try session.setActive(true)
        let settings: [String: Any] = [
            AVFormatIDKey: kAudioFormatMPEG4AAC,
            AVSampleRateKey: 44_100,
            AVNumberOfChannelsKey: 1,
            AVEncoderBitRateKey: 64_000,
        ]
        let r = try AVAudioRecorder(url: out, settings: settings)
        r.isMeteringEnabled = true
        guard r.record() else {
            try? FileManager.default.removeItem(at: dir)
            throw CocoaError(.fileWriteUnknown)
        }
        recorder = r
        file = out
        startedAt = Date()
    }

    /// The current input level, 0…1.
    func sample() -> Float {
        guard let r = recorder else { return 0 }
        r.updateMeters()
        // dBFS to a linear level, with the quiet end lifted so speech shows.
        return min(1, max(0, pow(10, r.averagePower(forChannel: 0) / 20) * 2))
    }

    /// Stops; the finished clip's upload path unless `discard` or it is too short to keep.
    func stop(discard: Bool) -> String? {
        guard let r = recorder, let out = file else { return nil }
        recorder = nil
        file = nil
        let tooShort = Date().timeIntervalSince(startedAt) < Self.minimum
        r.stop()
        let size = (try? FileManager.default.attributesOfItem(atPath: out.path)[.size] as? Int64) ?? 0
        if discard || tooShort || size == 0 {
            try? FileManager.default.removeItem(at: out.deletingLastPathComponent())
            return nil
        }
        return out.deletingLastPathComponent().lastPathComponent + "/" + out.lastPathComponent
    }
}

/// Records a round video message (Android: chat/VideoNoteRecorder.kt): the
/// camera and microphone into a QuickTime file, then a passthrough export to
/// `video-<stamp>.mp4` (`video-sq-…` for the square shape), the names every
/// skychat UI reads as recorded in the composer.
@MainActor
final class VideoNoteRecorder: NSObject, ObservableObject, AVCaptureFileOutputRecordingDelegate {
    let session = AVCaptureSession()
    private let output = AVCaptureMovieFileOutput()
    private var videoInput: AVCaptureDeviceInput?
    private var finished: CheckedContinuation<Bool, Never>?
    private var startedAt = Date()
    private var movie: URL?
    private var square = false
    private(set) var front = true

    var isRecording: Bool { output.isRecording }
    var elapsedMs: Int64 { output.isRecording ? Int64(Date().timeIntervalSince(startedAt) * 1000) : 0 }

    static var available: Bool { AVCaptureDevice.default(for: .video) != nil }

    /// Opens the camera (front when asked and present) and the microphone.
    func bind(front: Bool) throws {
        self.front = front
        session.beginConfiguration()
        defer { session.commitConfiguration() }
        if session.canSetSessionPreset(.vga640x480) { session.sessionPreset = .vga640x480 }
        if let videoInput { session.removeInput(videoInput) }
        let position: AVCaptureDevice.Position = front ? .front : .back
        guard let camera = AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: position) ?? AVCaptureDevice.default(for: .video) else {
            throw CocoaError(.featureUnsupported)
        }
        let input = try AVCaptureDeviceInput(device: camera)
        guard session.canAddInput(input) else { throw CocoaError(.featureUnsupported) }
        session.addInput(input)
        videoInput = input
        if !session.inputs.contains(where: { ($0 as? AVCaptureDeviceInput)?.device.hasMediaType(.audio) == true }),
           let mic = AVCaptureDevice.default(for: .audio), let micInput = try? AVCaptureDeviceInput(device: mic), session.canAddInput(micInput) {
            session.addInput(micInput)
        }
        if !session.outputs.contains(output), session.canAddOutput(output) { session.addOutput(output) }
        if let connection = output.connection(with: .video), connection.isVideoMirroringSupported {
            connection.isVideoMirrored = front
        }
        if !session.isRunning {
            nonisolated(unsafe) let running = session
            DispatchQueue.global(qos: .userInitiated).async { running.startRunning() }
        }
    }

    func start(square: Bool) throws {
        guard session.isRunning || !session.inputs.isEmpty else { throw CocoaError(.featureUnsupported) }
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("chat-rec", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let file = dir.appendingPathComponent(UUID().uuidString + ".mov")
        self.square = square
        movie = file
        startedAt = Date()
        output.startRecording(to: file, recordingDelegate: self)
    }

    /// Stops; the clip's upload path unless `discard` or it did not record.
    func stop(discard: Bool) async -> String? {
        guard output.isRecording, let movie else { return nil }
        let tooShort = Date().timeIntervalSince(startedAt) < 0.5
        let ok = await withCheckedContinuation { continuation in
            finished = continuation
            output.stopRecording()
        }
        self.movie = nil
        defer { try? FileManager.default.removeItem(at: movie) }
        guard ok, !discard, !tooShort else { return nil }
        let folder = UUID().uuidString
        let dir = ChatStore.uploads.appendingPathComponent(folder, isDirectory: true)
        let name = (square ? "video-sq-" : "video-") + Skychat.clipStamp() + ".mp4"
        let out = dir.appendingPathComponent(name)
        do {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            guard let export = AVAssetExportSession(asset: AVURLAsset(url: movie), presetName: AVAssetExportPresetPassthrough) else { return nil }
            export.outputURL = out
            export.outputFileType = .mp4
            export.shouldOptimizeForNetworkUse = true
            await export.export()
            guard export.status == .completed else { return nil }
            return folder + "/" + name
        } catch {
            return nil
        }
    }

    func release() {
        if output.isRecording { output.stopRecording() }
        nonisolated(unsafe) let running = session
        DispatchQueue.global(qos: .userInitiated).async { running.stopRunning() }
    }

    nonisolated func fileOutput(_ output: AVCaptureFileOutput, didFinishRecordingTo outputFileURL: URL, from connections: [AVCaptureConnection], error: (any Error)?) {
        let success = error == nil || ((error as NSError?)?.userInfo[AVErrorRecordingSuccessfullyFinishedKey] as? Bool ?? false)
        Task { @MainActor in
            self.finished?.resume(returning: success)
            self.finished = nil
        }
    }
}
