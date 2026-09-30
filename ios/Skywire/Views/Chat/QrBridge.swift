import CoreImage
import CoreClient
import Vision
import WebKit

/// QR decoding for the chat page, done by the app (Android: ui/chat/QrBridge.kt).
///
/// The page decodes with `BarcodeDetector` where the browser has one, and
/// WebKit does not. So, as on Android, the page keeps the camera, the image
/// picker and the dialog, and only the decode crosses over: one method,
/// `window.SkywireQr.decode(dataUrl)`, an image in, the code's text out ("" for
/// none). The difference is that a WKWebView bridge cannot answer
/// synchronously the way Android's JavascriptInterface does, so here `decode`
/// returns a promise; the page awaits it, which works for Android's plain
/// string too.
///
/// `window.SkywireQr` is also how the page knows it is inside the Skywire app
/// (one window, so visible means focused; the app owns call sounds), so it is
/// installed on every chat page, camera or not.
///
/// The Simulator has no camera: there the page's image picker (a photo with a
/// code in it) and its paste field are the ways in, and the test hands the
/// bridge a generated code directly.
final class QrBridge: NSObject, WKScriptMessageHandlerWithReply {
    static let handlerName = "skywireQr"

    /// Adds the bridge to a chat page's content controller.
    @MainActor
    static func install(in content: WKUserContentController) {
        content.addUserScript(WKUserScript(source: script, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        content.addScriptMessageHandler(QrBridge(), contentWorld: .page, name: handlerName)
    }

    /// The page's side: the name and shape the page looks for (a `decode`
    /// function), frozen so a script cannot swap it.
    private static let script = """
    (function () {
      var handler = window.webkit.messageHandlers.\(handlerName);
      window.SkywireQr = Object.freeze({
        decode: function (dataUrl) { return handler.postMessage(String(dataUrl || '')); }
      });
    })();
    """

    func userContentController(
        _ userContentController: WKUserContentController,
        didReceive message: WKScriptMessage,
        replyHandler: @escaping @MainActor (Any?, String?) -> Void
    ) {
        // Only the chat page on loopback may ask; the web view loads nothing
        // else, and the rule holds anyway.
        guard message.frameInfo.securityOrigin.host == AppArgs.loopbackHost,
              let dataURL = message.body as? String
        else {
            replyHandler("", nil)
            return
        }
        // Off the main thread: the camera path asks several times a second,
        // and a decode is tens of milliseconds.
        let reply = Reply(send: replyHandler)
        Task.detached(priority: .userInitiated) {
            let text = QrDecoder.decode(dataURL: dataURL)
            await MainActor.run { reply.send(text, nil) }
        }
    }

    /// WebKit's reply handler is main-actor-only and called there; this only
    /// carries it across the decode.
    private struct Reply: @unchecked Sendable {
        let send: @MainActor (Any?, String?) -> Void
    }
}

/// The text of the first QR code in an image.
enum QrDecoder {
    /// Decodes a `data:image/…;base64,…` URL, the form both of the page's
    /// callers have (a camera frame painted onto a canvas, a picked image).
    /// "" when there is no code, or no image: the page treats both as a frame
    /// with nothing in it yet.
    static func decode(dataURL: String) -> String {
        guard let marker = dataURL.range(of: "base64,"),
              let data = Data(base64Encoded: String(dataURL[marker.upperBound...]), options: .ignoreUnknownCharacters),
              let image = CIImage(data: data)
        else { return "" }
        return decode(image)
    }

    static func decode(_ image: CIImage) -> String {
        let request = VNDetectBarcodesRequest()
        request.symbologies = [.qr]
        do {
            try VNImageRequestHandler(ciImage: image, options: [:]).perform([request])
            return request.results?.lazy.compactMap(\.payloadStringValue).first ?? ""
        } catch {
            // Vision can be unavailable (a Simulator without its models): Core
            // Image's detector reads QR codes with no model at all.
            let detector = CIDetector(ofType: CIDetectorTypeQRCode, context: nil, options: [CIDetectorAccuracy: CIDetectorAccuracyHigh])
            let codes = detector?.features(in: image).compactMap { ($0 as? CIQRCodeFeature)?.messageString }
            return codes?.first ?? ""
        }
    }
}
