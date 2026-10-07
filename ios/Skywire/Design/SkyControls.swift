import SwiftUI

// Android's shared controls: Material 3 1.4.0 defaults with the Skywire theme applied.

/// SectionCard (ui/components/ServerUi.kt): the standard card.
struct SectionCard<Content: View>: View {
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 0) { content }
            .padding(20)
            .frame(maxWidth: .infinity, alignment: .leading)
            .foregroundStyle(Color.skyOnSurface)
            .background(Color.skySurfaceVariant, in: .sky(SkyRadius.large))
            .overlay(RoundedRectangle(cornerRadius: SkyRadius.large).strokeBorder(Color.skyOutlineVariant, lineWidth: 1))
    }
}

/// InfoRow (ServerUi.kt): a label left, a value right that wraps.
struct SkyInfoRow: View {
    let label: Text
    let value: String
    var mono = false
    var valueColor: Color = .skyOnSurface

    var body: some View {
        HStack(alignment: .center, spacing: 16) {
            label.skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant).lineLimit(1).fixedSize()
            Text(verbatim: value)
                .skyText(.bodyMedium, mono: mono)
                .foregroundStyle(valueColor)
                .multilineTextAlignment(.trailing)
                .frame(maxWidth: .infinity, alignment: .trailing)
        }
        .padding(.vertical, 4)
    }
}

/// A divider of Android's `HorizontalDivider` (1 pt outlineVariant by default).
struct SkyDivider: View {
    var color: Color = .skyOutlineVariant

    var body: some View {
        Rectangle().fill(color).frame(height: 1)
    }
}

/// A status dot (10 pt by default).
struct StatusDot: View {
    let color: Color
    var size: CGFloat = 10

    var body: some View {
        Circle().fill(color).frame(width: size, height: size)
    }
}

// MARK: Buttons

private struct PillLabel<Label: View>: View {
    let label: Label
    let pressed: Bool
    let fill: Color
    let ink: Color
    let horizontal: CGFloat
    var height: CGFloat = 40
    @Environment(\.isEnabled) private var enabled

    var body: some View {
        label
            .skyText(.labelLarge)
            .lineLimit(1)
            .foregroundStyle(enabled ? ink : Color.skyOnSurface.opacity(0.38))
            .padding(.horizontal, horizontal)
            .frame(minWidth: 58, minHeight: height)
            .background(enabled ? fill : (fill == .clear ? .clear : Color.skyOnSurface.opacity(0.12)), in: Capsule())
            .overlay(Capsule().fill(ink.opacity(pressed ? 0.10 : 0)))
            .contentShape(Capsule())
            // Material reserves 48 pt for the 40 pt pill; a fixed height (50, 52) takes exactly that.
            .padding(.vertical, height == 40 ? 4 : 0)
    }
}

/// FilledTonalButton: the app's standalone action.
struct TonalButtonStyle: ButtonStyle {
    var height: CGFloat = 40
    var horizontal: CGFloat = 24

    func makeBody(configuration: Configuration) -> some View {
        PillLabel(label: configuration.label, pressed: configuration.isPressed,
                  fill: .skySecondaryContainer, ink: .skyOnSecondaryContainer, horizontal: horizontal, height: height)
    }
}

/// Button: filled primary.
struct FilledButtonStyle: ButtonStyle {
    var height: CGFloat = 40

    func makeBody(configuration: Configuration) -> some View {
        PillLabel(label: configuration.label, pressed: configuration.isPressed,
                  fill: .skyPrimary, ink: .skyOnPrimary, horizontal: 24, height: height)
    }
}

/// TextButton: dialog actions and inline links.
struct SkyTextButtonStyle: ButtonStyle {
    var color: Color = .skyPrimary

    func makeBody(configuration: Configuration) -> some View {
        PillLabel(label: configuration.label, pressed: configuration.isPressed,
                  fill: .clear, ink: color, horizontal: 12)
    }
}

extension ButtonStyle where Self == TonalButtonStyle {
    static var tonal: TonalButtonStyle { TonalButtonStyle() }
}

extension ButtonStyle where Self == FilledButtonStyle {
    static var filled: FilledButtonStyle { FilledButtonStyle() }
}

extension ButtonStyle where Self == SkyTextButtonStyle {
    static var skyText: SkyTextButtonStyle { SkyTextButtonStyle() }
}

/// A plain press: no highlight (Android's bar slots), or a 10 % state layer.
struct PressStyle: ButtonStyle {
    var layer: Color? = nil
    var shape: AnyShape = AnyShape(Rectangle())

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .overlay(layer.map { color in shape.fill(color.opacity(configuration.isPressed ? 0.10 : 0)) })
    }
}

// MARK: Chips and switches

