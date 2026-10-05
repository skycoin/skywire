import SwiftUI

/// SkyVPN (Android: VpnScreen), off on iOS until the tunnel exists (M7, Lane D).
struct VpnView: View {
    @EnvironmentObject private var navigator: Navigator

    var body: some View {
        VStack(spacing: 0) {
            SkyTopBar(title: Text("app_skyvpn"), onBack: { navigator.back() }, help: .vpn)
            ScrollView {
                VStack(spacing: 16) {
                    SectionCard {
                        HStack(spacing: 8) {
                            StatusDot(color: .skyOnSurfaceVariant)
                            Text("state_disconnected").skyText(.titleMedium)
                        }
                        Button {} label: { Text("connect").frame(maxWidth: .infinity) }
                            .buttonStyle(.filled)
                            .disabled(true)
                            .padding(.top, 12)
                        Text("vpn_ios_pending").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                            .padding(.top, 4)
                    }
                }
                .padding(.horizontal, 20)
                .padding(.vertical, 16)
            }
        }
        .background(Color.skyBackground)
    }
}
