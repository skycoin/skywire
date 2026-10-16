import UIKit
import WebKit

/// Attachments the chat page hands out (a `download` link, `/files/…`,
/// `/export/…`). WKWebView downloads each into a folder of its own under the
/// temporary directory, then the share sheet offers what the phone can do
/// with it: save a picture or video to Photos, keep the file in Files, send it
/// on. The folder goes when the sheet does. (Android hands the URL to
/// DownloadManager, which saves into Downloads; iOS has no shared Downloads
/// folder, and the share sheet is how an app hands a file to the rest of the
/// phone.)
@MainActor
final class ChatDownloads: NSObject {
    /// The gate's credential: a download is its own request and can be
    /// challenged like the page.
    var credential: () -> URLCredential? = { nil }
    /// The web view, over which the sheet or an error is shown.
    weak var presenter: UIView?

    private var destinations: [ObjectIdentifier: URL] = [:]

    func adopt(_ download: WKDownload) {
        download.delegate = self
    }

    /// The file name WebKit suggests, reduced to a single path component.
    static func fileName(_ suggested: String) -> String {
        let name = suggested
            .components(separatedBy: CharacterSet(charactersIn: "/\\:"))
            .joined(separator: "_")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return name.isEmpty || name == "." || name == ".." ? "download" : name
    }

    private func share(_ file: URL) {
        let folder = file.deletingLastPathComponent()
        guard let presenter else {
            try? FileManager.default.removeItem(at: folder)
            return
        }
        let sheet = UIActivityViewController(activityItems: [file], applicationActivities: nil)
        sheet.completionWithItemsHandler = { _, _, _, _ in
            try? FileManager.default.removeItem(at: folder)
        }
        // A popover on an iPad needs an anchor; the page's centre will do.
        sheet.popoverPresentationController?.sourceView = presenter
        sheet.popoverPresentationController?.sourceRect = CGRect(x: presenter.bounds.midX, y: presenter.bounds.midY, width: 0, height: 0)
        if !WebDialogs.present(sheet, over: presenter) {
            try? FileManager.default.removeItem(at: folder)
        }
    }

    private func failed(_ name: String, _ error: any Error) {
        ChatPage.log.error("download of \(name, privacy: .public) failed: \(error.localizedDescription, privacy: .public)")
        guard let presenter else { return }
        let alert = UIAlertController(
            title: nil,
            message: L10n.format("chat_download_failed", name, error.localizedDescription),
            preferredStyle: .alert
        )
        alert.addAction(UIAlertAction(title: L10n.text("ok"), style: .default))
        WebDialogs.present(alert, over: presenter)
    }
}

extension ChatDownloads: WKDownloadDelegate {
    func download(
        _ download: WKDownload,
        decideDestinationUsing response: URLResponse,
        suggestedFilename: String,
        completionHandler: @escaping @MainActor (URL?) -> Void
    ) {
        let folder = FileManager.default.temporaryDirectory
            .appendingPathComponent("chat-downloads", isDirectory: true)
            .appendingPathComponent(UUID().uuidString, isDirectory: true)
        do {
            try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        } catch {
            failed(suggestedFilename, error)
            completionHandler(nil)
            return
        }
        let destination = folder.appendingPathComponent(Self.fileName(suggestedFilename))
        destinations[ObjectIdentifier(download)] = destination
        completionHandler(destination)
    }

    func download(
        _ download: WKDownload,
        didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping @MainActor (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
    ) {
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodHTTPBasic,
              challenge.previousFailureCount == 0, let credential = credential()
        else {
            completionHandler(.performDefaultHandling, nil)
            return
        }
        completionHandler(.useCredential, credential)
    }

    func downloadDidFinish(_ download: WKDownload) {
        guard let file = destinations.removeValue(forKey: ObjectIdentifier(download)) else { return }
        share(file)
    }

    func download(_ download: WKDownload, didFailWithError error: any Error, resumeData: Data?) {
        let file = destinations.removeValue(forKey: ObjectIdentifier(download))
        if let file {
            try? FileManager.default.removeItem(at: file.deletingLastPathComponent())
        }
        failed(file?.lastPathComponent ?? download.originalRequest?.url?.lastPathComponent ?? "", error)
    }
}