/// FilterChip: 32 pt tall (48 of layout), 12 pt corners.
struct SkyFilterChip: View {
    let label: Text
    let selected: Bool
    var style: SkyTextStyle = .labelLarge
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            label
                .skyText(style)
                .lineLimit(1)
                .foregroundStyle(selected ? Color.skyOnSecondaryContainer : Color.skyOnSurfaceVariant)
                .padding(.horizontal, 16)
                .frame(height: 32)
                .background(selected ? Color.skySecondaryContainer : .clear, in: .sky(SkyRadius.small))
                .overlay(RoundedRectangle(cornerRadius: SkyRadius.small)
                    .strokeBorder(selected ? .clear : Color.skyOutlineVariant, lineWidth: 1))
                .contentShape(RoundedRectangle(cornerRadius: SkyRadius.small))
                .padding(.vertical, 8)
        }
        .buttonStyle(PressStyle(layer: .skyOnSurface, shape: AnyShape(RoundedRectangle(cornerRadius: SkyRadius.small))))
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// Material 3 Switch: 52 x 32 track, thumb 16 off and 24 on. A real switch to accessibility.
struct SkySwitchStyle: ToggleStyle {
    var checkedTrack: Color = .skyPrimary
    var checkedThumb: Color = .skyOnPrimary
    var uncheckedTrack: Color = .skyContainerHighest
    var uncheckedThumb: Color = .skyOutline
    var uncheckedBorder: Color = .skyOutline

    func makeBody(configuration: Configuration) -> some View {
        SwitchBody(configuration: configuration, style: self)
    }

    private struct SwitchBody: View {
        let configuration: Configuration
        let style: SkySwitchStyle
        @Environment(\.isEnabled) private var enabled
        @GestureState private var pressed = false

        var body: some View {
            let on = configuration.isOn
            let thumb: CGFloat = pressed ? 28 : (on ? 24 : 16)
            // Only the switch: every caller lays out its own title; the label is for accessibility.
            HStack(spacing: 0) {
                ZStack {
                    Capsule().fill(trackColor(on))
                    Capsule().strokeBorder(on ? .clear : borderColor, lineWidth: 2)
                    Circle().fill(thumbColor(on))
                        .frame(width: thumb, height: thumb)
                        .offset(x: on ? 10 : -10)
                }
                .frame(width: 52, height: 32)
                .frame(height: 48)
                .contentShape(Rectangle())
                .onTapGesture { configuration.isOn.toggle() }
                .animation(.spring(response: 0.25, dampingFraction: 0.9), value: on)
            }
            .accessibilityRepresentation { Toggle(isOn: configuration.$isOn) { configuration.label } }
        }

        private func trackColor(_ on: Bool) -> Color {
            guard enabled else { return on ? Color.skyOnSurface.opacity(0.12) : style.uncheckedTrack.opacity(0.12) }
            return on ? style.checkedTrack : style.uncheckedTrack
        }

        private func thumbColor(_ on: Bool) -> Color {
            guard enabled else { return on ? Color.skyBackground : Color.skyOnSurface.opacity(0.38) }
            return on ? style.checkedThumb : style.uncheckedThumb
        }

        private var borderColor: Color {
            enabled ? style.uncheckedBorder : Color.skyOnSurface.opacity(0.12)
        }
    }
}

extension ToggleStyle where Self == SkySwitchStyle {
    static var skySwitch: SkySwitchStyle { SkySwitchStyle() }
}

// MARK: Progress

/// CircularProgressIndicator, indeterminate: a rotating arc.
struct MaterialSpinner: View {
    var size: CGFloat = 40
    var stroke: CGFloat = 4
    var color: Color = .skyPrimary

    var body: some View {
        TimelineView(.animation) { context in
            let t = context.date.timeIntervalSinceReferenceDate
            // Material's arc grows and shrinks while the whole turns (1.3 s cycle).
            let phase = (t.truncatingRemainder(dividingBy: 1.333)) / 1.333
            let sweep = 0.1 + 0.65 * (phase < 0.5 ? phase * 2 : 2 - phase * 2)
            Circle()
                .trim(from: 0, to: sweep)
                .stroke(color, style: StrokeStyle(lineWidth: stroke, lineCap: .round))
                .rotationEffect(.degrees(t.truncatingRemainder(dividingBy: 1.568) / 1.568 * 360 + phase * 360))
        }
        .frame(width: size - stroke, height: size - stroke)
        .frame(width: size, height: size)
    }
}

/// PulseRing (ui/components/Effects.kt): a filled circle that grows and fades, 2.6 s.
struct PulseRing: View {
    let size: CGFloat
    var color: Color = .skyPrimary

    var body: some View {
        TimelineView(.animation) { context in
            let phase = context.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 2.6) / 2.6
            Circle()
                .fill(color)
                .frame(width: size, height: size)
                .scaleEffect(0.9 + 0.45 * phase)
                .opacity(0.45 * (1 - phase))
        }
        .frame(width: size, height: size)
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}

/// Slider (Material 3 1.4): a 16 pt track, a 4 x 44 handle in 6 pt gaps, a stop dot at the end.
struct SkySlider: View {
    @Binding var value: Double
    let range: ClosedRange<Double>
    @GestureState private var pressed = false

