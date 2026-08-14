import XCTest

final class QuackRidgeUITests: XCTestCase {
    func testOnboardingIsKeyboardReachable() {
        let app = XCUIApplication(); app.launchArguments = ["-completedOnboarding", "NO"]; app.launch()
        XCTAssertTrue(app.buttons["Continue"].waitForExistence(timeout: 5))
        app.buttons["Continue"].typeKey(.return, modifierFlags: [])
    }
}
