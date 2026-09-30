import CoreBridge

/// Where the core runs. On the Simulator it runs in the app process
/// (`InAppCoreHost`); on a device it will run in the packet-tunnel extension
/// (`TunnelCoreHost`, milestone M7), the only process iOS keeps alive in the
/// background. The rest of the app sees only this protocol and the visor's
/// local API, so it does not care which.
protocol CoreHost: Sendable {
    /// Starts the core (generating its config on first run) and returns once
    /// the visor's local API is up.
    func start() async throws

    /// Stops the core and returns once its listeners are closed.
    func stop() async throws

    /// Stops the core and starts it again, re-reading the config.
    func restart() async throws

    /// Deletes the local API's account store, with the core stopped, so the
    /// next start's login creates the account again with the Keychain's
    /// password: the way out when that password no longer opens users.db.
    func resetAccount() async throws

    /// The core's state: the current value first, then each change.
    var state: AsyncStream<CoreState> { get }
}
