import UIKit
import WebKit

/// Answers a page's JavaScript dialogs, alert, confirm and prompt, with the
/// system's alert (Android: JsDialogChromeClient). Any WKUIDelegate the app
/// has calls these: the chat page now, the DEX page in M4.
///
/// Not a nicety. A WKUIDelegate that leaves `confirm()` out answers false at
/// once, so every action the page guards behind one silently never happens:
/// skychat guards seven that way (delete a conversation, leave or delete a
/// group, remove a contact, wipe the saved notes, stop pairing, stop
/// publishing a profile). And a completion handler WebKit hands out has to be
/// called exactly once: the page's script waits on it, and WebKit raises an
/// exception for one released uncalled. So each call here answers exactly
/// once, including when no screen can show the alert (answered as dismissed:
/// OK for an alert, Cancel for the others).
///
/// Unlike Android there is no `beforeunload` hook: WebKit on iOS never asks
/// the delegate about one, as Safari on iOS never shows one.
@MainActor
enum WebDialogs {
    static func alert(_ message: String, in webView: WKWebView, completion: @escaping @MainActor () -> Void) {
        let alert = UIAlertController(title: nil, message: message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: L10n.text("ok"), style: .default) { _ in completion() })
        if !present(alert, over: webView) {
            completion()
        }
    }

    static func confirm(_ message: String, in webView: WKWebView, completion: @escaping @MainActor (Bool) -> Void) {
        let alert = UIAlertController(title: nil, message: message, preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: L10n.text("cancel"), style: .cancel) { _ in completion(false) })
        alert.addAction(UIAlertAction(title: L10n.text("ok"), style: .default) { _ in completion(true) })
        if !present(alert, over: webView) {
            completion(false)
        }
    }

    static func prompt(
        _ message: String,
        defaultText: String?,
        in webView: WKWebView,
        completion: @escaping @MainActor (String?) -> Void
    ) {
        let alert = UIAlertController(title: nil, message: message, preferredStyle: .alert)
        alert.addTextField { $0.text = defaultText }
        alert.addAction(UIAlertAction(title: L10n.text("cancel"), style: .cancel) { _ in completion(nil) })
        alert.addAction(UIAlertAction(title: L10n.text("ok"), style: .default) { [weak alert] _ in
            completion(alert?.textFields?.first?.text ?? "")
        })
        if !present(alert, over: webView) {
            completion(nil)
        }
    }

    /// Presents `controller` over whatever is on top of `view`'s window.
    /// False when it could not be shown: the view is not in a window, or
    /// UIKit refused (a presentation already under way). An alert's buttons
    /// are its only way out, so once shown it always answers.
    @discardableResult
    static func present(_ controller: UIViewController, over view: UIView) -> Bool {
        guard var top = view.window?.rootViewController else { return false }
        while let next = top.presentedViewController, !next.isBeingDismissed {
            top = next
        }
        top.present(controller, animated: true)
        // UIKit accepts or refuses synchronously: an accepted controller has
        // its presenter set before present(_:animated:) returns.
        return controller.presentingViewController != nil
    }
}
