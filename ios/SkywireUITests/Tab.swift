/// RootView's tabs, in their order in the tab bar. Tests name a tab rather
/// than count to it, so a tab added in between does not quietly move them.
enum Tab: Int {
    case home, chat, apps, settings
}
