import SwiftUI

// The app's chrome from Android: SkyTopBar, the help dialog, the floating bottom bar.

/// What a screen's ? button explains (Android: HelpTopic).
enum HelpTopic {
    case chat, dex, hub, settings, socks, vpn, wallet, fleet

    var title: LocalizedStringKey {
        switch self {
        case .chat: L10n.key("help_chat_title")
        case .dex: L10n.key("help_dex_title")
        case .hub: L10n.key("help_hub_title")
        case .settings: L10n.key("help_settings_title")
        case .socks: L10n.key("help_socks_title")
        case .vpn: L10n.key("help_vpn_title")
        case .wallet: L10n.key("help_wallet_title")
        case .fleet: L10n.key("help_fleet_title")
        }
    }

    var body: LocalizedStringKey {
        switch self {
        case .chat: L10n.key("help_chat_body")
        case .dex: L10n.key("help_dex_body")
        case .hub: L10n.key("help_hub_body")
        case .settings: L10n.key("help_settings_body")
        case .socks: L10n.key("help_socks_body")
        case .vpn: L10n.key("help_vpn_body")
        case .wallet: L10n.key("help_wallet_body")
        case .fleet: L10n.key("help_fleet_body")
        }
    }
}

/// SkyTopBar (ui/components/SkyTopBar.kt): back, a left-aligned title, actions, help.
struct SkyTopBar<Actions: View>: View {
    let title: Text
    var subtitle: Text? = nil
    var onBack: (() -> Void)? = nil
    var help: HelpTopic? = nil
    @ViewBuilder var actions: Actions
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        HStack(spacing: 12) {
            if let onBack {
                SquareBarButton(icon: MI.roundedArrowBack, action: onBack)
                    .accessibilityLabel(Text("back"))
            }
            VStack(alignment: .leading, spacing: 0) {
                title.skyText(.titleLarge).foregroundStyle(Color.skyOnBackground).lineLimit(1)
                if let subtitle {
                    subtitle.skyText(.labelMedium).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(1)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            actions
            if let help {
                SquareBarButton(icon: MI.roundedHelpOutline) { dialogs.showHelp(help) }
                    .accessibilityLabel(Text("help_open"))
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
        .background(Color.skyBackground)
    }
}

extension SkyTopBar where Actions == EmptyView {
    init(title: Text, subtitle: Text? = nil, onBack: (() -> Void)? = nil, help: HelpTopic? = nil) {
        self.init(title: title, subtitle: subtitle, onBack: onBack, help: help) { EmptyView() }
    }
}

/// The bar's 42 pt rounded-square tonal button.
struct SquareBarButton: View {
    let icon: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            MaterialIcon(icon)
                .foregroundStyle(Color.skyOnSecondaryContainer)
                .frame(width: 42, height: 42)
                .background(Color.skySecondaryContainer, in: .sky(SkyRadius.medium))
        }
        .buttonStyle(PressStyle(layer: .skyOnSecondaryContainer, shape: AnyShape(RoundedRectangle(cornerRadius: SkyRadius.medium))))
    }
}

// MARK: Dialogs

/// An AlertDialog's content (Material 3 defaults, Skywire theme).
struct SkyDialog: Identifiable {
    struct Action {
        let label: Text
        var destructive = false
        var handler: () -> Void = {}
    }

    let id = UUID()
    var title: Text = Text(verbatim: "")
    var message: Text? = nil
    var scrollable = false
    var actions: [Action] = []
    /// Content that draws its own title and buttons (a dialog with a field).
    var custom: AnyView? = nil
}

/// Dialogs drawn over the whole window, the bottom bar included (Android's dialog window).
@MainActor
final class SkyDialogs: ObservableObject {
    @Published var current: SkyDialog?
    @Published var sheet: SkySheet?
    @Published private(set) var toastText: Text?
    @Published private(set) var snackText: Text?
    private var toastTask: Task<Void, Never>?
    private var snackTask: Task<Void, Never>?

    func show(_ dialog: SkyDialog) {
        withAnimation(.easeOut(duration: 0.15)) { current = dialog }
    }

    func dismiss() {
        withAnimation(.easeIn(duration: 0.1)) { current = nil }
    }

