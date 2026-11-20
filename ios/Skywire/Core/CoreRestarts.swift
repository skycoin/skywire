import Foundation

/// When a failed core starts again (Android: SkywireCoreService's loop): 2 s after a short run,
/// doubling to 30 s, and 1 s again after a run of over a minute.
struct CoreRestarts {
    static let first = Duration.seconds(1)
    static let longest = Duration.seconds(30)
    static let stable = Duration.seconds(60)

    /// Starts since the last Connect.
    private(set) var attempts = 0
    private(set) var backoff = CoreRestarts.first
    private var startedAt: ContinuousClock.Instant?

    /// The number Home shows for the next start.
    var next: Int { attempts + 1 }

    /// A Connect: the count and the wait begin again.
    mutating func connected(at now: ContinuousClock.Instant = .now) {
        attempts = 0
        backoff = Self.first
        starting(at: now)
    }

    mutating func starting(at now: ContinuousClock.Instant = .now) {
        attempts += 1
        startedAt = now
    }

    /// The core failed: returns the wait before the next start.
    mutating func failed(at now: ContinuousClock.Instant = .now) -> Duration {
        let ran = startedAt.map { now - $0 } ?? .zero
        backoff = ran > Self.stable ? Self.first : min(backoff * 2, Self.longest)
        return backoff
    }
}
