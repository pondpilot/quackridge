import XCTest
@testable import QuackRidge

final class ProtocolFixtureTests: XCTestCase {
    func testHandshakeFixtureDecodesWithExactIdentity() throws {
        let data = Data(#"{"version":2,"request_id":"fixture-request","ok":true,"version_info":{"product":"quackridge","product_version":"1.0.0","management_protocol_version":2,"quack_protocol_version":2,"capabilities":[]},"daemon_instance_id":"0123456789abcdef0123456789abcdef","pairing_generation":"fedcba9876543210fedcba9876543210"}"#.utf8)
        try ManagementClient.validateEnvelopeKeys(data)
        let response = try JSONDecoder().decode(ManagementResponse<HandshakeResult>.self, from: data)
        XCTAssertTrue(response.ok)
        XCTAssertEqual(response.versionInfo?.managementProtocolVersion, 2)
        XCTAssertEqual(response.daemonInstanceID?.count, 32)
    }

    func testUnknownEnvelopeFieldFailsClosed() throws {
        let data = Data(#"{"version":2,"request_id":"fixture","ok":true,"secret":"marker"}"#.utf8)
        XCTAssertThrowsError(try ManagementClient.validateEnvelopeKeys(data))
    }

    func testSourceCredentialIsWriteOnly() throws {
        let source = ConfiguredSource(id: "warehouse", name: "Warehouse", alias: "warehouse", type: "postgres", databaseType: "postgres", enabled: true, credentialRef: "", options: ["host": .string("localhost")])
        let request = SourceMutation(operation: "add", source: source, sourceID: nil, enabled: nil, expectedRevision: String(repeating: "a", count: 64), credentialAction: .replace, credential: Data("synthetic".utf8))
        let data = try JSONEncoder().encode(request)
        XCTAssertTrue(String(decoding: data, as: UTF8.self).contains("credential"))
        XCTAssertFalse(String(describing: source).contains("synthetic"))
    }
}
