import CoreClient
import Foundation

/// What Home shows while the core runs: the visor's summary and service
/// health, polled every four seconds while the screen is up and the API
/// answers (Android: HomeViewModel.pollWhileRunning), and Home's two actions.
@MainActor
final class HomeModel: ObservableObject {
    @Published private(set) var summary: VisorSummary?
    @Published private(set) var health: [ServiceHealthEntry] = []
    /// A poll that failed; cleared by the next one that works.
    @Published private(set) var error: String?
    /// The outcome of the last action (dmsg reconnect), shown once.
    @Published var notice: String?

    static let refreshInterval: Duration = .seconds(4)

    /// Polls until cancelled (SwiftUI's `.task` ends it when Home goes away or
    /// the connection changes). A failure only waits for the next tick: the
    /// client logs in again by itself, so a restart is a blip, not an end.
    func poll(_ app: AppModel) async {
        guard app.connected else {
            summary = nil
            health = []
            error = nil
            return
        }
        while !Task.isCancelled {
            do {
                // The dmsg servers ride along in the summary.
                let summary = try await app.client.summary()
                let health = (try? await app.client.serviceHealth()) ?? []
                self.summary = summary
                self.health = health
                error = nil
            } catch is CancellationError {
                return
            } catch {
                if app.handle(error) { return }
                self.error = error.localizedDescription
            }
            try? await Task.sleep(for: Self.refreshInterval)
        }
    }

    /// Drops the visor's dmsg sessions so it re-dials now.
    func reconnectDmsg(_ app: AppModel) {
        Task {
            do {
                let closed = try await app.client.dmsgReconnect()
                notice = L10n.format("home_dmsg_reconnected", closed)
            } catch {
                if !app.handle(error) {
                    notice = error.localizedDescription
                }
            }
        }
    }
}
