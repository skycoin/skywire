import CoreClient
@testable import Skywire
import XCTest

/// skychat writes its call records and the join request notification in English, and the app
/// translates them by matching that text (Android: NotificationBridge shownTitle and shownBody).
@MainActor
final class NotificationTextTests: XCTestCase {
    private var suite = ""
    private var settings: AppSettings!
    private var was: AppLanguage!

    override func setUp() async throws {
        suite = "NotificationTextTests-\(UUID().uuidString)"
        settings = AppSettings(defaults: try XCTUnwrap(UserDefaults(suiteName: suite)))
        was = AppLanguage.current()
        settings.language = .spanish
    }

    override func tearDown() async throws {
        L10n.bundle = was.bundle
        L10n.locale = was.locale
        UserDefaults.standard.removePersistentDomain(forName: suite)
    }

    private func chat(_ title: String, _ body: String, tag: String) -> NotifyEvent {
        NotifyEvent(app: SkychatProfile.app, title: title, body: body, tag: tag)
    }

    func testAJoinRequestIsTranslated() {
        let one = chat("Group join request", "1 waiting to join Los Amigos", tag: "join:g1")
        XCTAssertEqual(NotificationBridge.shownTitle(one), L10n.text("chat_notif_join_request"))
        XCTAssertNotEqual(NotificationBridge.shownTitle(one), "Group join request")
        XCTAssertEqual(NotificationBridge.shownBody(one, NotifPrefs()), "1 persona espera unirse a Los Amigos")
        let more = chat("Group join request", "12 waiting to join waiting to join us", tag: "join:g1")
        XCTAssertEqual(NotificationBridge.shownBody(more, NotifPrefs()), "12 personas esperan unirse a waiting to join us")
    }

    func testAMessageRequestTitleAndAMissedCallAreTranslated() {
        let request = chat("Message request", "hola", tag: "02ab")
        XCTAssertEqual(NotificationBridge.shownTitle(request), L10n.text("chat_notif_message_request"))
        XCTAssertNotEqual(NotificationBridge.shownTitle(request), "Message request")
        let missed = chat("Ana", "📞 Missed call", tag: "02ab")
        XCTAssertEqual(NotificationBridge.shownBody(missed, NotifPrefs()), "📞 " + L10n.text("call_missed_title"))
        XCTAssertNotEqual(NotificationBridge.shownBody(missed, NotifPrefs()), "📞 Missed call")
    }

    /// group.go titles a group or channel post "Group message"; the phone shows the group's name.
    func testAGroupPostIsTitledWithItsGroup() {
        let post = chat("Group message", "Acer: hola", tag: "g1")
        XCTAssertEqual(NotificationBridge.shownTitle(post, groupName: "Liuli"), "Liuli")
        XCTAssertEqual(NotificationBridge.shownTitle(post), L10n.text("chat_notif_group_message"))
        XCTAssertEqual(NotificationBridge.shownTitle(post, groupName: " "), L10n.text("chat_notif_group_message"))
        XCTAssertNotEqual(NotificationBridge.shownTitle(post), "Group message")
    }

    /// A file arrives named in English: "📎 <name>" in a group, "📎 <Kind>: <name>" in a chat.
    func testARecordedClipIsNamedInTheAppsLanguage() {
        func shown(_ body: String) -> String { NotificationBridge.localizeFileBody(body, voice: "语音消息", video: "视频消息") }
        XCTAssertEqual(shown("Acer: 📎 voice-20261116-134400.m4a"), "Acer: 🎤 语音消息")
        XCTAssertEqual(shown("Acer: 📎 video-sq-20261116-134400.mp4"), "Acer: 📹 视频消息")
        XCTAssertEqual(shown("📎 Audio: voice-20261120-171651.oga"), "🎤 语音消息")
        XCTAssertEqual(shown("📎 Video: video-20261120-171651.mp4"), "📹 视频消息")
        // An attachment keeps its name without the English kind in front.
        XCTAssertEqual(shown("📎 Image: photo.jpg"), "📎 photo.jpg")
        XCTAssertEqual(shown("Acer: 📎 report.pdf"), "Acer: 📎 report.pdf")
        // A name that only starts like a clip, and plain text, are left alone.
        XCTAssertEqual(shown("📎 File: voice-notes.txt"), "📎 voice-notes.txt")
        XCTAssertEqual(shown("Acer: Audio: later"), "Acer: Audio: later")
        let clip = chat("Group message", "Acer: 📎 voice-20261116-134400.m4a", tag: "g1")
        XCTAssertEqual(NotificationBridge.shownBody(clip, NotifPrefs()), "Acer: 🎤 " + L10n.text("chat_voice_message"))
    }

    /// Anything else passes as it came: a name, another app's text, a body that only looks alike.
    func testOtherTextIsLeftAlone() {
        XCTAssertEqual(NotificationBridge.shownTitle(chat("Ana", "hola", tag: "02ab")), "Ana")
        let mail = NotifyEvent(app: MailProfile.app, title: "Group join request", body: "3 waiting to join x", tag: "join:g1")
        XCTAssertEqual(NotificationBridge.shownTitle(mail), "Group join request")
        XCTAssertEqual(NotificationBridge.shownBody(mail, NotifPrefs()), "3 waiting to join x")
        let message = chat("Ana", "3 waiting to join x", tag: "02ab")
        XCTAssertEqual(NotificationBridge.shownBody(message, NotifPrefs()), "3 waiting to join x")
    }

    /// With previews off a message hides its text, while a call and a join request keep theirs.
    func testPreviewsOffHideOnlyMessages() {
        var prefs = NotifPrefs()
        prefs.preview = false
        XCTAssertEqual(NotificationBridge.shownBody(chat("Ana", "hola", tag: "02ab"), prefs), L10n.text("chat_notif_new_message"))
        XCTAssertEqual(NotificationBridge.shownBody(chat("Ana", "📞 Missed call", tag: "02ab"), prefs), "📞 " + L10n.text("call_missed_title"))
        XCTAssertEqual(NotificationBridge.shownBody(chat("Group join request", "2 waiting to join G", tag: "join:g"), prefs),
                       "2 personas esperan unirse a G")
    }

    func testAnUnansweredCallIsTranslatedInTheThread() {
        XCTAssertEqual(ChatText.shown("📞 Unanswered call"), "📞 " + L10n.text("chat_call_unanswered"))
        XCTAssertNotEqual(ChatText.shown("📞 Unanswered call"), "📞 Unanswered call")
        XCTAssertEqual(ChatText.outcomeLabel("Unanswered call"), L10n.text("chat_call_unanswered"))
    }
}