    /// Android's short Toast: a pill near the bottom for about two seconds.
    func toast(_ text: Text) {
        toastTask?.cancel()
        withAnimation(.easeOut(duration: 0.15)) { toastText = text }
        toastTask = Task {
            try? await Task.sleep(for: .seconds(2))
            guard !Task.isCancelled else { return }
            withAnimation(.easeIn(duration: 0.2)) { toastText = nil }
        }
    }

    /// A Snackbar: above the bar, four seconds.
    func snackbar(_ text: Text) {
        snackTask?.cancel()
        withAnimation(.easeOut(duration: 0.15)) { snackText = text }
        snackTask = Task {
            try? await Task.sleep(for: .seconds(4))
            guard !Task.isCancelled else { return }
            withAnimation(.easeIn(duration: 0.2)) { snackText = nil }
        }
    }

    func showCustom<Content: View>(@ViewBuilder _ content: () -> Content) {
        show(SkyDialog(custom: AnyView(content())))
    }

    func showHelp(_ topic: HelpTopic) {
        show(SkyDialog(title: Text(topic.title), message: Text(topic.body), scrollable: true,
                       actions: [SkyDialog.Action(label: Text("help_close"))]))
    }
}

struct SkyDialogLayer: View {
    @ObservedObject var dialogs: SkyDialogs

    var body: some View {
        ZStack {
            if let toast = dialogs.toastText {
                toast.skyText(.bodyMedium)
                    .foregroundStyle(Color.skyInverseOnSurface)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 12)
                    .background(Color.skyInverseSurface, in: .sky(20))
                    .frame(maxHeight: .infinity, alignment: .bottom)
                    .padding(.bottom, 128)
                    .allowsHitTesting(false)
                    .transition(.opacity)
            }
            if let snack = dialogs.snackText {
                snack.skyText(.bodyMedium)
                    .foregroundStyle(Color.skyInverseOnSurface)
                    .frame(maxWidth: .infinity, minHeight: 48, alignment: .leading)
                    .padding(.horizontal, 16)
                    .background(Color.skyInverseSurface, in: .sky(SkyRadius.extraSmall))
                    .skyElevation(6)
                    .padding(12)
                    .frame(maxHeight: .infinity, alignment: .bottom)
                    .padding(.bottom, SkyBottomBar.height)
                    .allowsHitTesting(false)
                    .transition(.opacity)
            }
            dialog
        }
    }

