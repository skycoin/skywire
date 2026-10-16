import XCTest

/// Calls with a desktop peer (ios/scripts/desktop-peer.sh), in the SkywireTour
/// scheme. The peer's API answers on the Mac's loopback without a login, and
/// is named to the runner:
///
///   TEST_RUNNER_SKYWIRE_CALL_PEER=http://127.0.0.1:18000 \
///   TEST_RUNNER_SKYWIRE_CALL_PEER_PK=<pk> … -only-testing:SkywireUITests/CallChecks
@MainActor
final class CallChecks: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    /// A call the chat page placed and the peer answered within the app's poll reaches the app as
    /// an id only. Its call screen then covered the page with a spinner and no Hang up (G8); now
    /// it stays out of the way, as Android's does. Whether the answer beats the poll is a race.
    func testACallThePagePlacedCanBeHungUp() async throws {
        let env = ProcessInfo.processInfo.environment
        guard let origin = env["SKYWIRE_CALL_PEER"].flatMap(URL.init(string:)), let peerPK = env["SKYWIRE_CALL_PEER_PK"] else {
            throw XCTSkip("no peer: set TEST_RUNNER_SKYWIRE_CALL_PEER and _PK")
        }
        let peer = CallPeer(origin: origin, pk: peerPK)
        let app = connectedApp(arguments: ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"])
        app.open(URL(string: "skychat://\(peerPK)")!)
        let page = app.webViews["chat-webview"]
        XCTAssertTrue(page.waitForExistence(timeout: 60), "the chat page never came up")
        let open = page.buttons.matching(NSPredicate(format: "label IN %@", ["Start Chat", "Open Chat"])).firstMatch
        XCTAssertTrue(open.waitForExistence(timeout: 60), "the link never filled Add by address")
        open.tap()

        let actions = page.descendants(matching: .any)["Chat actions"].firstMatch
        XCTAssertTrue(actions.waitForExistence(timeout: 20), "no chat actions")
        actions.tap()
        let call = page.buttons["Call"].firstMatch
        XCTAssertTrue(call.waitForExistence(timeout: 10), "the chat actions offer no Call")
        call.tap()

        // Answered the moment it rings, often before the app's next two-second poll.
        var callID: String?
        for _ in 0..<300 where callID == nil {
            callID = try await peer.incoming().first
            if callID == nil { try await Task.sleep(for: .milliseconds(100)) }
        }
        let id = try XCTUnwrap(callID, "the call never rang on the peer")
        try await peer.post("answer", ["call_id": id])

        // Two polls, so the app has seen the call as active. Then whichever is in front ends it:
        // the app's call screen if it saw the call ring, the page's panel if the answer beat it.
        try await Task.sleep(for: .seconds(5))
        snap("call-answered")
        let screenHangUp = app.buttons["call-hang-up"]
        var hangUp = screenHangUp
        if !screenHangUp.exists {
            hangUp = page.buttons["Hang up"].firstMatch
            XCTAssertTrue(hangUp.waitForExistence(timeout: 10), "neither the app nor the page offers Hang up")
        }
        hangUp.tap()
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: hangUp)
        XCTAssertEqual(XCTWaiter().wait(for: [gone], timeout: 10), .completed, "the call is still on the phone")
        // The peer may first take the hang-up for a lost connection and wait out its 60 s reconnect.
        for _ in 0..<150 {
            if try await peer.active().isEmpty { return }
            try await Task.sleep(for: .milliseconds(500))
        }
        XCTFail("the call is still up on the peer")
    }

    private func snap(_ name: String) {
        usleep(700_000)
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}

/// A desktop peer's voice routes, called from the test runner, which shares the Mac's network.
private struct CallPeer {
    let origin: URL
    let pk: String

    /// The ids of calls ringing on the peer; each entry reads "<id> from <caller pk>".
    func incoming() async throws -> [String] {
        let (data, _) = try await URLSession.shared.data(from: origin.appendingPathComponent("/api/visors/\(pk)/skychat/voice/incoming"))
        let entries = (try? JSONSerialization.jsonObject(with: data)) as? [String] ?? []
        return entries.compactMap { $0.split(separator: " ").first.map(String.init) }
    }

    func active() async throws -> [String] {
        let (data, _) = try await URLSession.shared.data(from: origin.appendingPathComponent("/api/visors/\(pk)/skychat/voice/active"))
        return (try? JSONSerialization.jsonObject(with: data)) as? [String] ?? []
    }

    func post(_ action: String, _ body: [String: String]) async throws {
        let (csrf, _) = try await URLSession.shared.data(from: origin.appendingPathComponent("/api/csrf"))
        let token = (try JSONSerialization.jsonObject(with: csrf) as? [String: Any])?["csrf_token"] as? String
        var request = URLRequest(url: origin.appendingPathComponent("/api/visors/\(pk)/skychat/voice/\(action)"))
        request.httpMethod = "POST"
        request.setValue(token, forHTTPHeaderField: "X-CSRF-Token")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, response) = try await URLSession.shared.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        XCTAssertTrue((200..<300).contains(status), "the peer could not \(action) (\(status)): \(String(decoding: data, as: UTF8.self))")
    }
}
