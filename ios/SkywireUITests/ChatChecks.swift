import XCTest

/// The Chat tab against the real network (M3), in the SkywireTour scheme like
/// FlowChecks: skychat comes up, the page loads behind its password gate, and
/// the page's JavaScript dialogs are answered.
@MainActor
final class ChatChecks: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    /// Connected, the Chat tab shows skychat's page: its own buttons are in
    /// the web view, which they can only be once the gate let the page in.
    func testChatPageComesUp() {
        let app = connectedApp()
        openTab(.chat, in: app)
        let page = chatPage(app)
        keep(page.debugDescription, named: "chat-page-tree")
        snap("chat-page")
    }

    /// A video message recorded in the page: a tap on the empty composer's
    /// button switches it from voice to video, a hold records (the page's own
    /// gesture), and the take lands in the thread. On the Simulator WebKit
    /// records its mock camera and microphone (a test pattern and a beep);
    /// the real devices are Lane D's.
    func testVideoMessageRecords() {
        let app = connectedApp()
        openTab(.chat, in: app)
        let page = chatPage(app)
        page.staticTexts["Saved Messages"].firstMatch.tap()
        let voice = page.buttons["Voice message"].firstMatch
        let video = page.buttons["Video message"].firstMatch
        XCTAssertTrue(voice.waitForExistence(timeout: 10) || video.exists, "no record button")
        if voice.exists { voice.tap() }
        XCTAssertTrue(video.waitForExistence(timeout: 5), "the tap did not switch to video")
        snap("video-mode")
        video.press(forDuration: 3.5)
        sleep(4)
        snap("video-after-take")
        keep(page.debugDescription, named: "video-thread-tree")
    }

    /// Leaving the Chat tab and coming back keeps the page where it was: a
    /// thread left open is still open (G3 re-review: the page was torn down
    /// and loaded again on every return, back on the chat list).
    func testTabSwitchKeepsTheConversation() {
        let app = connectedApp()
        openTab(.chat, in: app)
        let page = chatPage(app)
        page.staticTexts["Saved Messages"].firstMatch.tap()
        let back = page.buttons["Back to chats"].firstMatch
        XCTAssertTrue(back.waitForExistence(timeout: 10), "the thread did not open")
        // The page focuses its composer when a thread opens, and the keyboard
        // covers the tab bar: put it away as a person would first.
        let done = app.buttons["Done"].firstMatch
        if done.waitForExistence(timeout: 3) {
            done.tap()
        } else {
            page.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3)).tap()
        }
        openTab(.home, in: app)
        openTab(.apps, in: app)
        openTab(.chat, in: app)
        XCTAssertTrue(back.waitForExistence(timeout: 5), "the page came back on the list: the thread was dropped")
        XCTAssertFalse(page.buttons["All"].isHittable, "the chat list is on screen instead of the thread")
        back.tap()
    }

    /// The page stays at its own scale: focusing the composer (14 px text,
    /// which WebKit zooms into), a pinch and a double tap leave the thread's
    /// header on screen and the composer inside the screen's width. Zoomed, the
    /// conversation was magnified and scrolled sideways, the other side's
    /// bubbles off the left edge (seen on the bench, 2026-09-30).
    func testThePageKeepsItsScale() {
        let app = connectedApp()
        openTab(.chat, in: app)
        let page = chatPage(app)
        page.staticTexts["Saved Messages"].firstMatch.tap()
        let composer = page.textViews.firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 10))
        composer.tap()
        composer.typeText("z")
        page.pinch(withScale: 3, velocity: 2)
        page.doubleTap()
        usleep(800_000)
        snap("chat-scale")
        let screen = app.windows.firstMatch.frame
        XCTAssertTrue(page.buttons["Back to chats"].firstMatch.isHittable, "the thread's header left the screen")
        XCTAssertGreaterThanOrEqual(composer.frame.minX, screen.minX, "the composer starts off the left edge")
        XCTAssertLessThanOrEqual(composer.frame.maxX, screen.maxX, "the composer runs off the right edge")
        // The double tap took the focus: back into the field to take the
        // draft out again.
        composer.tap()
        composer.typeText(XCUIKeyboardKey.delete.rawValue)
        page.buttons["Back to chats"].firstMatch.tap()
    }

    /// The page's `confirm()` is answered, both ways, through the app's
    /// alert: Saved Messages' "Clear notes" asks first. Cancel keeps a note
    /// just written, OK clears it, and in between the page still takes taps
    /// (an unanswered dialog would have left it frozen, a missing delegate
    /// would have answered no without asking). Then back to the list.
    func testJavaScriptConfirmIsAnswered() {
        let app = connectedApp()
        openTab(.chat, in: app)
        let page = chatPage(app)

        page.staticTexts["Saved Messages"].firstMatch.tap()
        let composer = page.textViews.firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 10), "the notes thread has no composer")
        composer.tap()
        // One key at a time, checked, and sent with the page's own button:
        // synthesized typing into a web text area drops keys when it runs
        // ahead of the page, and a word the keyboard autocorrects on Return
        // would no longer be the one to look for.
        let note = "note-" + UUID().uuidString.prefix(8).lowercased()
        for key in note {
            composer.typeText(String(key))
        }
        let typed = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", note), object: composer)
        XCTAssertEqual(XCTWaiter().wait(for: [typed], timeout: 5), .completed, "the composer holds \(composer.value ?? "nothing"), not \(note)")
        page.buttons["Send"].firstMatch.tap()
        let written = page.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", note)).firstMatch
        XCTAssertTrue(written.waitForExistence(timeout: 10), "the note never showed")

        clearNotes(in: page, app, answer: "Cancel")
        XCTAssertTrue(written.exists, "Cancel cleared the notes anyway")

        clearNotes(in: page, app, answer: "OK")
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: written)
        XCTAssertEqual(XCTWaiter().wait(for: [gone], timeout: 10), .completed, "OK did not clear the notes")

        // Out of the thread with the page's own back, which steps its history.
        // (The web view's edge swipe does the same for a finger; a synthetic
        // drag under XCUITest does not register as an edge pan.)
        page.buttons["Back to chats"].firstMatch.tap()
        XCTAssertTrue(page.buttons["All"].waitForExistence(timeout: 10), "back did not return to the list")
    }

    /// The thread's menu, "Clear notes", and the page's confirm answered with
    /// `answer`.
    private func clearNotes(in page: XCUIElement, _ app: XCUIApplication, answer: String) {
        // The page exposes its menu button as a plain element, not a button.
        page.descendants(matching: .any)["Chat actions"].firstMatch.tap()
        let row = page.descendants(matching: .any)["Clear notes"].firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 5), "the thread's menu has no Clear notes")
        row.tap()
        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 5), "the page's confirm() raised no alert")
        XCTAssertTrue(alert.staticTexts.containing(NSPredicate(format: "label BEGINSWITH 'Delete every saved note?'")).firstMatch.exists,
                      "the alert is not the page's question")
        alert.buttons[answer].tap()
        let dismissed = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: alert)
        XCTAssertEqual(XCTWaiter().wait(for: [dismissed], timeout: 5), .completed)
    }

    /// A conversation with a second visor over the real network, both ways,
    /// and a notification from it tapped into the chat (items 3.3-3.5). The
    /// peer is a desktop core on the Mac (playbook §5, peers) whose skychat
    /// answers without a password on loopback; the runner is told about it
    /// through its environment, so this is skipped anywhere else (CI):
    ///
    ///   TEST_RUNNER_SKYWIRE_PEER_PK=<pk> \
    ///   TEST_RUNNER_SKYWIRE_PEER_CHAT=http://127.0.0.1:18001 \
    ///   xcodebuild … -scheme SkywireTour test \
    ///     -only-testing:SkywireUITests/ChatChecks/testTwoWayConversationWithPeer
    ///
    /// The link opens the page's Add-by-address dialog (the page shows it and
    /// stops; Start Chat is the user's), the Simulator writes first so the
    /// peer learns its key from its own history, the peer answers into the
    /// open conversation, then writes again while the app is away: the banner
    /// shows, and a tap on it lands in the conversation.
    func testTwoWayConversationWithPeer() async throws {
        let environment = ProcessInfo.processInfo.environment
        guard let peer = environment["SKYWIRE_PEER_PK"], let chat = environment["SKYWIRE_PEER_CHAT"].flatMap(URL.init(string:)) else {
            throw XCTSkip("no peer: set TEST_RUNNER_SKYWIRE_PEER_PK and TEST_RUNNER_SKYWIRE_PEER_CHAT")
        }
        let app = connectedApp()
        app.open(URL(string: "skychat://\(peer)")!)
        let page = chatPage(app)
        let start = page.buttons.matching(NSPredicate(format: "label IN %@", ["Start Chat", "Open Chat"])).firstMatch
        XCTAssertTrue(start.waitForExistence(timeout: 60), "the link never filled Add by address")
        snap("peer-link")
        start.tap()

        // Over DMSG, the direct path: a Skynet route between two visors that
        // share no transport waits on route setup (it had not come up in 90 s
        // on the bench, the message queued as "establishing connection").
        // A <select>: labelled by its title, valued by the chosen option.
        let network = page.descendants(matching: .any)
            .matching(NSPredicate(format: "label == 'Network type' OR value == 'Skynet'")).firstMatch
        XCTAssertTrue(network.waitForExistence(timeout: 10), "no network selector")
        network.tap()
        let dmsg = app.descendants(matching: .any)["DMSG"].firstMatch
        XCTAssertTrue(dmsg.waitForExistence(timeout: 5), "the selector offered no DMSG")
        dmsg.tap()

        let mine = "sim-" + UUID().uuidString.prefix(8).lowercased()
        let composer = page.textViews.firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 10))
        composer.tap()
        for key in mine {
            composer.typeText(String(key))
        }
        page.buttons["Send"].firstMatch.tap()

        // The peer learns this visor's key from its own history. skychat
        // holds a new conversation's messages until its link to the peer is
        // up, and that link was dialled over Skynet when the conversation
        // opened (choosing DMSG while it runs changes nothing). Where no
        // Skynet route comes up it fails after 45 s and the page offers
        // Retry, which dials over the network now chosen, DMSG.
        let retry = page.buttons["Retry"].firstMatch
        let me = try await poll("the peer never received \(mine)") { () async throws -> String? in
            for pk in try await self.peerHistoryPeers(chat) where try await self.peerHistory(chat, pk).contains(mine) {
                return pk
            }
            if retry.exists { retry.tap() }
            return nil
        }
        let theirs = "peer-" + UUID().uuidString.prefix(8).lowercased()
        try await peerSend(chat, to: me, theirs)
        XCTAssertTrue(page.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", theirs)).firstMatch.waitForExistence(timeout: 180),
                      "the peer's answer never showed")
        snap("peer-two-way")

        // Away from the app: the conversation is no longer on screen, so
        // skychat notifies, and the banner comes from the app's bridge. Sent
        // at once, inside the app's background grace (about 30 s; after it
        // iOS suspends the app and its core, which is Lane D's to cover).
        XCUIDevice.shared.press(.home)
        // A moment away first, as a person would be: the app tells skychat
        // nothing is on screen as it leaves (the frozen page cannot), and a
        // message racing that by milliseconds is still "on screen" to skychat.
        try await Task.sleep(for: .seconds(3))
        let away = "away-" + UUID().uuidString.prefix(8).lowercased()
        try await peerSend(chat, to: me, away)
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let banner = springboard.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", away)).firstMatch
        XCTAssertTrue(banner.waitForExistence(timeout: 180), "no notification for \(away)")
        snap("peer-notification")
        banner.tap()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 10), "the tap did not bring the app back")
        XCTAssertTrue(page.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", away)).firstMatch.waitForExistence(timeout: 30),
                      "the tap did not land in the conversation")
        snap("peer-tapped")

        // The same from another tab (the re-review's repro): the tap switches
        // to Chat and the page opens the conversation, not the list.
        openTab(.home, in: app)
        XCUIDevice.shared.press(.home)
        try await Task.sleep(for: .seconds(3))
        let fromHome = "home-" + UUID().uuidString.prefix(8).lowercased()
        try await peerSend(chat, to: me, fromHome)
        let second = springboard.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", fromHome)).firstMatch
        XCTAssertTrue(second.waitForExistence(timeout: 60), "no notification for \(fromHome)")
        second.tap()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 10))
        XCTAssertTrue(page.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", fromHome)).firstMatch.waitForExistence(timeout: 30),
                      "the tap from the Home tab did not land in the conversation")
        XCTAssertTrue(page.buttons["Back to chats"].firstMatch.exists, "the chat list is on screen, not the conversation")
        snap("peer-tapped-from-home")
    }

    // MARK: The peer's skychat (plain HTTP on loopback, no password)

    private func peerHistoryPeers(_ chat: URL) async throws -> [String] {
        let (data, _) = try await URLSession.shared.data(from: chat.appendingPathComponent("history/peers"))
        return (try? JSONDecoder().decode([String].self, from: data)) ?? []
    }

    private func peerHistory(_ chat: URL, _ pk: String) async throws -> String {
        var components = URLComponents(url: chat.appendingPathComponent("history"), resolvingAgainstBaseURL: false)!
        components.queryItems = [URLQueryItem(name: "peer", value: pk)]
        let (data, _) = try await URLSession.shared.data(from: components.url!)
        return String(decoding: data, as: UTF8.self)
    }

    private func peerSend(_ chat: URL, to pk: String, _ text: String) async throws {
        var request = URLRequest(url: chat.appendingPathComponent("message"), timeoutInterval: 90)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: ["recipient": pk, "message": text])
        let (data, response) = try await URLSession.shared.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        XCTAssertTrue((200..<300).contains(status), "the peer could not send (\(status)): \(String(decoding: data, as: UTF8.self))")
    }

    /// Asks `check` every two seconds for up to five minutes: every
    /// `xcodebuild test` install is a new identity, and a new visor's first
    /// connection to another takes minutes (the page says so; the desktop peer
    /// took five to attach to a dmsg relay).
    private func poll<T>(_ failure: String, _ check: () async throws -> T?) async throws -> T {
        for _ in 0..<150 {
            if let value = try await check() { return value }
            try await Task.sleep(for: .seconds(2))
        }
        XCTFail(failure)
        throw XCTSkip(failure)
    }

    /// The page's web view, once it has drawn something.
    private func chatPage(_ app: XCUIApplication) -> XCUIElement {
        let page = app.webViews["chat-webview"]
        XCTAssertTrue(page.waitForExistence(timeout: 60), "the chat page never came up")
        XCTAssertTrue(page.buttons.firstMatch.waitForExistence(timeout: 30), "the chat page drew nothing")
        return page
    }

    private func keep(_ text: String, named name: String) {
        let attachment = XCTAttachment(string: text)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    private func snap(_ name: String) {
        usleep(700_000)
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
