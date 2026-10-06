import SwiftUI
import UIKit

/// Where kept scroll views stand, by their first visible item and how far into it: Android's
/// saved LazyListState, which outlives the activity a language change recreates. RootView's
/// rebuild on a language change makes new scroll views; a kept one is put back in its place.
@MainActor
final class ScrollPlaces {
    struct Place: Equatable {
        let item: Int
        let offset: CGFloat
    }

    private var trackers: [String: ScrollPlaceTracker] = [:]
    private var saved: [String: Place] = [:]

    /// Records where every kept scroll view stands, just before the rebuild.
    func capture() {
        for (key, tracker) in trackers {
            saved[key] = tracker.place
        }
    }

    fileprivate func attach(_ tracker: ScrollPlaceTracker, _ key: String) {
        trackers[key] = tracker
        if let place = saved.removeValue(forKey: key) {
            tracker.pending = place
            tracker.restoreIfReady()
        }
    }

    fileprivate func detach(_ tracker: ScrollPlaceTracker, _ key: String) {
        if trackers[key] === tracker { trackers[key] = nil }
    }
}

extension View {
    /// The scroll view's content: its place is kept under `key` (see ScrollPlaces).
    func keepsScrollPlace(_ key: String, in places: ScrollPlaces) -> some View {
        modifier(KeepsScrollPlace(key: key, places: places))
    }

    /// One item of kept content, numbered top to bottom.
    func scrollPlaceItem(_ index: Int) -> some View {
        background(GeometryReader { geometry in
            Color.clear.preference(key: ScrollItemFrames.self, value: [index: geometry.frame(in: .named(ScrollPlaceTracker.space))])
        })
    }
}

@MainActor
private final class ScrollPlaceTracker: ObservableObject {
    static let space = "scroll-place"

    weak var scrollView: UIScrollView?
    /// Each item's frame in the content's own coordinates.
    var frames: [Int: CGRect] = [:]
    var pending: ScrollPlaces.Place?

    var place: ScrollPlaces.Place? {
        guard let scrollView else { return nil }
        let top = scrollView.contentOffset.y + scrollView.adjustedContentInset.top
        guard let first = frames.sorted(by: { $0.key < $1.key }).first(where: { $0.value.maxY > top }) else { return nil }
        return ScrollPlaces.Place(item: first.key, offset: top - first.value.minY)
    }

    func restoreIfReady() {
        guard let pending, let scrollView, let frame = frames[pending.item] else { return }
        self.pending = nil
        scrollView.layoutIfNeeded()
        let inset = scrollView.adjustedContentInset
        let bottom = max(-inset.top, scrollView.contentSize.height + inset.bottom - scrollView.bounds.height)
        let y = min(max(frame.minY + pending.offset - inset.top, -inset.top), bottom)
        scrollView.setContentOffset(CGPoint(x: scrollView.contentOffset.x, y: y), animated: false)
    }
}

private struct ScrollItemFrames: PreferenceKey {
    static let defaultValue: [Int: CGRect] = [:]

    static func reduce(value: inout [Int: CGRect], nextValue: () -> [Int: CGRect]) {
        value.merge(nextValue()) { _, new in new }
    }
}

private struct KeepsScrollPlace: ViewModifier {
    let key: String
    let places: ScrollPlaces
    @StateObject private var tracker = ScrollPlaceTracker()

    func body(content: Content) -> some View {
        content
            .coordinateSpace(name: ScrollPlaceTracker.space)
            .onPreferenceChange(ScrollItemFrames.self) { [tracker] frames in
                MainActor.assumeIsolated {
                    tracker.frames = frames
                    tracker.restoreIfReady()
                }
            }
            .background(ScrollViewFinder { [tracker] scrollView in
                tracker.scrollView = scrollView
                tracker.restoreIfReady()
            })
            .onAppear { places.attach(tracker, key) }
            .onDisappear { places.detach(tracker, key) }
    }
}

/// Hands over the UIScrollView a SwiftUI ScrollView draws with, from inside its content.
private struct ScrollViewFinder: UIViewRepresentable {
    let found: (UIScrollView) -> Void

    func makeUIView(context: Context) -> FinderView {
        FinderView(found: found)
    }

    func updateUIView(_ view: FinderView, context: Context) {}

    final class FinderView: UIView {
        let found: (UIScrollView) -> Void

        init(found: @escaping (UIScrollView) -> Void) {
            self.found = found
            super.init(frame: .zero)
            isUserInteractionEnabled = false
            isAccessibilityElement = false
        }

        @available(*, unavailable)
        required init?(coder: NSCoder) { nil }

        override func didMoveToWindow() {
            super.didMoveToWindow()
            guard window != nil else { return }
            var view = superview
            while let current = view, !(current is UIScrollView) {
                view = current.superview
            }
            if let scrollView = view as? UIScrollView { found(scrollView) }
        }
    }
}
