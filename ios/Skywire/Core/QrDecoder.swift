import CoreImage
import Vision

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

    /// Vision first (it copes better with a tilted or blurred code), Core
    /// Image's detector whenever Vision found nothing. Not only when it
    /// throws: where Vision cannot run its model (a Simulator in a VM, as
    /// the CI runners are: "On-device compilation within a VM only supports
    /// CPU") it returns no result without an error, which kept the fallback
    /// from ever running (G3).
    static func decode(_ image: CIImage) -> String {
        let seen = decodeWithVision(image)
        return seen.isEmpty ? decodeWithCoreImage(image) : seen
    }

    static func decodeWithVision(_ image: CIImage) -> String {
        let request = VNDetectBarcodesRequest()
        request.symbologies = [.qr]
        do {
            try VNImageRequestHandler(ciImage: image, options: [:]).perform([request])
        } catch {
            return ""
        }
        return request.results?.lazy.compactMap(\.payloadStringValue).first ?? ""
    }

    /// Core Image's QR detector: no model at all, so it runs everywhere.
    static func decodeWithCoreImage(_ image: CIImage) -> String {
        let detector = CIDetector(ofType: CIDetectorTypeQRCode, context: nil, options: [CIDetectorAccuracy: CIDetectorAccuracyHigh])
        let codes = detector?.features(in: image).compactMap { ($0 as? CIQRCodeFeature)?.messageString }
        return codes?.first ?? ""
    }
}
