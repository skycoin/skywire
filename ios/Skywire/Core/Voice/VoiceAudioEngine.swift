import AVFoundation
import CoreClient
import os

/// The phone's microphone and speaker, lent to the visor for the duration of
/// a call (Android: core/VoiceAudio.kt).
///
/// The visor has no audio device of its own here, so it exposes the two ends
/// of its call mixer over the local API and this class plays the device:
/// capture goes up the microphone stream (a POST whose body is this phone's
/// PCM), playback comes down the speaker stream.
///
/// **Everything is raw little-endian int16 PCM at 48000 Hz, mono.** That is
/// the format the visor's call package fixes for every backend, so there is
/// nothing to negotiate — and nothing to notice if it were wrong, since a
/// mismatched rate is not an error, just the wrong pitch.
///
/// Both directions run for as long as the engine does and reconnect on their
/// own: a stream that drops mid-call (the visor restarting under us) should
/// cost a moment of audio, not the call.
///
/// Capture is push (the engine's tap calls us with each buffer) where
/// Android's is pull (`AudioRecord.read`); the upload's writer thread turns
/// ours back into a pull, because the HTTP body only knows pull: it writes
/// what is queued and blocks when the visor stops reading. A blocked queue
/// drops the oldest second — it is live audio, and late is worthless.
final class VoiceAudioEngine: @unchecked Sendable {

    /// Fixed by the visor's call package (`call.SampleRate` /
    /// `call.FrameSamples`). Changing either here alone corrupts audio
    /// silently — both ends must move together.
    static let sampleRate: Double = 48_000
    /// One tap buffer: 20 ms, the visor's frame.
    private static let frameSamples = 960

