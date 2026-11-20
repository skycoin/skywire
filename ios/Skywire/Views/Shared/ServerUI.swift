import CoreClient
import SwiftUI
import UIKit

// The server lists and their cards (Android: components/ServerUi.kt, FavoriteServers.kt,
// TransportPreferenceUi.kt), shared by SkySOCKS and SkyVPN.

/// One server: flag, "GB · London", key, version and the star. A Card: no border.
struct ServerRow: View {
    let entry: ServiceEntry
    let selected: Bool
    let unlisted: Bool
    let favorite: Bool
    let onTap: () -> Void
    let onStar: () -> Void

    var body: some View {
        HStack(spacing: 0) {
            if let flag = entry.geo.flatMap({ Format.flag($0.country) }) {
                Text(verbatim: flag).skyText(.titleLarge).padding(.trailing, 12)
            }
            VStack(alignment: .leading, spacing: 0) {
                Text(verbatim: location).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurface)
                Text(verbatim: Format.shortPK(entry.pk)).skyText(.bodySmall, mono: true).foregroundStyle(Color.skyOnSurfaceVariant)
                if unlisted {
                    Text("favorite_unlisted").skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            if !entry.version.isEmpty {
                Text(verbatim: entry.version).skyText(.labelSmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.leading, 12)
            }
            FavoriteStar(favorite: favorite, toggle: onStar)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .background(selected ? Color.skySecondaryContainer : Color.skySurfaceVariant, in: .sky(SkyRadius.medium))
        .contentShape(RoundedRectangle(cornerRadius: SkyRadius.medium))
        .onTapGesture(perform: onTap)
    }

    /// Android's words: the country code and the region, or "Unknown location".
    private var location: String {
        let parts = [entry.geo?.country.uppercased() ?? "", entry.geo?.region ?? ""].filter { !$0.isEmpty }
        return parts.isEmpty ? L10n.text("server_location_unknown") : parts.joined(separator: " · ")
    }
}

/// The star that keeps a server at the top of its list.
struct FavoriteStar: View {
    let favorite: Bool
    let toggle: () -> Void

    var body: some View {
        Button(action: toggle) {
            MaterialIcon(favorite ? MI.filledStar : MI.outlinedStarBorder)
                .foregroundStyle(favorite ? Color.skyWarning : Color.skyOnSurfaceVariant)
                .frame(width: 48, height: 48)
                .contentShape(Rectangle())
        }
        .buttonStyle(PressStyle())
        .accessibilityLabel(Text(favorite ? L10n.key("favorite_remove") : L10n.key("favorite_add")))
    }
}

/// "Favorites (2)".
struct FavoritesHeader: View {
    let count: Int

    var body: some View {
        Text(verbatim: L10n.format("servers_favorites", count)).skyText(.titleMedium).foregroundStyle(Color.skyOnBackground)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// The primary transport and its Change button.
struct TransportPreferenceCard: View {
    let current: String
    let enabled: Bool
    let change: () -> Void

    var body: some View {
        SectionCard {
            Text("transport_title").skyText(.labelMedium).foregroundStyle(Color.skyOnSurfaceVariant)
            HStack {
                Text(TransportPreferenceSheet.name(current)).skyText(.titleMedium).frame(maxWidth: .infinity, alignment: .leading)
                Button(action: change) { Text("transport_change") }
                    .buttonStyle(.tonal)
                    .disabled(!enabled)
                    .accessibilityIdentifier("transport-link")
            }
            .padding(.top, 6)
            Text("transport_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 4)
        }
    }
}

/// The three transports, the chosen one ticked; a tap applies it and closes the sheet.
struct TransportPreferenceSheet: View {
    let current: String
    let choose: (String) -> Void
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("transport_sheet_title").skyText(.titleMedium)
            Text("transport_sheet_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
            VStack(spacing: 0) {
                ForEach(TransportPreference.choices, id: \.self) { type in
                    Button {
                        dialogs.dismissSheet()
                        choose(type)
                    } label: {
                        HStack(spacing: 8) {
                            SkyRadio(selected: type == current).frame(width: 20, height: 20)
                            VStack(alignment: .leading, spacing: 0) {
                                HStack(spacing: 8) {
                                    Text(Self.name(type)).skyText(.bodyLarge)
                                    if type == TransportPreference.defaultPrimary {
                                        Text("transport_recommended").skyText(.labelSmall)
                                            .foregroundStyle(Color.skyOnSecondaryContainer)
                                            .padding(.horizontal, 8).padding(.vertical, 2)
                                            .background(Color.skySecondaryContainer, in: Capsule())
                                    }
                                }
                                Text(Self.hint(type)).skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                        }
                        .padding(.vertical, 10)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(PressStyle(layer: .skyOnSurface))
                    .accessibilityAddTraits(type == current ? .isSelected : [])
                }
            }
            .padding(.top, 12)
        }
        .padding(.horizontal, 24)
        .padding(.bottom, 24)
    }

    static func name(_ type: String) -> LocalizedStringKey {
        switch type {
        case TransportPreference.stcpr: L10n.key("transport_stcpr")
        case TransportPreference.sudph: L10n.key("transport_sudph")
        default: L10n.key("transport_dmsg")
        }
    }

    static func hint(_ type: String) -> LocalizedStringKey {
        switch type {
        case TransportPreference.stcpr: L10n.key("transport_stcpr_hint")
        case TransportPreference.sudph: L10n.key("transport_sudph_hint")
        default: L10n.key("transport_dmsg_hint")
        }
    }
}
