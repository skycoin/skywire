import SwiftUI

/// The one-time "update available" banner over the top of the current screen
/// (Android: ui/components/UpdateBanner.kt). See UpdateNotice.
struct UpdateBanner: View {
    @ObservedObject private var notice = UpdateNotice.shared
    @Environment(\.openURL) private var openURL

    var body: some View {
        VStack {
            if let offer = notice.offer {
                card(offer).transition(.move(edge: .top).combined(with: .opacity))
            }
        }
        .frame(maxWidth: .infinity)
        .animation(.easeInOut(duration: 0.25), value: notice.offer)
    }

    private func card(_ offer: UpdateNotice.Offer) -> some View {
        VStack(alignment: .trailing, spacing: 12) {
            HStack(spacing: 12) {
                MaterialIcon(MI.roundedSystemUpdate).foregroundStyle(Color.skyPrimary)
                VStack(alignment: .leading, spacing: 2) {
                    Text("update_notice_title").skyText(.titleSmall).foregroundStyle(Color.skyOnSurface)
                    Text(verbatim: L10n.format("update_notice_body_store", offer.version, offer.source.storeName, offer.installed))
                        .skyText(.bodySmall)
                        .foregroundStyle(Color.skyOnSurfaceVariant)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            HStack(spacing: 12) {
                Button { notice.dismiss(offer) } label: { Text("update_notice_later") }
                    .buttonStyle(.tonal)
                    .accessibilityIdentifier("update-later")
                Button { update(offer) } label: { Text("update_notice_update") }
                    .buttonStyle(.filled)
                    .accessibilityIdentifier("update-now")
            }
        }
        .padding(16)
        .background(Color.skyContainerHigh, in: RoundedRectangle.sky(16))
        .overlay(RoundedRectangle.sky(16).stroke(Color.skyOutlineVariant, lineWidth: 1))
        .skyElevation(8)
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("update-banner")
        // Android marks it a polite live region.
        .onAppear { UIAccessibility.post(notification: .announcement, argument: L10n.text("update_notice_title")) }
    }

    private func update(_ offer: UpdateNotice.Offer) {
        notice.dismiss(offer)
        openURL(offer.url) { opened in
            if !opened, offer.source == .testFlight { openURL(UpdateNotice.testFlightWeb) }
        }
    }
}
