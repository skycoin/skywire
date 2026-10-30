import XCTest

/// The native Chat tab against the real network, in the SkywireTour scheme
/// like FlowChecks: skychat comes up, the list draws, notes are written and
/// cleared, a conversation survives a tab switch, and with a peer the
/// messages go both ways and a notification lands in the conversation.
@MainActor
final class ChatChecks: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    /// Connected, the Chat tab draws the list once skychat answered behind its password gate.
    func testChatListComesUp() {
        let app = connectedApp()
        openTab(.chat, in: app)
        chatList(app)
        snap("chat-list")
    }

    /// A note written in Saved Messages, its sheet opened, then "Clear notes"
    /// asked both ways: Cancel keeps it, OK clears it. Then back to the list.
    func testNotesAreWrittenAndCleared() {
        let app = connectedApp()
        openTab(.chat, in: app)
        chatList(app).tap()
        let note = "note-" + UUID().uuidString.prefix(8).lowercased()
        send(note, in: app)
        let written = message(note, in: app)
        XCTAssertTrue(written.waitForExistence(timeout: 10), "the note never showed")
        snap("chat-note")

        written.tap()
        tapWhenUp("msg-copy", in: app)
        XCTAssertTrue(app.staticTexts["Copied."].waitForExistence(timeout: 5), "Copy said nothing")

        clearNotes(app, answer: "Cancel")
        XCTAssertTrue(written.exists, "Cancel cleared the notes anyway")
        clearNotes(app, answer: "OK")
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: written)
        XCTAssertEqual(XCTWaiter().wait(for: [gone], timeout: 10), .completed, "OK did not clear the notes")

        goBack(app)
        XCTAssertTrue(app.buttons["chat-row-saved"].waitForExistence(timeout: 10), "back did not return to the list")
    }

    /// Opening the Chat tab again after another one: the thread left open is still open.
    func testTabSwitchKeepsTheConversation() {
        let app = connectedApp()
        openTab(.chat, in: app)
        chatList(app).tap()
        let title = app.buttons["thread-title"]
        XCTAssertTrue(title.waitForExistence(timeout: 10), "the thread did not open")
        openTab(.home, in: app)
        openTab(.apps, in: app)
        openTab(.chat, in: app)
        XCTAssertTrue(title.waitForExistence(timeout: 5), "the tab came back on the list: the thread was dropped")
        XCTAssertFalse(app.buttons["chat-row-saved"].isHittable, "the chat list is on screen instead of the thread")
        goBack(app)
    }

    /// A group made on this phone: it opens, takes a post, shows the post's feed tick, and
    /// its settings page names it. Then the profile page publishes a name.
    func testGroupAndProfile() {
        let app = connectedApp()
        openTab(.chat, in: app)
        chatList(app)
        app.buttons["chat-start"].tap()
        tapMenuItem("New group", in: app)
        let name = "grp-" + UUID().uuidString.prefix(6).lowercased()
        let field = app.textFields["group-name"]
        XCTAssertTrue(field.waitForExistence(timeout: 5), "no group name field")
        field.tap()
        field.typeText(name)
        app.buttons["group-create"].tap()
        XCTAssertTrue(app.buttons["thread-title"].waitForExistence(timeout: 60), "the new group did not open")
        XCTAssertTrue(app.staticTexts[name].firstMatch.waitForExistence(timeout: 10), "the group's header does not name it")
        let post = "post-" + UUID().uuidString.prefix(8).lowercased()
        send(post, in: app)
        XCTAssertTrue(message(post, in: app).waitForExistence(timeout: 10), "the post never showed")
        let sent = app.descendants(matching: .any).matching(NSPredicate(format: "label == 'Sent'")).firstMatch
        XCTAssertTrue(sent.waitForExistence(timeout: 60), "the post never got its feed tick")
        snap("group-post")
        app.buttons["thread-title"].tap()
        XCTAssertTrue(app.staticTexts["Group settings"].waitForExistence(timeout: 10), "the settings page did not open")
        snap("group-settings")
        goBack(app)
        goBack(app)

        app.buttons["chat-me"].tap()
        let display = app.textFields["profile-name"]
        XCTAssertTrue(display.waitForExistence(timeout: 10), "the profile page has no name field")
        display.tap()
        display.typeText("Sim " + UUID().uuidString.prefix(4))
        app.buttons["profile-save"].tap()
        XCTAssertTrue(app.staticTexts["Profile published."].waitForExistence(timeout: 60), "the profile was not published")
        goBack(app)
    }

    /// A conversation with a second visor over the real network, both ways,
    /// and a notification from it tapped into the chat. The peer is a desktop
    /// core on the Mac (ios/scripts/desktop-peer.sh) whose skychat answers
    /// without a password on loopback and keeps its history; the runner is
    /// told about it through its environment, so this is skipped anywhere else:
    ///
    ///   TEST_RUNNER_SKYWIRE_PEER_PK=<pk> \
    ///   TEST_RUNNER_SKYWIRE_PEER_CHAT=http://127.0.0.1:18001 \
    ///   xcodebuild … -scheme SkywireTour test \
    ///     -only-testing:SkywireUITests/ChatChecks/testTwoWayConversationWithPeer
    ///
    /// The link opens Add by address (it shows the person and stops; Start chat
    /// is the user's), the route is set to DMSG, the Simulator writes first so
    /// the peer learns its key from its own history, the peer answers into the
    /// open conversation, then writes again while the app is away: the banner
    /// shows, and a tap on it lands in the conversation, from the Home tab too.
    func testTwoWayConversationWithPeer() async throws {
        let environment = ProcessInfo.processInfo.environment
        guard let peer = environment["SKYWIRE_PEER_PK"], let chat = environment["SKYWIRE_PEER_CHAT"].flatMap(URL.init(string:)) else {
            throw XCTSkip("no peer: set TEST_RUNNER_SKYWIRE_PEER_PK and TEST_RUNNER_SKYWIRE_PEER_CHAT")
        }
        let app = connectedApp()
        app.open(URL(string: "skychat://\(peer)")!)
        let start = app.buttons["address-start"]
        // app.open relaunches the app and its core.
        XCTAssertTrue(start.waitForExistence(timeout: 180), "the link never filled Add by address")
        snap("peer-link")
        start.tap()

        // Over DMSG, the direct path: a Skynet route between two visors that
        // share no transport waits on route setup.
        app.buttons["thread-menu"].tap()
        tapMenuItem("Send over", in: app, opens: "DMSG")
        tapMenuItem("DMSG", in: app)

        let mine = "sim-" + UUID().uuidString.prefix(8).lowercased()
        send(mine, in: app)
        let me = try await poll("the peer never received \(mine)") { () async throws -> String? in
            for pk in try await self.peerHistoryPeers(chat) where try await self.peerHistory(chat, pk).contains(mine) {
                return pk
            }
            let retry = app.buttons["Retry"].firstMatch
            if retry.exists { retry.tap() }
            return nil
        }
        let theirs = "peer-" + UUID().uuidString.prefix(8).lowercased()
        try await peerSend(chat, to: me, theirs)
        XCTAssertTrue(message(theirs, in: app).waitForExistence(timeout: 180), "the peer's answer never showed")
        snap("peer-two-way")

        // Away from the app: the conversation is no longer on screen, so
        // skychat notifies, and the banner comes from the app's bridge. Sent
        // inside the app's background grace.
        XCUIDevice.shared.press(.home)
        try await Task.sleep(for: .seconds(3))
        let away = "away-" + UUID().uuidString.prefix(8).lowercased()
        try await peerSend(chat, to: me, away)
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let banner = springboard.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", away)).firstMatch
        XCTAssertTrue(banner.waitForExistence(timeout: 180), "no notification for \(away)")
        snap("peer-notification")
        banner.tap()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 10), "the tap did not bring the app back")
        XCTAssertTrue(message(away, in: app).waitForExistence(timeout: 30), "the tap did not land in the conversation")
        snap("peer-tapped")

        // The same from another tab: the tap switches to Chat and opens the conversation, not the list.
        goBack(app)
        openTab(.home, in: app)
        XCUIDevice.shared.press(.home)
        try await Task.sleep(for: .seconds(3))
        let fromHome = "home-" + UUID().uuidString.prefix(8).lowercased()
        try await peerSend(chat, to: me, fromHome)
        let second = springboard.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", fromHome)).firstMatch
        XCTAssertTrue(second.waitForExistence(timeout: 60), "no notification for \(fromHome)")
        second.tap()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 10))
        XCTAssertTrue(message(fromHome, in: app).waitForExistence(timeout: 30), "the tap from the Home tab did not land in the conversation")
        XCTAssertTrue(app.buttons["thread-title"].exists, "the chat list is on screen, not the conversation")
        snap("peer-tapped-from-home")
    }

    /// Media and the message actions with the same peer (see testTwoWayConversationWithPeer for
    /// the environment). The peer sends a picture and a voice message recorded the way the desktop
    /// page records one (Opus in WebM, which only WebKit decodes on iOS; silent, so nothing sounds
    /// on the Mac); the picture opens full screen and the clip plays. Then a reply reaches the
    /// peer with its quote, a message is forwarded to Saved Messages, and one is deleted for both.
    func testMediaAndActionsWithPeer() async throws {
        let environment = ProcessInfo.processInfo.environment
        guard let peer = environment["SKYWIRE_PEER_PK"], let chat = environment["SKYWIRE_PEER_CHAT"].flatMap(URL.init(string:)),
              let voice = environment["SKYWIRE_PEER_VOICE"].map(URL.init(fileURLWithPath:)),
              let picture = environment["SKYWIRE_PEER_PICTURE"].map(URL.init(fileURLWithPath:))
        else {
            throw XCTSkip("no peer: set TEST_RUNNER_SKYWIRE_PEER_PK, _PEER_CHAT, _PEER_VOICE (a .weba) and _PEER_PICTURE (a .png)")
        }
        let app = connectedApp()
        app.open(URL(string: "skychat://\(peer)")!)
        let start = app.buttons["address-start"]
        XCTAssertTrue(start.waitForExistence(timeout: 180), "the link never filled Add by address")
        start.tap()
        app.buttons["thread-menu"].tap()
        tapMenuItem("Send over", in: app, opens: "DMSG")
        tapMenuItem("DMSG", in: app)

        let hello = "hello-" + UUID().uuidString.prefix(8).lowercased()
        send(hello, in: app)
        let me = try await poll("the peer never received \(hello)") { () async throws -> String? in
            for pk in try await self.peerHistoryPeers(chat) where try await self.peerHistory(chat, pk).contains(hello) {
                return pk
            }
            return nil
        }

        // A picture from the peer: its bubble, then the viewer.
        let pictureName = "pic-" + UUID().uuidString.prefix(6).lowercased() + ".png"
        try await peerSendFile(chat, to: me, picture, name: pictureName, type: "image/png")
        let pictureBubble = app.buttons[pictureName].firstMatch
        XCTAssertTrue(pictureBubble.waitForExistence(timeout: 120), "the picture never arrived")
        pictureBubble.tap()
        XCTAssertTrue(app.buttons["viewer-close"].waitForExistence(timeout: 10), "the picture did not open")
        snap("peer-picture")
        app.buttons["viewer-close"].tap()

        // A WebM voice message: it plays (its button turns to Pause).
        let voiceName = "voice-\(Self.stamp()).weba"
        try await peerSendFile(chat, to: me, voice, name: voiceName, type: "audio/webm")
        let play = app.buttons.matching(NSPredicate(format: "label == 'Play'")).element(boundBy: 0)
        XCTAssertTrue(play.waitForExistence(timeout: 120), "the voice message never arrived")
        play.tap()
        XCTAssertTrue(app.buttons["Pause"].firstMatch.waitForExistence(timeout: 15), "the WebM voice message did not play")
        snap("peer-voice-playing")

        // A reply carries its quote to the peer.
        let theirs = "quote-" + UUID().uuidString.prefix(8).lowercased()
        try await peerSend(chat, to: me, theirs)
        let quoted = message(theirs, in: app)
        XCTAssertTrue(quoted.waitForExistence(timeout: 120), "the peer's message never showed")
        quoted.tap()
        tapWhenUp("msg-reply", in: app)
        let answer = "answer-" + UUID().uuidString.prefix(8).lowercased()
        send(answer, in: app)
        _ = try await poll("the reply never reached the peer with its quote") { () async throws -> Bool? in
            let history = try await self.peerHistory(chat, me)
            return history.contains(answer) && history.contains(theirs) && history.contains("reply_to_preview") ? true : nil
        }

        // Delete for everyone: gone here and on the peer.
        message(answer, in: app).tap()
        tapWhenUp("msg-delete-all", in: app)
        _ = try await poll("the peer still holds the deleted reply") { () async throws -> Bool? in
            try await self.peerHistory(chat, me).contains(answer) ? nil : true
        }

        // Forward the peer's message to Saved Messages.
        quoted.tap()
        tapWhenUp("msg-forward", in: app)
        let saved = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Saved Messages'")).firstMatch
        XCTAssertTrue(saved.waitForExistence(timeout: 5), "the forward list has no Saved Messages")
        saved.tap()
        XCTAssertTrue(message(theirs, in: app).waitForExistence(timeout: 10), "the forward did not land in Saved Messages")
        snap("forwarded-to-saved")
    }

    private static func stamp() -> String {
        let formatter = DateFormatter()
        formatter.dateFormat = "yyyyMMdd-HHmmss"
        return formatter.string(from: Date())
    }

    // MARK: Steps

    /// The list, once skychat answered: its Saved Messages row.
    @discardableResult
    private func chatList(_ app: XCUIApplication) -> XCUIElement {
        let saved = app.buttons["chat-row-saved"]
        XCTAssertTrue(saved.waitForExistence(timeout: 180), "the chat list never came up")
        return saved
    }

    /// Types `text` into the open thread's composer and sends it with the button.
    private func send(_ text: String, in app: XCUIApplication) {
        let field = app.textFields["composer-field"].exists ? app.textFields["composer-field"] : app.textViews["composer-field"]
        XCTAssertTrue(field.waitForExistence(timeout: 10), "the thread has no composer")
        field.tap()
        field.typeText(text)
        app.buttons["composer-send"].tap()
    }

    /// A message's text in the open thread.
    private func message(_ text: String, in app: XCUIApplication) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    /// The thread's menu, "Clear notes", and the confirmation answered with `answer`.
    private func clearNotes(_ app: XCUIApplication, answer: String) {
        app.buttons["thread-menu"].tap()
        tapMenuItem("Clear notes", in: app, opens: "Cancel")
        let question = app.staticTexts.containing(NSPredicate(format: "label BEGINSWITH 'Delete every saved note?'")).firstMatch
        XCTAssertTrue(question.waitForExistence(timeout: 5), "Clear notes asked nothing")
        tapWhenUp(answer, in: app)
        let dismissed = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: question)
        XCTAssertEqual(XCTWaiter().wait(for: [dismissed], timeout: 5), .completed)
    }

    /// A menu's row, tapped once the menu has settled; tapped again only when `opens`, what the
    /// tap should bring up, did not appear (a tap made while the menu was still opening is lost).
    private func tapMenuItem(_ label: String, in app: XCUIApplication, opens: String? = nil) {
        let item = app.buttons[label].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "no \(label) in the menu")
        for _ in 0..<3 {
            usleep(700_000)
            item.tap()
            guard let opens else { return }
            if app.descendants(matching: .any)[opens].firstMatch.waitForExistence(timeout: 3) { return }
        }
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

    /// The peer sends `file` to this visor as an attachment, once its link is up.
    private func peerSendFile(_ chat: URL, to pk: String, _ file: URL, name: String, type: String) async throws {
        var link = URLRequest(url: chat.appendingPathComponent("link"), timeoutInterval: 30)
        link.httpMethod = "POST"
        link.httpBody = try JSONSerialization.data(withJSONObject: ["pk": pk, "network": "dmsg"])
        _ = try? await URLSession.shared.data(for: link)
        let boundary = "b-" + UUID().uuidString
        var body = Data("--\(boundary)\r\nContent-Disposition: form-data; name=\"pk\"\r\n\r\n\(pk)\r\n".utf8)
        body += Data("--\(boundary)\r\nContent-Disposition: form-data; name=\"file\"; filename=\"\(name)\"\r\nContent-Type: \(type)\r\n\r\n".utf8)
        body += try Data(contentsOf: file)
        body += Data("\r\n--\(boundary)--\r\n".utf8)
        var request = URLRequest(url: chat.appendingPathComponent("send-file"), timeoutInterval: 120)
        request.httpMethod = "POST"
        request.setValue("multipart/form-data; boundary=\(boundary)", forHTTPHeaderField: "Content-Type")
        request.httpBody = body
        let (data, response) = try await URLSession.shared.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        XCTAssertTrue((200..<300).contains(status), "the peer could not send \(name) (\(status)): \(String(decoding: data, as: UTF8.self))")
    }

    /// Asks `check` every two seconds for up to five minutes: a new visor's
    /// first connection to another takes minutes.
    private func poll<T>(_ failure: String, _ check: () async throws -> T?) async throws -> T {
        for _ in 0..<150 {
            if let value = try await check() { return value }
            try await Task.sleep(for: .seconds(2))
        }
        XCTFail(failure)
        throw XCTSkip(failure)
    }

    private func snap(_ name: String) {
        usleep(700_000)
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