    @ViewBuilder private var dialog: some View {
        if let dialog = dialogs.current {
            GeometryReader { geometry in
                ZStack {
                    // The platform's dialog dim: black at 60 %.
                    Color.black.opacity(0.6).ignoresSafeArea()
                        .onTapGesture { dialogs.dismiss() }
                    DialogCard(dialog: dialog, dialogs: dialogs, maxHeight: geometry.size.height * 0.8)
                        .frame(width: min(320, geometry.size.width - 48))
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            .transition(.opacity)
        }
    }

    private struct DialogCard: View {
        let dialog: SkyDialog
        let dialogs: SkyDialogs
        let maxHeight: CGFloat

        var body: some View {
            if let custom = dialog.custom {
                custom
                    .padding(24)
                    .frame(maxHeight: maxHeight)
                    .background(Color.skyContainerHigh, in: .sky(SkyRadius.extraLarge))
            } else {
                standard
            }
        }

        private var standard: some View {
            VStack(alignment: .leading, spacing: 0) {
                dialog.title.skyText(.headlineSmall).foregroundStyle(Color.skyOnSurface)
                if let message = dialog.message {
                    let text = message.skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Group {
                        if dialog.scrollable {
                            ScrollView { text }.fixedSize(horizontal: false, vertical: true)
                        } else {
                            text
                        }
                    }
                    .padding(.top, 16)
                }
                HStack(spacing: 8) {
                    Spacer(minLength: 0)
                    ForEach(Array(dialog.actions.enumerated()), id: \.offset) { _, action in
                        Button {
                            dialogs.dismiss()
                            action.handler()
                        } label: {
                            action.label
                        }
                        .buttonStyle(SkyTextButtonStyle(color: action.destructive ? .skyError : .skyPrimary))
                    }
                }
                .padding(.top, 24)
            }
            .padding(24)
            .frame(maxHeight: maxHeight)
            .background(Color.skyContainerHigh, in: .sky(SkyRadius.extraLarge))
        }
    }
}

// MARK: Bottom bar

/// SkyNavBar (ui/SkywireApp.kt): a floating 64 pt shell, four icons, the raised cloud.
struct SkyBottomBar: View {
    @ObservedObject var navigator: Navigator
    /// 21 pt of cloud above the 64 pt shell, and 8 pt under it (plus the safe area).
    static let height: CGFloat = 93

    var body: some View {
        ZStack(alignment: .top) {
            HStack(spacing: 0) {
                slot(.home, idle: MI.outlinedHome, selected: MI.roundedHome, label: Text("tab_home"), id: "tab-home")
                slot(.chat, idle: MI.outlinedChat, selected: MI.roundedChat, label: Text("tab_chat"), id: "tab-chat")
                Color.clear.frame(maxWidth: .infinity)
                slot(.wallet, idle: MI.outlinedAccountBalanceWallet, selected: MI.roundedAccountBalanceWallet, label: Text("tab_wallet"), id: "tab-wallet")
                slot(.settings, idle: MI.outlinedSettings, selected: MI.roundedSettings, label: Text("tab_settings"), id: "tab-settings")
            }
            .frame(height: 64)
            .background(Color.skyContainerLowest, in: .sky(SkyRadius.extraLarge))
            .overlay(RoundedRectangle(cornerRadius: SkyRadius.extraLarge).strokeBorder(Color.skyOutlineVariant, lineWidth: 1))
            .skyElevation(8)
            .padding(.top, 21)
            CloudButton { navigator.openHub() }
        }
        .padding(.horizontal, 12)
        .padding(.bottom, 8)
        .background(Color.skyBackground.ignoresSafeArea())
    }

    private func slot(_ tab: AppTab, idle: String, selected: String, label: Text, id: String) -> some View {
        let isSelected = navigator.selectedSlot == tab
        return Button {
            navigator.select(tab)
        } label: {
            MaterialIcon(isSelected ? selected : idle, size: 26)
                .foregroundStyle(isSelected ? Color.skyPrimary : Color.skyOutline)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .contentShape(Rectangle())
        }
        .buttonStyle(PressStyle())
        .accessibilityLabel(label)
        .accessibilityIdentifier(id)
        .accessibilityAddTraits(isSelected ? .isSelected : [])
    }
}

/// The raised cloud: a 58 pt gradient disc in a 4 pt ring of the shell's colour.
private struct CloudButton: View {
    let action: () -> Void

    var body: some View {
        ZStack {
            PulseRing(size: 66)
            Button(action: action) {
                ZStack {
                    Circle().fill(Color.skyContainerLowest)
                    Circle().fill(SkyGradient.button).padding(4)
                    Image("skywire-logo").renderingMode(.template).resizable().scaledToFit()
                        .foregroundStyle(.white)
                        .frame(width: 34, height: 34)
                }
                .frame(width: 66, height: 66)
            }
            .buttonStyle(PressStyle(layer: .white, shape: AnyShape(Circle())))
            .accessibilityLabel(Text("tab_hub_description"))
            .accessibilityIdentifier("tab-hub")
        }
    }
}

/// A custom dialog's title and its end-aligned text buttons (AlertDialog's own layout).
struct DialogFrame<Body: View>: View {
    let title: Text
    var confirm: LocalizedStringKey
    var destructive = false
    var enabled = true
    let onConfirm: () -> Void
    @ViewBuilder let content: Body
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            title.skyText(.headlineSmall).foregroundStyle(Color.skyOnSurface)
            content.padding(.top, 16)
            HStack(spacing: 8) {
                Spacer(minLength: 0)
                Button { dialogs.dismiss() } label: { Text("cancel") }.buttonStyle(.skyText)
                Button {
                    dialogs.dismiss()
                    onConfirm()
                } label: { Text(confirm) }
                .buttonStyle(SkyTextButtonStyle(color: destructive ? .skyError : .skyPrimary))
                .disabled(!enabled)
            }
            .padding(.top, 24)
        }
    }
}
