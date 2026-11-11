import AVFoundation
import Foundation

/// What the caller hears while the other side rings (Android:
/// CallViewModel's ToneGenerator and MediaPlayer legs).
///
/// iOS offers no tone generator, so the two network tones are synthesized as
/// little WAVs and played by AVAudioPlayer: the ordinary ring (the
/// 440+480 Hz pair a North American phone plays, 2 s on, 4 s off) and the
/// busy signal (480+620 Hz, half a second on and off, three bursts). The
/// other side's OWN ringback tone, when it sends one, is its bytes, played
/// as they are.
@MainActor
final class CallTones {
    private var player: AVAudioPlayer?

    /// The network's ordinary ring, looped, until `stop()`.
    func playRing() {
        guard player == nil else { return }
        play(wav: Self.ring, numberOfLoops: -1, volume: 0.6)
    }

    /// The other side's own ringback tone, looped. Returns false when the
    /// bytes would not play (the ordinary ring stays up in its place).
    func playCustomRingback(_ data: Data) -> Bool {
        guard let custom = try? AVAudioPlayer(data: data) else { return false }
        stop()
        custom.numberOfLoops = -1
        custom.volume = 0.6
        custom.prepareToPlay()
        custom.play()
        player = custom
        return true
    }

    /// Three bursts of the busy signal: the call did not go through.
    func playEndedSignal() {
        play(wav: Self.busy, numberOfLoops: 0, volume: 0.6)
    }

    func stop() {
        player?.stop()
        player = nil
    }

    private func play(wav: Data, numberOfLoops: Int, volume: Float) {
        guard let new = try? AVAudioPlayer(data: wav) else { return }
        stop()
        new.numberOfLoops = numberOfLoops
        new.volume = volume
        new.prepareToPlay()
        new.play()
        player = new
    }

    // MARK: The tones, as WAV bytes

    /// 440+480 Hz for 2 s, then 4 s of silence, at the call's rate. looping
    /// this is the cadence every landline in North America rings in.
    private static let ring: Data = tone([440, 480], onSeconds: 2.0, offSeconds: 4.0, bursts: 1)

    /// 480+620 Hz, half-second on and off, three times.
    private static let busy: Data = tone([480, 620], onSeconds: 0.5, offSeconds: 0.5, bursts: 3)

    private static func tone(_ freqs: [Double], onSeconds: Double, offSeconds: Double, bursts: Int) -> Data {
        let rate = 48_000.0
        let onSamples = Int(onSeconds * rate)
        let offSamples = Int(offSeconds * rate)
        let total = (onSamples + offSamples) * bursts
        var samples = [Int16](repeating: 0, count: total)
        for burst in 0..<bursts {
            let start = burst * (onSamples + offSamples)
            for i in 0..<onSamples {
                let t = Double(i) / rate
                let wave = freqs.reduce(0.0) { $0 + sin(2 * .pi * $1 * t) } / Double(freqs.count)
                samples[start + i] = Int16(max(-1, min(1, wave * 0.8)) * 32_767)
            }
        }
        return wav(samples, rate: Int(rate))
    }

    /// The smallest WAV: a 16-bit mono PCM header around the samples.
    private static func wav(_ samples: [Int16], rate: Int) -> Data {
        var data = Data()
        func append(_ text: String) { data.append(contentsOf: text.utf8) }
        func append32(_ value: UInt32) {
            var v = value.littleEndian
            withUnsafeBytes(of: &v) { data.append(contentsOf: $0) }
        }
        func append16(_ value: UInt16) {
            var v = value.littleEndian
            withUnsafeBytes(of: &v) { data.append(contentsOf: $0) }
        }
        let bytes = samples.count * 2
        append("RIFF")
        append32(UInt32(36 + bytes))
        append("WAVE")
        append("fmt ")
        append32(16)
        append16(1)
        append16(1)
        append32(UInt32(rate))
        append32(UInt32(rate * 2))
        append16(2)
        append16(16)
        append("data")
        append32(UInt32(bytes))
        for sample in samples {
            append16(UInt16(bitPattern: sample))
        }
        return data
    }
}
