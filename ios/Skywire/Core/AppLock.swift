import Foundation
import LocalAuthentication
import SwiftUI
import UIKit

/// Whether the app is behind its lock (Android: core/AppLock.kt). The lock
/// asks for Face ID, Touch ID or the passcode when the app opens and when it
/// comes back after more than `grace` away; the preference is
/// AppSettings.appLockEnabled, and while it is off nothing here is asked.
///
/// It starts locked: a fresh process is exactly the case that must ask (the
/// phone restarted, the app was killed), and starting unlocked would make
/// every process death a free pass.
@MainActor
final class AppLock: ObservableObject {
    /// How long the app may be away before it locks again: long enough to
    /// copy a code out of another app and come back, far short of someone
    /// else picking the phone up.
    static let grace: TimeInterval = 30

    @Published private(set) var locked = true
    /// The screen is being recorded or mirrored (UIScreen.isCaptured).
    @Published private(set) var captured = false
    /// An unlock prompt is on screen.
    @Published private(set) var prompting = false

    /// When the app last went to the background; nil until it has, which is
    /// what makes a cold start lock.
    private var leftAt: Date?
    /// The prompt has been put up by itself since the app came forward. Once
    /// only: the prompt itself makes the app inactive and active again, and a
    /// cancelled prompt must leave the Unlock button, not come straight back.
    private var autoPrompted = false
    private var captureObserver: NSObjectProtocol?

    init() {
        captured = UIScreen.main.isCaptured
        captureObserver = NotificationCenter.default.addObserver(
            forName: UIScreen.capturedDidChangeNotification, object: nil, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.captured = UIScreen.main.isCaptured }
        }
    }

    /// Whether the device has anything to check against (a passcode at least).
    static var available: Bool {
        LAContext().canEvaluatePolicy(.deviceOwnerAuthentication, error: nil)
    }

    func didEnterBackground() {
        leftAt = Date()
        autoPrompted = false
    }

    /// Locks again unless the app was away for less than `grace`.
    func willEnterForeground() {
        if let leftAt, Date().timeIntervalSince(leftAt) <= Self.grace { return }
        locked = true
    }

    /// The first time the app is frontmost and locked, prompt without a tap.
    func promptOnce() async {
        guard locked, !autoPrompted else { return }
        autoPrompted = true
        await unlock()
    }

    /// Asks for Face ID, Touch ID or the passcode; unlocks when it passes.
    func unlock() async {
        guard !prompting else { return }
        prompting = true
        defer { prompting = false }
        if await Self.authenticate(reason: L10n.text("lock_prompt_title")) {
            locked = false
        }
    }

    /// A check before turning the lock on or off, so it can be neither set up
    /// nor removed by someone who cannot pass it. Passing it also unlocks:
    /// having just proved themselves, the user is not asked twice in a row.
    func confirm(reason: String) async -> Bool {
        let passed = await Self.authenticate(reason: reason)
        if passed { locked = false }
        return passed
    }

    private static func authenticate(reason: String) async -> Bool {
        let context = LAContext()
        guard context.canEvaluatePolicy(.deviceOwnerAuthentication, error: nil) else { return false }
        return (try? await context.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: reason)) ?? false
    }
}

/// What covers the app: the lock screen while it is locked, a plain cover
/// while it is not frontmost (so the app switcher's snapshot shows nothing)
/// or while the screen is being recorded or mirrored. Only with the lock on,
/// as on Android, where the same setting turns on FLAG_SECURE.
struct LockCover: ViewModifier {
    @ObservedObject var lock: AppLock
    let enabled: Bool
    @Environment(\.scenePhase) private var scenePhase

    func body(content: Content) -> some View {
        content.overlay {
            if enabled && lock.locked {
                LockScreen(lock: lock)
            } else if enabled && (scenePhase != .active || lock.captured) {
                PrivacyCover()
            }
        }
    }
}

private struct LockScreen: View {
    @ObservedObject var lock: AppLock
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        // Android's lock overlay (BiometricGate.kt): the logo, the title, Unlock.
        ZStack {
            Color.skyBackground.ignoresSafeArea()
            VStack(spacing: 0) {
                Image("skywire-logo").resizable().scaledToFit().frame(width: 96, height: 96)
                Text("lock_title").skyText(.titleMedium).foregroundStyle(Color.skyOnBackground).padding(.top, 28)
                Button {
                    Task { await lock.unlock() }
                } label: {
                    Text("lock_unlock")
                }
                .buttonStyle(.filled)
                .padding(.top, 24)
                .accessibilityIdentifier("unlock-button")
            }
            .padding(32)
        }
        .task(id: scenePhase) {
            // Only once the app is frontmost: a prompt raised any earlier fails.
            if scenePhase == .active { await lock.promptOnce() }
        }
    }
}

private struct PrivacyCover: View {
    var body: some View {
        ZStack {
            Rectangle().fill(.ultraThickMaterial).ignoresSafeArea()
            Image(systemName: "lock.shield").font(.system(size: 44)).foregroundStyle(.secondary)
        }
    }
}