    var body: some View {
        GeometryReader { geometry in
            let width = geometry.size.width
            let span = range.upperBound - range.lowerBound
            let fraction = span > 0 ? min(max((value - range.lowerBound) / span, 0), 1) : 0
            // The handle's centre travels between its own half-widths.
            let x = 2 + (width - 4) * fraction
            ZStack(alignment: .leading) {
                if x - 6 > 0 {
                    UnevenTrack(leading: 8, trailing: 2).fill(Color.skyPrimary)
                        .frame(width: x - 6, height: 16)
                }
                UnevenTrack(leading: 2, trailing: 8).fill(Color.skySecondaryContainer)
                    .frame(width: max(0, width - x - 6), height: 16)
                    .offset(x: x + 6)
                Circle().fill(Color.skyPrimary).frame(width: 4, height: 4).offset(x: width - 8)
                Capsule().fill(Color.skyPrimary)
                    .frame(width: pressed ? 2 : 4, height: 44)
                    .offset(x: x - (pressed ? 1 : 2))
            }
            .frame(maxHeight: .infinity)
            .contentShape(Rectangle())
            .gesture(DragGesture(minimumDistance: 0)
                .updating($pressed) { _, state, _ in state = true }
                .onChanged { drag in
                    let f = min(max((drag.location.x - 2) / max(1, width - 4), 0), 1)
                    value = range.lowerBound + f * span
                })
        }
        .frame(height: 44)
        .accessibilityRepresentation { Slider(value: $value, in: range) }
    }
}

/// A track piece: fully round at one end, nearly square at the end facing the handle.
private struct UnevenTrack: Shape {
    let leading: CGFloat
    let trailing: CGFloat

    func path(in rect: CGRect) -> Path {
        let l = min(leading, rect.height / 2, rect.width / 2)
        let t = min(trailing, rect.height / 2, rect.width / 2)
        var path = Path()
        path.move(to: CGPoint(x: rect.minX + l, y: rect.minY))
        path.addArc(tangent1End: CGPoint(x: rect.maxX, y: rect.minY), tangent2End: CGPoint(x: rect.maxX, y: rect.maxY), radius: t)
        path.addArc(tangent1End: CGPoint(x: rect.maxX, y: rect.maxY), tangent2End: CGPoint(x: rect.minX, y: rect.maxY), radius: t)
        path.addArc(tangent1End: CGPoint(x: rect.minX, y: rect.maxY), tangent2End: CGPoint(x: rect.minX, y: rect.minY), radius: l)
        path.addArc(tangent1End: CGPoint(x: rect.minX, y: rect.minY), tangent2End: CGPoint(x: rect.maxX, y: rect.minY), radius: l)
        path.closeSubpath()
        return path
    }
}

/// A row sharing its width by weight, as Compose's `Modifier.weight` does.
struct WeightedRow: Layout {
    let weights: [CGFloat]
    var spacing: CGFloat = 0

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let widths = share(proposal.width ?? 0, subviews.count)
        let height = subviews.indices.map { subviews[$0].sizeThatFits(ProposedViewSize(width: widths[$0], height: proposal.height)).height }.max() ?? 0
        return CGSize(width: proposal.width ?? 0, height: height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let widths = share(bounds.width, subviews.count)
        var x = bounds.minX
        for index in subviews.indices {
            subviews[index].place(at: CGPoint(x: x, y: bounds.midY), anchor: .leading,
                                  proposal: ProposedViewSize(width: widths[index], height: bounds.height))
            x += widths[index] + spacing
        }
    }

    private func share(_ width: CGFloat, _ count: Int) -> [CGFloat] {
        let w = (0..<count).map { $0 < weights.count ? weights[$0] : 1 }
        let free = max(0, width - spacing * CGFloat(max(0, count - 1)))
        let total = w.reduce(0, +)
        return w.map { total > 0 ? free * $0 / total : 0 }
    }
}

/// A row whose children share its width in proportion to their own widths (Android's Identity
/// buttons, weighted by their labels' measured widths).
struct ProportionalRow: Layout {
    var spacing: CGFloat = 12

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let widths = share(proposal.width, subviews)
        let height = subviews.indices.map { subviews[$0].sizeThatFits(ProposedViewSize(width: widths[$0], height: nil)).height }.max() ?? 0
        return CGSize(width: proposal.width ?? widths.reduce(0, +) + spacing * CGFloat(max(0, subviews.count - 1)), height: height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let widths = share(bounds.width, subviews)
        var x = bounds.minX
        for index in subviews.indices {
            subviews[index].place(at: CGPoint(x: x, y: bounds.midY), anchor: .leading,
                                  proposal: ProposedViewSize(width: widths[index], height: bounds.height))
            x += widths[index] + spacing
        }
    }

    private func share(_ width: CGFloat?, _ subviews: Subviews) -> [CGFloat] {
        let ideal = subviews.map { $0.sizeThatFits(.unspecified).width }
        guard let width else { return ideal }
        let free = max(0, width - spacing * CGFloat(max(0, subviews.count - 1)))
        let total = ideal.reduce(0, +)
        return ideal.map { total > 0 ? free * $0 / total : 0 }
    }
}
