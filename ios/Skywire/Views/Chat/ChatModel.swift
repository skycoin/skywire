import CoreClient
import Foundation

/// Brings the chat surface up and hands the screen an address to load
/// (Android: ChatViewModel). Once the core is connected: start skychat if it
/// is not up, then poll its own listener until it answers. The page is shown
/// only after that; a web view pointed at a port nothing listens on shows an
/// error page and needs a manual reload.
@MainActor
final class ChatModel: ObservableObject {
    /// Set once the surface answered; the web view loads exactly this.
    @Published private(set) var url: URL?
    /// The secret the page's password gate is answered with (SkychatProfile).
    @Published private(set) var password: String?
    /// Starting skychat or waiting for its listener.
    @Published private(set) var starting = false
    @Published private(set) var error: String?
    /// Bumped by Retry, so the screen's task runs the bring-up again.
    @Published private(set) var attempt = 0

    /// ~15 s at 500 ms: an in-process app binds its listener in well under a
    /// second.
    static let readyAttempts = 30
    static let readyInterval: Duration = .milliseconds(500)

    /// What the screen's task is keyed on: a new connection or a Retry runs
    /// the bring-up again, and a lost connection ends it.
    struct RunKey: Equatable {
        let connected: Bool
        let attempt: Int
    }

    /// Runs for as long as its key holds (SwiftUI's `.task(id:)`). The address
    /// is dropped with the connection: a core restart restarts skychat too,
    /// and the page must be loaded again rather than left talking to a
    /// listener that went away.
    func run(_ app: AppModel) async {
        url = nil
        starting = false
        guard app.connected else {
            error = nil
            return
        }
        await bringUp(app)
    }

    func retry() {
        error = nil
        attempt += 1
    }

    /// Start skychat if it is not up, then wait for its listener. The start's
    /// error is held rather than shown: a start of an app the visor already
    /// counts as started fails, and the polled state can be a beat behind, so
    /// the probe is the real answer and the error is shown only if nothing
    /// ever answers.
    private func bringUp(_ app: AppModel) async {
        starting = true
        error = nil
        defer { starting = false }

        let secret: String
        do {
            secret = try SecretStore.app().password(.skychatPassword)
        } catch {
            self.error = error.localizedDescription
            return
        }
        password = secret

        var startError: String?
        let state: AppState
        do {
            let current = try await app.client.app(SkychatProfile.app)
            if current.status == AppState.statusRunning || current.status == AppState.statusStarting {
                state = current
            } else {
                do {
                    state = try await app.client.updateApp(SkychatProfile.app, status: CoreClient.appStart)
                } catch is CancellationError {
                    return
                } catch {
                    startError = error.localizedDescription
                    state = current
                }
            }
        } catch is CancellationError {
            return
        } catch {
            if !app.handle(error) {
                self.error = error.localizedDescription
            }
            return
        }

        let port = SkychatProfile.listenPort(state.args)
        let skychat = SkychatClient(transport: LoopbackTransport(origin: SkychatProfile.origin(port: port))) { secret }
        var reloadedGate = false
        for _ in 0..<Self.readyAttempts {
            switch await skychat.probe() {
            case 200:
                url = SkychatProfile.baseURL(port: port)
                return
            case 401 where !reloadedGate:
                // The running skychat holds an older password file (the
                // Keychain's secret changed under it). Write the file for the
                // current secret (a no-op when it already matches) and restart
                // the app to reload it; once, since a second refusal is a real
                // failure.
                reloadedGate = true
                do {
                    try PasswordFile.ensure(at: app.paths.skychatPasswordFile, password: secret)
                    _ = try await app.client.updateApp(SkychatProfile.app, status: CoreClient.appStop)
                    _ = try await app.client.updateApp(SkychatProfile.app, status: CoreClient.appStart)
                } catch is CancellationError {
                    return
                } catch {
                    startError = error.localizedDescription
                }
            default:
                break
            }
            do {
                try await Task.sleep(for: Self.readyInterval)
            } catch {
                return
            }
        }
        error = startError ?? L10n.format("chat_error_no_answer", SkychatProfile.baseURL(port: port).absoluteString)
    }
}
