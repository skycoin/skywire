import SwiftUI
import UIKit

// Material 3 form controls and the bottom sheet, as Android draws them.

/// ModalBottomSheet's content, drawn by SkySheetLayer over the whole window.
struct SkySheet: Identifiable {
    let id = UUID()
    let content: AnyView
}

extension SkyDialogs {
    func presentSheet<Content: View>(@ViewBuilder _ content: () -> Content) {
        withAnimation(.easeOut(duration: 0.25)) { sheet = SkySheet(content: AnyView(content())) }
    }

    func dismissSheet() {
        withAnimation(.easeIn(duration: 0.2)) { sheet = nil }
    }
}

/// The sheet: surfaceContainerLow, 28 pt top corners, a 32 x 4 handle, a 32 % scrim.
struct SkySheetLayer: View {
    @ObservedObject var dialogs: SkyDialogs
    @GestureState private var drag: CGFloat = 0

    var body: some View {
        ZStack(alignment: .bottom) {
            if let sheet = dialogs.sheet {
                Color.black.opacity(0.32).ignoresSafeArea()
                    .onTapGesture { dialogs.dismissSheet() }
                    .transition(.opacity)
                VStack(spacing: 0) {
                    Capsule().fill(Color.skyOnSurfaceVariant.opacity(0.4)).frame(width: 32, height: 4).padding(.vertical, 22)
                    sheet.content
                }
                .frame(maxWidth: 640)
                .frame(maxWidth: .infinity)
                .foregroundStyle(Color.skyOnSurface)
                .background(TopRounded(radius: SkyRadius.extraLarge).fill(Color.skyContainerLow).ignoresSafeArea(edges: .bottom))
                .offset(y: max(0, drag))
                .gesture(DragGesture()
                    .updating($drag) { value, state, _ in state = value.translation.height }
                    .onEnded { value in if value.translation.height > 100 { dialogs.dismissSheet() } })
                .transition(.move(edge: .bottom))
                .id(sheet.id)
            }
        }
    }
}

/// A rectangle with only its top corners rounded (iOS 16 has no UnevenRoundedRectangle).
struct TopRounded: Shape {
    let radius: CGFloat

    func path(in rect: CGRect) -> Path {
        Path(UIBezierPath(roundedRect: rect, byRoundingCorners: [.topLeft, .topRight],
                          cornerRadii: CGSize(width: radius, height: radius)).cgPath)
    }
}

/// OutlinedTextField: 56 pt, 8 pt corners, a label that floats into the border when focused or filled.
struct SkyOutlinedTextField: View {
    var label: LocalizedStringKey? = nil
    var placeholder: LocalizedStringKey? = nil
    @Binding var text: String
    var isError = false
    var keyboard: UIKeyboardType = .default
    var mono = false
    /// The colour behind the field, for the label's notch in the border.
    var notch: Color = .skyBackground
    var identifier: String? = nil
    @FocusState private var focused: Bool

    var body: some View {
        let floating = focused || !text.isEmpty
        ZStack(alignment: .leading) {
            RoundedRectangle(cornerRadius: SkyRadius.extraSmall)
                .strokeBorder(borderColor, lineWidth: focused || isError ? 2 : 1)
            if let label {
                Text(label)
                    .skyText(floating ? .bodySmall : .bodyLarge)
                    .foregroundStyle(isError ? Color.skyError : (focused ? Color.skyPrimary : Color.skyOnSurfaceVariant))
                    .padding(.horizontal, floating ? 4 : 0)
                    .background(floating ? notch : .clear)
                    .offset(y: floating ? -28 : 0)
                    .padding(.leading, floating ? 12 : 16)
                    .allowsHitTesting(false)
            }
            if let placeholder, text.isEmpty, label == nil || focused {
                Text(placeholder).skyText(.bodyLarge).foregroundStyle(Color.skyOnSurfaceVariant)
                    .padding(.leading, 16).allowsHitTesting(false)
            }
            // An empty prompt: the label and placeholder above are the field's own drawing.
            TextField(text: $text, prompt: Text(verbatim: "")) { label.map { Text($0) } ?? placeholder.map { Text($0) } ?? Text(verbatim: "") }
                .focused($focused)
                .font(Font(SkyTextStyle.bodyLarge.uiFont(mono: mono)))
                .foregroundStyle(Color.skyOnSurface)
                .tint(.skyPrimary)
                .keyboardType(keyboard)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
                .padding(.horizontal, 16)
                .accessibilityIdentifier(identifier ?? "")
        }
        .frame(minHeight: 56)
        .contentShape(Rectangle())
        .onTapGesture { focused = true }
    }

    private var borderColor: Color {
        if isError { return .skyError }
        return focused ? .skyPrimary : .skyOutline
    }
}

/// RadioButton: a 20 pt ring with a 12 pt dot, in a 48 pt target.
struct SkyRadio: View {
    let selected: Bool

    var body: some View {
        ZStack {
            Circle().strokeBorder(selected ? Color.skyPrimary : Color.skyOnSurfaceVariant, lineWidth: 2).frame(width: 20, height: 20)
            if selected {
                Circle().fill(Color.skyPrimary).frame(width: 12, height: 12)
            }
        }
        .frame(width: 48, height: 48)
        .accessibilityHidden(true)
    }
}

/// The sheet's footer: Cancel as text, the action as a filled button, at the end.
struct SheetButtons: View {
    let confirm: LocalizedStringKey
    var enabled = true
    let onConfirm: () -> Void
    @EnvironmentObject private var dialogs: SkyDialogs

    var body: some View {
        HStack(spacing: 8) {
            Spacer()
            Button { dialogs.dismissSheet() } label: { Text("cancel") }.buttonStyle(.skyText)
            Button {
                dialogs.dismissSheet()
                onConfirm()
            } label: { Text(confirm) }
            .buttonStyle(.filled)
            .disabled(!enabled)
        }
    }
}
