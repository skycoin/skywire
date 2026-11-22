import XCTest

/// Skymail end to end, in the SkywireTour scheme. Mail to the phone's own
/// address is delivered locally, so no peer is needed:
///
///   -only-testing:SkywireUITests/MailChecks
@MainActor
final class MailChecks: XCTestCase {
    /// Its dialogs are found by their English labels, whatever the app was left set to.
    static let english = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]

    override func setUp() {
        continueAfterFailure = false
    }

    /// Write to yourself, read it, reply, delete it.
    func testSendReadReplyDelete() {
        let app = connectedApp(arguments: Self.english)
        let address = openMail(app)
        let subject = "UI check \(Int(Date().timeIntervalSince1970))"
        send(app, to: address, subject: subject, body: "Hello from MailChecks.")

        let row = mailRow(app, subject)
        XCTAssertTrue(row.waitForExistence(timeout: 30), "the message never reached the inbox")
        snap("mail-inbox")
        row.tap()
        let text = app.staticTexts["mail-text"]
        XCTAssertTrue(text.waitForExistence(timeout: 15), "the message did not open")
        XCTAssertTrue(text.label.hasPrefix("Hello from MailChecks."), "body read \(text.label)")
        // Sent by this visor to itself: the sender is proved.
        XCTAssertEqual(app.descendants(matching: .any)["mail-verified"].value as? String, "verified")
        snap("mail-message")

        app.buttons["mail-reply"].tap()
        let subjectField = app.textFields["mail-subject"]
        XCTAssertTrue(subjectField.waitForExistence(timeout: 5), "reply opened no draft")
        XCTAssertEqual(subjectField.value as? String, "Re: \(subject)")
        snap("mail-reply")
        goBack(app)

        row.tap()
        XCTAssertTrue(app.buttons["mail-delete"].waitForExistence(timeout: 10))
        app.buttons["mail-delete"].tap()
        tapWhenUp("Delete", in: app)
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: row)
        XCTAssertEqual(XCTWaiter().wait(for: [gone], timeout: 20), .completed, "the deleted message is still listed")
    }

    /// A long press starts a batch; select all, invert and deselect move it, and the
    /// batch delete asks first and then removes every picked message.
    func testBatchSelectAndDelete() {
        let app = connectedApp(arguments: Self.english)
        let address = openMail(app)
        let stamp = Int(Date().timeIntervalSince1970)
        let first = "Batch A \(stamp)", second = "Batch B \(stamp)"
        send(app, to: address, subject: first, body: "One.")
        send(app, to: address, subject: second, body: "Two.")
        let rowA = mailRow(app, first), rowB = mailRow(app, second)
        XCTAssertTrue(rowA.waitForExistence(timeout: 30) && rowB.waitForExistence(timeout: 30), "the messages never arrived")
        let listed = app.buttons.matching(identifier: "mail-row").count

        rowA.press(forDuration: 1.0)
        XCTAssertTrue(app.staticTexts["1 selected"].waitForExistence(timeout: 5), "a long press picked nothing")
        XCTAssertFalse(app.buttons["mail-write"].exists, "Write stays up during a batch")
        app.buttons["mail-invert"].tap()
        XCTAssertTrue(app.staticTexts["\(listed - 1) selected"].waitForExistence(timeout: 5), "invert did not flip the picks")
        app.buttons["mail-select-all"].tap()
        XCTAssertTrue(app.staticTexts["\(listed) selected"].waitForExistence(timeout: 5), "select all left some out")
        app.buttons["mail-deselect-all"].tap()
        XCTAssertTrue(app.buttons["mail-write"].waitForExistence(timeout: 5), "deselect all did not end the batch")

        rowA.press(forDuration: 1.0)
        rowB.tap()
        XCTAssertTrue(app.staticTexts["2 selected"].waitForExistence(timeout: 5), "a tap during a batch did not pick")
        snap("mail-batch")
        app.buttons["mail-delete-selected"].tap()
        tapWhenUp("Delete", in: app)
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: rowA)
        XCTAssertEqual(XCTWaiter().wait(for: [gone], timeout: 20), .completed, "the batch was not deleted")
        XCTAssertFalse(rowB.exists, "the second picked message is still listed")
        XCTAssertTrue(app.buttons["mail-write"].waitForExistence(timeout: 5), "the batch did not end after the delete")
    }

    /// Save contact on a message names its sender in the lists; the contacts screen edits and
    /// deletes it, and the composer's picker offers it.
    func testContactsSaveEditPickDelete() {
        let app = connectedApp(arguments: Self.english)
        let address = openMail(app)
        let stamp = Int(Date().timeIntervalSince1970)
        let subject = "Contact \(stamp)", name = "Me \(stamp)"
        send(app, to: address, subject: subject, body: "Save me.")
        let row = mailRow(app, subject)
        XCTAssertTrue(row.waitForExistence(timeout: 30), "the message never arrived")
        row.tap()
        let save = app.buttons["mail-save-contact"]
        XCTAssertTrue(save.waitForExistence(timeout: 15), "no Save contact on a verified message")
        snap("mail-message-buttons")
        save.tap()
        let nameField = app.textFields["mail-contact-name"]
        XCTAssertTrue(nameField.waitForExistence(timeout: 5), "no contact dialog")
        nameField.tap()
        nameField.typeText(name)
        tapWhenUp("Save", in: app)
        // The From line now reads the saved name, with the address as written under it.
        XCTAssertTrue(app.staticTexts[name].waitForExistence(timeout: 5), "the open message does not use the saved name")
        goBack(app)
        XCTAssertTrue(mailRow(app, name).waitForExistence(timeout: 5), "the inbox row does not use the saved name")

        app.buttons["mail-write"].tap()
        app.buttons["mail-pick-contact"].tap()
        XCTAssertTrue(app.buttons[name].waitForExistence(timeout: 5), "the picker does not offer the contact")
        app.buttons[name].tap()
        let to = app.textFields["mail-to"]
        XCTAssertEqual((to.value as? String)?.lowercased(), address.lowercased(), "the picked contact did not fill To")
        app.buttons["mail-pick-contact"].tap()
        app.buttons["Manage contacts"].tap()
        let contact = app.descendants(matching: .any).matching(identifier: "mail-contact")
            .matching(NSPredicate(format: "label CONTAINS %@", name)).firstMatch
        XCTAssertTrue(contact.waitForExistence(timeout: 5), "the contacts screen does not list it")
        snap("mail-contacts")

        app.buttons["mail-contact-edit"].firstMatch.tap()
        let edit = app.textFields["mail-contact-name"]
        XCTAssertTrue(edit.waitForExistence(timeout: 5))
        edit.tap()
        edit.typeText(" edited")
        tapWhenUp("Save", in: app)
        XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "mail-contact")
            .matching(NSPredicate(format: "label CONTAINS %@", "\(name) edited")).firstMatch.waitForExistence(timeout: 5),
            "the edit did not stick")
        app.buttons["mail-contact-delete"].firstMatch.tap()
        tapWhenUp("Delete", in: app)
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: contact)
        XCTAssertEqual(XCTWaiter().wait(for: [gone], timeout: 5), .completed, "the contact was not deleted")
    }

    /// Settings copies the book into Contacts: a contact saved in Skymail shows there, its
    /// SkyMail row opens a draft back in Skywire, and turning the copy off removes it. Grant
    /// access first, or the system asks:
    ///   xcrun simctl privacy booted grant contacts com.skycoin.skywire
    func testTheBookReachesThePhoneContacts() {
        let app = connectedApp(arguments: Self.english)
        openTab(.settings, in: app)
        let toggle = app.switches["settings-phone-contacts"]
        scroll(to: toggle, in: app)
        if toggle.value as? String == "1" { toggle.tap() }
        XCTAssertTrue(waitValue(toggle, "0", timeout: 10))
        toggle.tap()
        XCTAssertTrue(waitValue(toggle, "1", timeout: 15), "the copy did not turn on")
        snap("settings-phone-contacts")

        let address = openMail(app)
        let stamp = Int(Date().timeIntervalSince1970)
        let subject = "Book \(stamp)", name = "Book \(stamp)"
        send(app, to: address, subject: subject, body: "Copy me.")
        let row = mailRow(app, subject)
        XCTAssertTrue(row.waitForExistence(timeout: 30), "the message never arrived")
        row.tap()
        app.buttons["mail-save-contact"].tap()
        let nameField = app.textFields["mail-contact-name"]
        XCTAssertTrue(nameField.waitForExistence(timeout: 5), "no contact dialog")
        nameField.tap()
        nameField.typeText(name)
        tapWhenUp("Save", in: app)
        XCTAssertTrue(app.staticTexts[name].waitForExistence(timeout: 10), "the contact was not saved")
        goBack(app)

        let contacts = XCUIApplication(bundleIdentifier: "com.apple.MobileAddressBook")
        contacts.launch()
        let entry = contacts.staticTexts[name].firstMatch
        XCTAssertTrue(entry.waitForExistence(timeout: 15), "Contacts does not list the copy")
        entry.tap()
        let link = contacts.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'skymail:'")).firstMatch
        XCTAssertTrue(link.waitForExistence(timeout: 5), "the copy has no SkyMail row")
        snap("contacts-copy")
        link.tap()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        if springboard.buttons["Open"].waitForExistence(timeout: 3) { springboard.buttons["Open"].tap() }
        let to = app.textFields["mail-to"]
        XCTAssertTrue(to.waitForExistence(timeout: 15), "the SkyMail row opened no draft")
        XCTAssertEqual((to.value as? String)?.lowercased(), address.lowercased(), "the draft is not to the contact")
        snap("contacts-link-draft")
        goBack(app)

        openTab(.settings, in: app)
        scroll(to: toggle, in: app)
        toggle.tap()
        XCTAssertTrue(waitValue(toggle, "0", timeout: 15), "the copy did not turn off")
        contacts.launch()
        XCTAssertFalse(contacts.staticTexts[name].waitForExistence(timeout: 5), "the copy outlived the switch")
    }

    /// The settings switch turns the mailbox off and on again.
    func testMailboxTurnsOffAndOn() {
        let app = connectedApp(arguments: Self.english)
        _ = openMail(app)
        app.buttons["mail-settings"].tap()
        let toggle = app.switches["mail-enabled"]
        XCTAssertTrue(toggle.waitForExistence(timeout: 10))
        XCTAssertEqual(toggle.value as? String, "1")
        toggle.tap()
        XCTAssertTrue(waitValue(toggle, "0", timeout: 15), "the mailbox did not turn off")
        snap("mail-off")
        toggle.tap()
        XCTAssertTrue(waitValue(toggle, "1", timeout: 15), "the mailbox did not turn on")
        goBack(app)
        XCTAssertTrue(app.staticTexts["mail-address"].waitForExistence(timeout: 20), "the mailbox is not running again")
    }

    /// Mail both ways with another visor over dmsg, and the banner a received
    /// message raises opens it. Needs a desktop peer whose API needs no login
    /// (playbook 3.5), named to the runner:
    ///
    ///   TEST_RUNNER_SKYWIRE_MAIL_PEER=http://127.0.0.1:18000 \
    ///   TEST_RUNNER_SKYWIRE_MAIL_PEER_PK=<pk> … -only-testing:SkywireUITests/MailChecks/testExchangeWithAPeer
    func testExchangeWithAPeer() async throws {
        let env = ProcessInfo.processInfo.environment
        guard let origin = env["SKYWIRE_MAIL_PEER"].flatMap(URL.init(string:)), let peerPK = env["SKYWIRE_MAIL_PEER_PK"] else {
            throw XCTSkip("no peer: set TEST_RUNNER_SKYWIRE_MAIL_PEER and _PK")
        }
        let peer = MailPeer(origin: origin, pk: peerPK)
        let app = connectedApp(arguments: Self.english)
        let address = openMail(app)
        let stamp = Int(Date().timeIntervalSince1970)

        // The phone writes; the peer's inbox gets it.
        let out = "To the peer \(stamp)"
        send(app, to: try await peer.address(), subject: out, body: "From the phone.")
        snap("mail-sent-to-peer")
        let arrived = try await peer.waitForInbox(subject: out, timeout: 30)
        XCTAssertTrue(arrived, "the peer never received the phone's mail")

        // The peer writes; the phone shows a banner, and the tap opens the message.
        goBack(app)
        let back = "From the peer \(stamp)"
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let banner = springboard.descendants(matching: .any)
            .matching(NSPredicate(format: "label CONTAINS %@", back)).firstMatch
        // Skymail delivers at once and never queues (pkg/skymail/send.go), and
        // the dmsg path from the peer to the phone can fail for a while (G8:
        // "dmsg error 202"), so the peer sends again while no banner came.
        var sends = 0
        repeat {
            sends += 1
            await peer.trySend(to: address, subject: "\(back) #\(sends)", body: "Opened from the banner.")
        } while !banner.waitForExistence(timeout: 30) && sends < 4
        XCTAssertTrue(banner.exists, "no banner for the peer's mail after \(sends) sends")
        snap("mail-banner")
        banner.tap()
        let text = app.staticTexts["mail-text"]
        XCTAssertTrue(text.waitForExistence(timeout: 20), "the banner did not open the message")
        XCTAssertTrue(text.label.hasPrefix("Opened from the banner."))
        XCTAssertEqual(app.descendants(matching: .any)["mail-verified"].value as? String, "verified")
        snap("mail-from-banner")
    }

    /// Opens Skymail from the hub, turns the mailbox on if an earlier run left
    /// it off, and returns this phone's address.
    private func openMail(_ app: XCUIApplication) -> String {
        openTab(.apps, in: app)
        let tile = app.buttons["hub-mail"]
        scroll(to: tile, in: app)
        tile.tap()
        let address = app.staticTexts["mail-address"]
        let turnOn = app.buttons["mail-turn-on"]
        let either = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == true"), object: address)
        if XCTWaiter().wait(for: [either], timeout: 20) != .completed, turnOn.exists {
            turnOn.tap()
        }
        XCTAssertTrue(address.waitForExistence(timeout: 20), "no address: the mailbox is not running")
        // The address is drawn with a break opportunity between characters.
        let text = address.label.replacingOccurrences(of: "\u{200B}", with: "")
        XCTAssertTrue(text.hasSuffix(".dmsg"), "the address is \(text)")
        return text
    }

    private func send(_ app: XCUIApplication, to address: String, subject: String, body: String) {
        app.buttons["mail-write"].tap()
        let to = app.textFields["mail-to"]
        XCTAssertTrue(to.waitForExistence(timeout: 5), "no draft")
        to.tap()
        to.typeText(address)
        let subjectField = app.textFields["mail-subject"]
        subjectField.tap()
        subjectField.typeText(subject)
        let bodyField = app.descendants(matching: .any)["mail-body"].firstMatch
        bodyField.tap()
        bodyField.typeText(body)
        let sendButton = app.buttons["mail-send"]
        // Under the on-screen keyboard the screen's middle is the Message box.
        scroll(to: sendButton, in: app, from: subjectField)
        sendButton.tap()
        // A delivered send closes the draft.
        XCTAssertTrue(app.buttons["mail-write"].waitForExistence(timeout: 60), "the draft did not close: not delivered")
    }

    private func mailRow(_ app: XCUIApplication, _ subject: String) -> XCUIElement {
        app.buttons.matching(identifier: "mail-row").matching(NSPredicate(format: "label CONTAINS %@", subject)).firstMatch
    }

    private func waitValue(_ element: XCUIElement, _ value: String, timeout: TimeInterval) -> Bool {
        let expectation = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value == %@", value), object: element)
        return XCTWaiter().wait(for: [expectation], timeout: timeout) == .completed
    }

    private func snap(_ name: String) {
        usleep(700_000)
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}

