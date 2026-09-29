import SwiftUI

@main
struct SkywireApp: App {
    @StateObject private var spike = SpikeModel.inApp()

    var body: some Scene {
        WindowGroup {
            SpikeView(model: spike)
        }
    }
}
