import Foundation

/// What something outside the chat page asks it to show: a `skychat:` link
/// another app (or the page itself) opened us with, or the conversation a
/// tapped notification came from (Android: core/DeepLinks.kt, plus the
/// notification's open intent).
///
/// App-wide rather than held by a screen, because the two ends never line up:
/// the request arrives at the app, and the page that acts on it may not exist
/// yet (a cold start), may be waiting for the core, or may still be loading.
/// Parking it here lets each side move at its own pace: offered once, pending
/// until the page has acted on it. The Chat tab is selected when one arrives.
@MainActor
final class ChatRouter: ObservableObject {
    static let shared = ChatRouter()

    enum Target: Equatable {
        /// A skychat address as written (`skychat://<pk>`,
        /// `skychat://<pk>/<group>`, `skychat:invite:<…>`), for the page's
        /// Add-by-address dialog. The page shows it and stops there: it never
        /// adds, opens or joins on a link's say-so.
        case address(String)
        /// A conversation: a peer's key or a group's ID, the tag skychat puts
        /// on its notifications. Empty: the chat, whichever screen it is on.
        case conversation(String)
    }

    /// A target with the id that makes two identical requests two events:
    /// opening the same link twice must do something twice.
    struct Request: Equatable {
        let target: Target
        let id: Int
    }

    /// The request nothing has acted on yet, if any.
    @Published private(set) var pending: Request?

    private var sequence = 0

    /// Only `skychat:` is claimed. `skycoin:` in particular is not ours: the
    /// Skycoin wallet answers it on the same phone.
    static let scheme = "skychat"

    /// True when `url` is a link this app claims (and it is now pending).
    @discardableResult
    func offer(_ url: URL) -> Bool {
        guard url.scheme?.lowercased() == Self.scheme else { return false }
        // The original text, not a rebuilt URL: an invite is the opaque form
        // `skychat:invite:<base64url>`, and the page's resolver takes every
        // form as written.
        let address = url.absoluteString.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !address.isEmpty else { return false }
        request(.address(address))
        return true
    }

    /// A tapped skychat notification: its tag names the conversation (a join
    /// request's tag is `join:<group>`, which opens that group; no tag opens
    /// the chat as it was).
    func openConversation(tag: String) {
        let key = tag.hasPrefix("join:") ? String(tag.dropFirst("join:".count)) : tag
        request(.conversation(key))
    }

    /// Drops `request` once the page acted on it (or declined it: a request
    /// the page refused would only be refused again). A newer one that
    /// arrived meanwhile stays.
    func handled(_ request: Request) {
        if pending == request {
            pending = nil
        }
    }

    private func request(_ target: Target) {
        sequence += 1
        pending = Request(target: target, id: sequence)
    }
}
