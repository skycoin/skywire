import UIKit

/// The app is drawn for portrait. A video the chat plays full screen turns with the phone, as
/// Android's video viewer does: only while the viewer is up does the app allow landscape.
@MainActor
enum Orientation {
    private(set) static var videoFullScreen = false

    static var allowed: UIInterfaceOrientationMask { videoFullScreen ? .allButUpsideDown : .portrait }

    /// The chat's video went full screen or came back; leaving puts the app upright again.
    static func setVideoFullScreen(_ on: Bool) {
        guard on != videoFullScreen else { return }
        videoFullScreen = on
        for scene in UIApplication.shared.connectedScenes.compactMap({ $0 as? UIWindowScene }) {
            for window in scene.windows {
                window.rootViewController?.setNeedsUpdateOfSupportedInterfaceOrientations()
            }
            if !on {
                scene.requestGeometryUpdate(.iOS(interfaceOrientations: .portrait))
            }
        }
    }
}

/// Answers iOS's question of which orientations the app takes, which the Info.plist's portrait
/// answered on its own before full-screen video.
final class AppDelegate: NSObject, UIApplicationDelegate {
    func application(_ application: UIApplication, supportedInterfaceOrientationsFor window: UIWindow?) -> UIInterfaceOrientationMask {
        Orientation.allowed
    }
}