/// A desktop peer's mail routes, called from the test runner, which shares the Mac's network.
private struct MailPeer {
    let origin: URL
    let pk: String

    func address() async throws -> String {
        let status = try await json("GET", "/api/visors/\(pk)/mail")
        return try XCTUnwrap(status["address_dmsg"] as? String)
    }

    /// One send, its outcome in the test log. The peer's API drops a send
    /// slower than its 10 s WriteTimeout even when the mail arrives, so the
    /// phone's banner, not this answer, says whether it was delivered.
    func trySend(to: String, subject: String, body: String) async {
        do {
            let result = try await json("POST", "/api/visors/\(pk)/mail/send",
                                        body: ["to": [to], "subject": subject, "body": body])
            print("peer send \"\(subject)\": \(result["error"].map { "\($0)" } ?? "delivered")")
        } catch {
            print("peer send \"\(subject)\": no answer, \(error.localizedDescription)")
        }
    }

    func waitForInbox(subject: String, timeout: TimeInterval) async throws -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            let request = URLRequest(url: origin.appendingPathComponent("/api/visors/\(pk)/mail/INBOX"))
            let (data, _) = try await URLSession.shared.data(for: request)
            let list = (try? JSONSerialization.jsonObject(with: data)) as? [[String: Any]] ?? []
            if list.contains(where: { $0["subject"] as? String == subject }) { return true }
            try await Task.sleep(for: .seconds(1))
        }
        return false
    }

    private func json(_ method: String, _ path: String, body: [String: Any]? = nil) async throws -> [String: Any] {
        var request = URLRequest(url: origin.appendingPathComponent(path))
        request.httpMethod = method
        request.timeoutInterval = 95
        if let body {
            let (csrf, _) = try await URLSession.shared.data(from: origin.appendingPathComponent("/api/csrf"))
            let token = (try JSONSerialization.jsonObject(with: csrf) as? [String: Any])?["csrf_token"] as? String
            request.setValue(token, forHTTPHeaderField: "X-CSRF-Token")
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            request.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        let (data, _) = try await URLSession.shared.data(for: request)
        return (try JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:]
    }
}
