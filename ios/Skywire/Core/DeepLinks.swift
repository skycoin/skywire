import CoreClient
import Foundation

/// What something outside the chat asks it to show: a `skychat:` link
/// another app opened us with, or the conversation a
/// tapped notification came from (Android: core/DeepLinks.kt, plus the
/// notification's open intent).
///
/// App-wide rather than held by a screen, because the two ends never line up:
/// the request arrives at the app, and the chat that acts on it may not be up
/// yet (a cold start, or waiting for the core and skychat). Parking it here
/// lets each side move at its own pace: offered once, pending until the chat
/// has acted on it. The Chat tab is selected when one arrives.
@MainActor
final class ChatRouter: ObservableObject {
    static let shared = ChatRouter()

    enum Target: Equatable {
        /// A skychat address as written (`skychat://<pk>`,
        /// `skychat://<pk>/<group>`, `skychat:invite:<…>`), for Add by
        /// address. It shows it and stops there: it never adds, opens or
        /// joins on a link's say-so.
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
    nonisolated static let scheme = "skychat"

    /// True when `url` is a link this app claims (and it is now pending).
    @discardableResult
    func offer(_ url: URL) -> Bool {
        guard url.scheme?.lowercased() == Self.scheme else { return false }
        // The original text, not a rebuilt URL: an invite is the opaque form
        // `skychat:invite:<base64url>`, and skychat's resolver takes every
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

    /// Drops `request` once the chat acted on it. A newer one that arrived
    /// meanwhile stays.
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

/// The inbox message a tapped Skymail notification asked to show, or a draft
/// to start (Android: DeepLinks.pendingMail). The visor tags a mail
/// notification with the message's id; the Skymail screen opens it once its
/// inbox lists it. A `skymail:` link, from a contact's SkyMail row in the
/// Contacts app (PhoneContacts), starts a draft to its address.
@MainActor
final class MailRouter: ObservableObject {
    static let shared = MailRouter()

    nonisolated static let scheme = "skymail"

    /// A message id, or with `to` set a draft, with the sequence that makes two taps on one two events.
    struct Link: Equatable {
        let id: String
        let sequence: Int
        var to = ""
    }

    @Published private(set) var pending: Link?

    private var sequence = 0

    func open(id: String) {
        let id = id.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !id.isEmpty else { return }
        sequence += 1
        pending = Link(id: id, sequence: sequence)
    }

    /// True when `url` is a `skymail:` link to a Skymail address, now pending as a draft.
    @discardableResult
    func offer(_ url: URL) -> Bool {
        guard url.scheme?.lowercased() == Self.scheme else { return false }
        let address = String(url.absoluteString.dropFirst(Self.scheme.count + 1))
            .trimmingCharacters(in: CharacterSet(charactersIn: "/").union(.whitespacesAndNewlines))
        guard let to = SkymailAddress.recipient(address.removingPercentEncoding ?? address) else { return false }
        sequence += 1
        pending = Link(id: "", sequence: sequence, to: to)
        return true
    }

    /// Drops `link` once the screen opened it or knows it never will; a newer one stays.
    func handled(_ link: Link) {
        if pending == link {
            pending = nil
        }
    }
}
