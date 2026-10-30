import CoreClient
import Foundation

/// Drives Fleet (Android: FleetViewModel): the opt-in, and once it is on the
/// visors that have connected in. The opt-in is the whole feature: it pins
/// `hypervisor.dmsg_ingest`, which the visor reads once while it builds, so
/// it restarts the core (Settings' switch does the same, SettingsModel).
///
/// Reads only, with one exception, Restart: everything else the hypervisor
/// API could do to these visors is deliberately not wired.
@MainActor
final class FleetModel: ObservableObject {
    /// Remote visors only: this phone and the serving hypervisor are left out.
    @Published private(set) var visors: [VisorSummary] = []
    /// Public key → the name the user gave that visor, on this phone.
    @Published private(set) var names: [String: String]
    /// Until the first list lands, so an empty one is not shown as "none".
    @Published private(set) var loading = false
    /// The visor whose restart is in flight.
    @Published private(set) var restarting: String?
    @Published private(set) var error: String?
    /// One action's outcome, shown once.
    @Published var message: String?

    /// Deliberately slow. Each poll makes the visor fire a Summary RPC to
    /// every remote over dmsg, and from a phone those take longer than the
    /// server's own 5 s budget; faster polling stacked calls on one dmsg
    /// stream until it broke and the peer was evicted and redialled (measured
    /// on Android's emulator), which left Restart answering 503.
    static let pollInterval: Duration = .seconds(15)
    private static let namesKey = "fleet_visor_names"

    private let defaults: UserDefaults

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        names = (defaults.data(forKey: Self.namesKey)).flatMap { try? JSONDecoder().decode([String: String].self, from: $0) } ?? [:]
    }

    /// Runs while the screen is up and its key holds (`.task(id:)` on the
    /// connection and the opt-in).
    func run(_ app: AppModel) async {
        guard app.connected, app.settings.fleetEnabled else {
            visors = []
            loading = false
            return
        }
        while !Task.isCancelled {
            await load(app)
            do { try await Task.sleep(for: Self.pollInterval) } catch { return }
        }
    }

    func load(_ app: AppModel) async {
        guard app.connected else { return }
        loading = visors.isEmpty
        defer { loading = false }
        do {
            let local = try await app.client.localPK()
            // The list opens with the visor serving it, this phone: not part
            // of anyone's fleet, and it has Home to itself.
            visors = try await app.client.visorsSummary().filter { !$0.isHypervisor && $0.overview.localPK != local }
            error = nil
        } catch is CancellationError {
            return
        } catch {
            if !app.handle(error) {
                self.error = error.localizedDescription
            }
        }
    }

    /// "Sent" is all this can honestly report: a visor cannot answer the
    /// request that tears down the connection carrying it, so the outcome is
    /// the row going offline and coming back. Marked offline now, not at the
    /// next poll, so the tap visibly did something.
    func restart(_ pk: String, label: String, _ app: AppModel) {
        restarting = pk
        Task {
            defer { restarting = nil }
            do {
                try await app.client.restartVisor(pk: pk)
                message = L10n.format("fleet_restart_sent", label)
                visors = visors.map { visor in
                    var visor = visor
                    if visor.overview.localPK == pk { visor.online = false }
                    return visor
                }
            } catch {
                // The server's own words ("currently disconnected (last seen
                // 40s ago), retrying") say more than any wording here could.
                if !app.handle(error) {
                    message = error.localizedDescription
                }
            }
        }
    }

    /// Names a visor, on this phone only; a blank name removes it.
    func rename(_ pk: String, _ name: String) {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        names[pk] = trimmed.isEmpty ? nil : trimmed
        defaults.set(try? JSONEncoder().encode(names), forKey: Self.namesKey)
    }

    func label(_ pk: String) -> String {
        names[pk] ?? Format.shortPK(pk)
    }
}