    private let log = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "voice-audio")
    private let condition = NSCondition()

    private struct Shared {
        var live = false
        var capturing = false
        /// Frames waiting for the upload, and their total size.
        var backlog: [Data] = []
        var backlogBytes = 0
        /// Upload writers currently inside `writeFrames`: the release test
        /// reads it — a writer that never returns is a microphone held for
        /// the life of the process, which is what that test exists to catch.
        var activeWriters = 0
        /// Buffers the tap has delivered, for the same test: a tap that
        /// still delivers after `stop` is still holding the microphone.
        var framesCaptured = 0
        /// The tap's converter and its output format, set when the tap is
        /// installed.
        var converter: AVAudioConverter?
        var captureFormat: AVAudioFormat?
    }

    private var shared = Shared()
    private var loops: [Task<Void, Never>] = []
    /// Two engines, deliberately. An input node's format is settled when
    /// its engine first renders, and a graph started without input in it
    /// settles on 0 Hz for good (measured: stop does not put it back) — so
    /// the engine that captures is started only once its tap is in, and
    /// playback, which begins before the microphone's permission may have
    /// landed, runs on one of its own.
    private let captureEngine = AVAudioEngine()
    private let playbackEngine = AVAudioEngine()
    private let player = AVAudioPlayerNode()
    private var playerAttached = false
    /// Frames already scheduled on the player and not yet played; more than
    /// `playaheadLimit` ahead means the device stalled and the rest is
    /// dropped, or memory would grow for as long as the call does.
    private var scheduledFrames: AVAudioFramePosition = 0
    private var playedFrames: AVAudioFramePosition = 0
    private var playaheadLimit: AVAudioFramePosition = 24_000
    /// A byte of a sample split across two speaker chunks.
    private var pendingByte: UInt8?

    /// Whether the tap is installed and the engine running: the microphone
    /// is held. Written only where the tap is installed and removed, so it
    /// tracks the device rather than the intent to use it.
    var capturing: Bool {
        condition.lock(); defer { condition.unlock() }
        return shared.capturing
    }

    /// How many upload writers are parked inside the body producer, and how
    /// many buffers the tap has delivered. For the release test.
    var writersAndFrames: (writers: Int, frames: Int) {
        condition.lock(); defer { condition.unlock() }
        return (shared.activeWriters, shared.framesCaptured)
    }

    enum SessionOwner {
        /// The engine activates and deactivates the AVAudioSession itself
        /// (a call answered in the app).
        case engine
        /// CallKit activated the session and owns it (a call answered from
        /// the system's UI); the engine configures it but never sets it
        /// active or inactive.
        case callKit
    }

    private let sessionOwner: SessionOwner

    init(sessionOwner: SessionOwner = .engine) {
        self.sessionOwner = sessionOwner
    }

    /// Starts both directions. Idempotent: the watcher may re-observe the
    /// same active call.
    ///
    /// The microphone permission is waited for rather than required up
    /// front: a call can arrive before the user has ever granted it, and the
    /// honest behaviour then is a call they can HEAR while the grant is still
    /// outstanding (Android's capture loop does the same). Playback begins
    /// at once; capture joins when the permission lands.
    func start(client: CoreClient) {
        condition.lock()
        if shared.live {
            condition.unlock()
            return
        }
        shared.live = true
        condition.unlock()

        configureSession()
        do {
            try startPlayback()
        } catch {
            log.error("cannot start playback: \(error.localizedDescription, privacy: .public)")
        }
        loops.append(Task { [weak self] in await self?.playbackLoop(client) })
        loops.append(Task { [weak self] in await self?.captureLoop(client) })
    }

    /// Stops both directions and hands the device back. The flag falls
    /// first, the queued upload wakes and returns (ending its request), the
    /// in-flight one is cancelled by its own task ending, and the tap —
    /// which is the microphone being held — is removed on the audio thread's
    /// own good time, with `capturing` going false with it.
    func stop() {
        condition.lock()
        shared.live = false
        shared.backlog = []
        shared.backlogBytes = 0
        condition.unlock()
        condition.broadcast()

        for loop in loops {
            loop.cancel()
        }
        loops = []

        captureEngine.inputNode.removeTap(onBus: 0)
        if captureEngine.isRunning {
            captureEngine.stop()
        }
        player.stop()
        if playbackEngine.isRunning {
            playbackEngine.stop()
        }
        condition.lock()
        shared.capturing = false
        condition.unlock()

        if sessionOwner == .engine {
            try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
        }
    }

    // MARK: Session

    /// Voice-call routing: earpiece rather than the media speaker, the input
    /// tuned for speech, and the platform's echo canceller applied — the
    /// visor has none, so without this the peer hears themselves back
    /// through our speaker. Earpiece ⇄ speaker is switched on top of this by
    /// `setSpeakerphone`, the phone's business, not the visor's.
    private func configureSession() {
        let session = AVAudioSession.sharedInstance()
        do {
            try session.setCategory(.playAndRecord, mode: .voiceChat, options: [])
        } catch {
            log.error("cannot set the call's audio mode: \(error.localizedDescription, privacy: .public)")
        }
        if sessionOwner == .engine {
            do {
                try session.setActive(true)
            } catch {
                log.error("cannot activate the call's audio: \(error.localizedDescription, privacy: .public)")
            }
        }
    }

    /// Earpiece ⇄ speakerphone, for the call screen's toggle.
    func setSpeakerphone(_ on: Bool) {
        try? AVAudioSession.sharedInstance().overrideOutputAudioPort(on ? .speaker : .none)
    }

    // MARK: Microphone → visor

    private func captureLoop(_ client: CoreClient) async {
        while live {
            switch AVAudioSession.sharedInstance().recordPermission {
            case .granted:
                break
            case .undetermined:
                // The prompt is up (or about to be); a moment later is the
                // earliest it can be answered.
                try? await Task.sleep(for: .seconds(1.5))
                continue
            case .denied:
                // A call they can hear, and we cannot send. Retry anyway:
                // the Settings toggle can land mid-call.
                try? await Task.sleep(for: .seconds(1.5))
                continue
            @unknown default:
                try? await Task.sleep(for: .seconds(1.5))
                continue
            }
            do {
                installTap()
                let head = try await client.voiceMicStream { [weak self] output in
                    self?.writeFrames(to: output)
                }
                if !head.isSuccess {
                    log.notice("mic stream rejected (\(head.status, privacy: .public))")
                }
            } catch {
                if !live { break }
                log.notice("mic stream ended: \(error.localizedDescription, privacy: .public)")
            }
            removeTap()
            guard live else { break }
            try? await Task.sleep(for: .milliseconds(500))
        }
        removeTap()
    }

    private func installTap() {
        let input = captureEngine.inputNode
        let hardware = input.outputFormat(forBus: 0)
        guard hardware.channelCount > 0, hardware.sampleRate > 0 else { return }
        // The tap must run in the hardware's format (an input node converts
        // nothing), so whatever it delivers — channels, rate, and with
        // .voiceChat the input is often 24 kHz — is converted to the call's
        // one format here, one converter per tap, its state kept across
        // buffers the way a stream converter is meant to be used.
        guard let target = AVAudioFormat(standardFormatWithSampleRate: Self.sampleRate, channels: 1) else { return }
        guard let converter = AVAudioConverter(from: hardware, to: target) else { return }
        condition.lock()
        shared.converter = converter
        shared.captureFormat = target
        condition.unlock()
        let frames: AVAudioFrameCount = max(256, AVAudioFrameCount((Double(Self.frameSamples) * hardware.sampleRate / Self.sampleRate).rounded()))
        input.installTap(onBus: 0, bufferSize: frames, format: hardware) { [weak self] buffer, _ in
            self?.captured(buffer)
        }
        do {
            try captureEngine.start()
        } catch {
            self.log.error("cannot start capture: \(error.localizedDescription, privacy: .public)")
            input.removeTap(onBus: 0)
            condition.lock()
            shared.converter = nil
            shared.captureFormat = nil
            condition.unlock()
            return
        }
        condition.lock()
        shared.capturing = true
        condition.unlock()
    }

    private func removeTap() {
        condition.lock()
        let wasCapturing = shared.capturing
        shared.capturing = false
        shared.converter = nil
        shared.captureFormat = nil
        condition.unlock()
        guard wasCapturing else { return }
        captureEngine.inputNode.removeTap(onBus: 0)
        if captureEngine.isRunning {
            captureEngine.stop()
        }
    }

    /// One tap buffer: the hardware's format → the call's (48 kHz mono
    /// Float32) → int16 → the queue. Never blocks: the tap runs on an audio
    /// thread, and backpressure is the writer's job.
    private func captured(_ buffer: AVAudioPCMBuffer) {
        condition.lock()
        let converter = shared.converter
        let target = shared.captureFormat
        condition.unlock()
        guard let converter, let target else { return }
        let capacity = AVAudioFrameCount(Double(buffer.frameLength) * Self.sampleRate / buffer.format.sampleRate) + 64
        guard let converted = AVAudioPCMBuffer(pcmFormat: target, frameCapacity: capacity) else { return }
        var fed = false
        var conversionError: NSError?
        let status = converter.convert(to: converted, error: &conversionError) { _, inputStatus in
            if fed {
                inputStatus.pointee = .noDataNow
                return nil
            }
            fed = true
            inputStatus.pointee = .haveData
            return buffer
        }
        guard status != .error, converted.frameLength > 0, let floats = converted.floatChannelData?[0] else { return }
        let frames = Int(converted.frameLength)
        var data = Data(capacity: frames * 2)
        for i in 0..<frames {
            // The visor's int16, scaled and clipped the way every backend
            // scales it.
            let scaled = max(-1, min(1, floats[i]))
            let sample = Int16(scaled >= 0 ? scaled * 32767 : scaled * 32768)
            data.append(contentsOf: withUnsafeBytes(of: sample.littleEndian) { Array($0) })
        }
        condition.lock()
        guard shared.live else {
            condition.unlock()
            return
        }
        shared.framesCaptured += 1
        shared.backlog.append(data)
        shared.backlogBytes += data.count
        // A full second queued means the writer is blocked: keep the newest,
        // drop the oldest — late audio is worthless audio.
        while shared.backlogBytes > 96_000, !shared.backlog.isEmpty {
            shared.backlogBytes -= shared.backlog.removeFirst().count
        }
        condition.unlock()
        condition.broadcast()
    }

    /// The upload's body IS the microphone: it writes what capture queued
    /// until the engine stops or the stream breaks. Runs on the transport's
    /// writer thread, where blocking is backpressure.
    private func writeFrames(to output: OutputStream) {
        condition.lock()
        shared.activeWriters += 1
        condition.unlock()
        defer {
            condition.lock()
            shared.activeWriters -= 1
            condition.unlock()
        }
        while live {
            condition.lock()
            while shared.backlog.isEmpty && shared.live {
                condition.wait()
            }
            let chunk = shared.backlog.isEmpty ? nil : shared.backlog.removeFirst()
            shared.backlogBytes -= chunk?.count ?? 0
            condition.unlock()
            guard let chunk else { return }
            var sent = 0
            while sent < chunk.count {
                let written = chunk.withUnsafeBytes { raw in
                    output.write(
                        raw.baseAddress!.assumingMemoryBound(to: UInt8.self) + sent,
                        maxLength: chunk.count - sent
                    )
                }
                if written <= 0 {
                    // The stream broke (the visor stopped reading, or the
                    // request was cancelled): ending the body is what
                    // reconnects it.
                    return
                }
                sent += written
            }
        }
    }

    // MARK: Visor → speaker

    private func playbackLoop(_ client: CoreClient) async {
        while live {
            do {
                let stream = try await client.voiceSpeakerStream()
                guard stream.isSuccess else {
                    log.notice("speaker stream rejected (\(stream.status, privacy: .public))")
                    try await Task.sleep(for: .milliseconds(500))
                    continue
                }
                for try await chunk in stream.body {
                    guard live else { break }
                    schedule(chunk)
                }
            } catch is CancellationError {
                return
            } catch {
                if !live { return }
                log.notice("speaker stream ended: \(error.localizedDescription, privacy: .public)")
            }
            guard live else { break }
            try? await Task.sleep(for: .milliseconds(500))
        }
    }

    private func startPlayback() throws {
        guard !playerAttached else {
            if !player.isPlaying { player.play() }
            return
        }
        guard let format = AVAudioFormat(standardFormatWithSampleRate: Self.sampleRate, channels: 1) else { return }
        playbackEngine.attach(player)
        playbackEngine.connect(player, to: playbackEngine.mainMixerNode, format: format)
        playerAttached = true
        if !playbackEngine.isRunning {
            try playbackEngine.start()
        }
        player.play()
    }

    /// Int16 LE mono → a player buffer. A chunk can split a sample; the odd
    /// byte leads the next one.
    private func schedule(_ chunk: Data) {
        var bytes = chunk
        if let pendingByte {
            bytes = Data([pendingByte]) + bytes
            self.pendingByte = nil
        }
        if bytes.count % 2 == 1 {
            self.pendingByte = bytes.last
            bytes = bytes.dropLast()
        }
        let frames = bytes.count / 2
        guard frames > 0 else { return }

        condition.lock()
        // A device stalled for half a second: drop what piled up rather than
        // bank it.
        if scheduledFrames - playedFrames > playaheadLimit {
            condition.unlock()
            return
        }
        condition.unlock()

        guard let format = AVAudioFormat(standardFormatWithSampleRate: Self.sampleRate, channels: 1),
              let buffer = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: AVAudioFrameCount(frames))
        else { return }
        buffer.frameLength = AVAudioFrameCount(frames)
        guard let floats = buffer.floatChannelData?[0] else { return }
        bytes.withUnsafeBytes { raw in
            let samples = raw.bindMemory(to: Int16.self)
            for i in 0..<frames {
                floats[i] = Float(Int16(littleEndian: samples[i])) / 32_768
            }
        }
        condition.lock()
        scheduledFrames += AVAudioFramePosition(frames)
        condition.unlock()
        let this = self
        player.scheduleBuffer(buffer) {
            this.condition.lock()
            this.playedFrames += AVAudioFramePosition(frames)
            this.condition.unlock()
        }
    }

    private var live: Bool {
        condition.lock(); defer { condition.unlock() }
        return shared.live
    }
}
