import WalletCore
import XCTest

/// The reward xpub (Android: SettingsScreen's RewardCard and the xpub dialog's button), against
/// the core in the app. Part of the SkywireTour scheme (the core needs the live network), not of
/// CI; in English, as the other flow checks.
@MainActor
final class RewardChecks: XCTestCase {
    /// An account level xpub; the visor's own validator takes it.
    private static let xpub = "xpub6DMLxE9HpaMf7wCMK2ikiAshrkvHBWfdyFFhdajSpi1h2PvCN51rLzxfsqV9EPnrmqpRmnutibiKJX39acErEGmU5syK9ir6wmMAnc2XQWL"
    private static let singleAddress = "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"
    private static let english = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]

    override func setUp() {
        continueAfterFailure = false
    }

    /// A single address is refused before the round trip. An account xpub is saved, and Remove
    /// clears it from the same running core, so Settings shows nothing when it comes back to the
    /// front (#5833: the visor kept it in memory). Saved again, a restarted core reads it back.
    func testSettingsSavesAndRemovesTheXpub() {
        var app = connectedApp(arguments: Self.english)
        openRewardCard(app)
        clearReward(app)

        replaceText("settings-reward", with: Self.singleAddress, app)
        XCTAssertTrue(app.staticTexts["Enter an account xpub. It starts with xpub; a single address is not accepted."].waitForExistence(timeout: 3))
        XCTAssertFalse(app.buttons["settings-reward-save"].isEnabled, "Save is on for a single address")
        saveXpub(app)
        attachScreenshot(app, "reward-1-saved")

        clearReward(app)
        openTab(.home, in: app)
        openRewardCard(app)
        XCTAssertTrue(waitForReward("", app), "the running core still reports the removed xpub")
        XCTAssertFalse(app.buttons["settings-reward-clear"].exists, "Remove is still offered")

        saveXpub(app)
        app.terminate()
        app = connectedApp(arguments: Self.english)
        openRewardCard(app)
        XCTAssertTrue(waitForReward(Self.xpub, app), "the restarted core lost the xpub")
        clearReward(app)
    }

    /// A new BIP 44 wallet's account xpub, set from its extended public keys dialog, is what
    /// Settings shows next, without a core restart.
    func testTheWalletSetsItsAccountXpub() throws {
        let app = connectedApp(arguments: Self.english)
        openRewardCard(app)
        clearReward(app)

        openTab(.wallet, in: app)
        selectCoin("SKY", app)
        startCreate(app)
        let bip44 = app.buttons["BIP44 (HD)"]
        XCTAssertTrue(bip44.waitForExistence(timeout: 5))
        bip44.tap()
        let words = try readSeedWords(app)
        passQuiz(words, app)
        let seed = KeySource(mnemonic: words.joined(separator: " "), passphrase: "", derivation: .bip44)
        // Derivation only: the node is never asked.
        let core = try SkyFiberWalletCore(nodeURL: "http://127.0.0.1:1")
        let account = try XCTUnwrap(try core.extendedPublicKeys(seed: seed)).account
        let first = try core.deriveAddresses(seed: seed, receiveCount: 1, changeCount: 0).receive[0]
        XCTAssertEqual(try receiveAddress(app, timeout: 30), first)
        back(app)

        openWallets(app)
        app.buttons["wallet-row-\(first)"].tap()
        tapWhenUp("wallet-action-xpub", in: app)
        XCTAssertEqual(app.buttons["wallet-xpub-0"].label, account)
        // One button, under the account key and above the chains'.
        let use = app.buttons["wallet-xpub-use-reward"]
        XCTAssertEqual(app.buttons.matching(identifier: "wallet-xpub-use-reward").count, 1)
        XCTAssertTrue(use.frame.minY > app.buttons["wallet-xpub-0"].frame.maxY && use.frame.maxY < app.buttons["wallet-xpub-1"].frame.minY,
                      "the button is not under the account key")
        use.tap()
        let result = app.staticTexts["wallet-xpub-reward-result"]
        XCTAssertTrue(result.waitForExistence(timeout: 20), "no answer in the dialog")
        XCTAssertEqual(result.label, "Reward xpub saved.")
        attachScreenshot(app, "reward-2-wallet")
        tapWhenUp("Done", in: app)
        back(app)

        openRewardCard(app)
        XCTAssertTrue(waitForReward(account, app), "Settings shows another address than the wallet set")
        clearReward(app)
        openTab(.wallet, in: app)
        removeWallet(first, app)
    }

    private func openRewardCard(_ app: XCUIApplication) {
        openTab(.settings, in: app)
        let field = app.textFields["settings-reward"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        scrollSettings(to: app.buttons["settings-reward-save"], app)
    }

    /// Drags Settings up along its right margin, beside the cards: a swipe from the middle of the
    /// screen can start on the DNS text box, and then the page does not move. Above where an
    /// on-screen keyboard starts, so a drag never types.
    private func scrollSettings(to element: XCUIElement, _ app: XCUIApplication) {
        let from = app.coordinate(withNormalizedOffset: CGVector(dx: 0.98, dy: 0.55))
        let to = app.coordinate(withNormalizedOffset: CGVector(dx: 0.98, dy: 0.3))
        for _ in 0..<10 where !element.isHittable {
            from.press(forDuration: 0.1, thenDragTo: to)
        }
        XCTAssertTrue(element.isHittable, "\(element) not reachable by scrolling")
    }

    /// Removes whatever reward address is set, an earlier run's included.
    private func clearReward(_ app: XCUIApplication) {
        let clear = app.buttons["settings-reward-clear"]
        guard clear.waitForExistence(timeout: 5) else { return }
        clear.tap()
        XCTAssertTrue(app.staticTexts["Reward address removed. This phone no longer earns rewards."].waitForExistence(timeout: 20),
                      "no removed notice")
        XCTAssertTrue(waitForReward("", app))
    }

    /// The field reads `value` once Settings has asked the core.
    private func waitForReward(_ value: String, _ app: XCUIApplication) -> Bool {
        let field = app.textFields["settings-reward"]
        let predicate = value.isEmpty ? NSPredicate(format: "value == nil OR value == ''") : NSPredicate(format: "value == %@", value)
        let shown = XCTNSPredicateExpectation(predicate: predicate, object: field)
        return XCTWaiter().wait(for: [shown], timeout: 20) == .completed
    }

    /// Types the account xpub and saves it. Return closes the keyboard, which would cover Save
    /// and the bottom bar.
    private func saveXpub(_ app: XCUIApplication) {
        replaceText("settings-reward", with: Self.xpub + "\n", app)
        let save = app.buttons["settings-reward-save"]
        scrollSettings(to: save, app)
        XCTAssertTrue(save.isEnabled, "Save is off for an account xpub")
        save.tap()
        XCTAssertTrue(app.staticTexts["Reward xpub saved."].waitForExistence(timeout: 20), "no saved notice")
    }
}
