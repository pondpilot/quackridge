import XCTest
@testable import QuackRidge

actor FakeSupervisor: ServiceSupervising {
    var running = false
    func start() async throws -> BackendReadiness { running = true; return BackendReadiness(pid: 42, daemonInstanceID: "one", pairingGeneration: "generation-one", lifecycleState: "ready", endpoint: "quack:127.0.0.1:9494", controlPath: "/tmp/control", productVersion: "1", managementProtocolVersion: 2) }
    func stop() async { running = false }
    func ownedPID() async -> Int32? { running ? 42 : nil }
}

actor FakeManagement: ManagementServing {
    var generation = "generation-one"
    func handshake() async throws -> ManagementResponse<HandshakeResult> { fatalError() }
    func pair() async throws -> ManagementResponse<EmptyPayload> { fatalError() }
    func diagnostics() async throws -> ManagementResponse<EmptyPayload> { fatalError() }
    func mutate(_ mutation: SourceMutation) async throws -> ManagementResponse<EmptyPayload> { fatalError() }
    func status() async throws -> ManagementResponse<EmptyPayload> { response(status: ServiceStatus(state: "ready", endpoint: "quack:127.0.0.1:9494", startedAt: .now, sources: [], lastError: nil, capabilities: [])) }
    func configuration() async throws -> ManagementResponse<EmptyPayload> { response(configuration: ConfigurationDocument(version: 2, sources: []), revision: String(repeating: "a", count: 64)) }
    private func response(status: ServiceStatus? = nil, configuration: ConfigurationDocument? = nil, revision: String? = nil) -> ManagementResponse<EmptyPayload> {
        ManagementResponse(version: 2, requestID: "fixture", ok: true, error: nil, result: nil, versionInfo: nil, status: status, configuration: configuration, revision: revision, daemonInstanceID: "one", pairingGeneration: generation, pairing: nil, diagnostics: nil)
    }
}

@MainActor final class AppModelTests: XCTestCase {
    func testOwnedStartLoadsConfiguration() async {
        let model = AppModel(supervisor: FakeSupervisor(), client: FakeManagement())
        await model.start()
        XCTAssertEqual(model.ownership, .owned)
        XCTAssertEqual(model.lifecycle, .ready)
        XCTAssertEqual(model.configuration.version, 2)
    }
}
