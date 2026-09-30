import CoreImage
import CoreImage.CIFilterBuiltins
@testable import Skywire
import UIKit
import WebKit
import XCTest

/// The chat page's QR bridge (QrBridge.swift): the decoder on generated codes,
/// and the bridge itself in a real web view, called the way the page calls it
/// (`await window.SkywireQr.decode(dataUrl)`). The Simulator has no camera, so
/// this is how a code gets decoded there; the page's picker path hands over
/// the same data URL.
@MainActor
final class QrBridgeTests: XCTestCase {
    private let address = "skychat://" + "02" + String(repeating: "7c", count: 32)

    func testDecoderReadsAGeneratedCode() throws {
        XCTAssertEqual(QrDecoder.decode(dataURL: try qrDataURL(address)), address)
    }

    /// Small, as a 640-px camera frame downscaled from a photo would carry it.
    func testDecoderReadsASmallCodeAsJPEG() throws {
        XCTAssertEqual(QrDecoder.decode(dataURL: try qrDataURL(address, scale: 3, jpeg: true)), address)
    }

    /// The fallback on its own: where Vision cannot run (CI's Simulator in a
    /// VM finds nothing, without an error) Core Image alone must read the
    /// code, and `decode` must reach it.
    func testCoreImageReadsTheCodeOnItsOwn() throws {
        let url = try qrDataURL(address)
        let data = try XCTUnwrap(Data(base64Encoded: String(url.split(separator: ",")[1])))
        let image = try XCTUnwrap(CIImage(data: data))
        XCTAssertEqual(QrDecoder.decodeWithCoreImage(image), address)
    }

    /// No code, no image, no payload: "", which the page reads as "nothing in
    /// this frame yet".
    func testDecoderAnswersEmptyWithoutACode() throws {
        XCTAssertEqual(QrDecoder.decode(dataURL: try blankDataURL()), "")
        XCTAssertEqual(QrDecoder.decode(dataURL: "data:image/png;base64,bm90IGFuIGltYWdl"), "")
        XCTAssertEqual(QrDecoder.decode(dataURL: "data:image/png;base64,"), "")
        XCTAssertEqual(QrDecoder.decode(dataURL: ""), "")
    }

    /// Through WebKit: the page's call shape, on the chat's loopback origin.
    /// The object is there from the first line of the page (the page reads
    /// its presence as "inside the Skywire app"), it cannot be replaced, and
    /// its promise carries the code's text.
    func testThePageAwaitsTheBridge() async throws {
        let webView = try await page(origin: "http://127.0.0.1:8001/")
        let shape = try await webView.callAsyncJavaScript(
            "return [typeof window.SkywireQr, typeof window.SkywireQr.decode, Object.isFrozen(window.SkywireQr)].join(' ');",
            contentWorld: .page
        )
        XCTAssertEqual(shape as? String, "object function true")
        let decoded = try await webView.callAsyncJavaScript(
            "return await window.SkywireQr.decode(url);",
            arguments: ["url": try qrDataURL(address)],
            contentWorld: .page
        )
        XCTAssertEqual(decoded as? String, address)
        let empty = try await webView.callAsyncJavaScript(
            "return await window.SkywireQr.decode(url);",
            arguments: ["url": try blankDataURL()],
            contentWorld: .page
        )
        XCTAssertEqual(empty as? String, "")
    }

    /// A page from anywhere but loopback gets nothing decoded.
    func testOtherOriginsAreRefused() async throws {
        let webView = try await page(origin: "http://example.com/")
        let decoded = try await webView.callAsyncJavaScript(
            "return await window.SkywireQr.decode(url);",
            arguments: ["url": try qrDataURL(address)],
            contentWorld: .page
        )
        XCTAssertEqual(decoded as? String, "")
    }

    // MARK: Fixtures

    /// A web view with the bridge installed, showing a small document as if
    /// served from `origin` (nothing is fetched).
    private func page(origin: String) async throws -> WKWebView {
        let configuration = WKWebViewConfiguration()
        QrBridge.install(in: configuration.userContentController)
        let webView = WKWebView(frame: CGRect(x: 0, y: 0, width: 320, height: 480), configuration: configuration)
        let loaded = LoadWaiter()
        webView.navigationDelegate = loaded
        await withCheckedContinuation { continuation in
            loaded.continuation = continuation
            webView.loadHTMLString("<!doctype html><title>t</title><p>t</p>", baseURL: URL(string: origin))
        }
        webView.navigationDelegate = nil
        return webView
    }

    /// `text` as a QR code with a white margin, as a data URL.
    private func qrDataURL(_ text: String, scale: CGFloat = 8, jpeg: Bool = false) throws -> String {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(text.utf8)
        filter.correctionLevel = "M"
        let code = try XCTUnwrap(filter.outputImage).transformed(by: CGAffineTransform(scaleX: scale, y: scale))
        let margin = 4 * scale
        let canvas = code.extent.insetBy(dx: -margin, dy: -margin)
        let image = code.composited(over: CIImage(color: .white).cropped(to: canvas))
        return try dataURL(image, jpeg: jpeg)
    }

    private func blankDataURL() throws -> String {
        try dataURL(CIImage(color: .white).cropped(to: CGRect(x: 0, y: 0, width: 200, height: 200)), jpeg: false)
    }

    private func dataURL(_ image: CIImage, jpeg: Bool) throws -> String {
        let cgImage = try XCTUnwrap(CIContext().createCGImage(image, from: image.extent))
        let picture = UIImage(cgImage: cgImage)
        if jpeg {
            return "data:image/jpeg;base64," + (try XCTUnwrap(picture.jpegData(compressionQuality: 0.8))).base64EncodedString()
        }
        return "data:image/png;base64," + (try XCTUnwrap(picture.pngData())).base64EncodedString()
    }
}

/// Resumes once the web view finished its load.
@MainActor
private final class LoadWaiter: NSObject, WKNavigationDelegate {
    var continuation: CheckedContinuation<Void, Never>?

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        continuation?.resume()
        continuation = nil
    }
}
