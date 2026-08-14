import AppKit
import Foundation

@MainActor
final class AppModel: ObservableObject {
    enum Ownership: Equatable { case none, owned, external }
    enum Lifecycle: Equatable { case stopped, starting(String), ready, degraded, failed(String) }

    @Published private(set) var lifecycle: Lifecycle = .stopped
    @Published private(set) var ownership: Ownership = .none
    @Published private(set) var status: ServiceStatus?
    @Published private(set) var configuration = ConfigurationDocument(version: 2, sources: [])
    @Published private(set) var revision = ""
    @Published var selection: SidebarDestination? = .overview
    @Published var showingSourceWizard = false
    @Published var alertMessage: String?

    private let supervisor: ServiceSupervising
    private var client: ManagementServing
    private var pollTask: Task<Void, Never>?
    private var mutationActive = false
    private var pollGeneration = 0
    private var identity: (String, String)?

    init(supervisor: ServiceSupervising, client: ManagementServing) {
        self.supervisor = supervisor
        self.client = client
    }

    static func live() -> AppModel {
        let root = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!.appendingPathComponent("QuackRidge")
        return AppModel(supervisor: ServiceSupervisor(), client: ManagementClient(socketPath: root.appendingPathComponent("control.sock").path))
    }

    var menuBarSymbol: String {
        switch lifecycle { case .ready: return "mountain.2.fill"; case .degraded, .failed: return "exclamationmark.triangle"; default: return "mountain.2" }
    }

    func start() async {
        guard lifecycle == .stopped else { return }
        lifecycle = .starting("Verifying backend")
        do {
            let readiness = try await supervisor.start()
            ownership = .owned
            identity = (readiness.daemonInstanceID, readiness.pairingGeneration)
            lifecycle = readiness.lifecycleState == "degraded" ? .degraded : .ready
            await refresh()
        } catch {
            do {
                let handshake = try await client.handshake()
                guard handshake.versionInfo?.product == "quackridge", handshake.versionInfo?.managementProtocolVersion == managementProtocolVersion else { throw ClientError.incompatibleProtocol }
                ownership = .external
                applyIdentity(handshake.daemonInstanceID, handshake.pairingGeneration)
                lifecycle = .ready
                await refresh()
            } catch {
                lifecycle = .failed("QuackRidge could not start. Open Diagnostics for recovery details.")
            }
        }
    }

    func refresh() async {
        pollGeneration += 1
        let generation = pollGeneration
        do {
            async let statusResponse = client.status()
            async let configurationResponse = client.configuration()
            let (nextStatus, nextConfiguration) = try await (statusResponse, configurationResponse)
            guard generation == pollGeneration else { return }
            applyIdentity(nextStatus.daemonInstanceID, nextStatus.pairingGeneration)
            status = nextStatus.status
            if let document = nextConfiguration.configuration { configuration = document }
            revision = nextConfiguration.revision ?? revision
            lifecycle = nextStatus.status?.state == "degraded" ? .degraded : .ready
        } catch {
            if ownership == .owned { lifecycle = .failed("The managed backend stopped unexpectedly.") }
        }
    }

    func mutate(_ mutation: SourceMutation) async {
        guard !mutationActive else { return }
        mutationActive = true
        defer { mutationActive = false }
        do {
            _ = try await client.mutate(mutation)
            await refresh()
            if mutation.operation != "test" { showingSourceWizard = false }
        } catch let error as ManagementError where error.code == "QR_CONFLICT" {
            alertMessage = "The source list changed. Review the latest settings and try again."
            await refresh()
        } catch {
            alertMessage = "The source change could not be completed."
        }
    }

    func setEnabled(_ source: ConfiguredSource, enabled: Bool) async {
        await mutate(SourceMutation(operation: "set_enabled", source: source, sourceID: source.id, enabled: enabled,
                                    expectedRevision: revision, credentialAction: .none, credential: nil))
    }

    func remove(_ source: ConfiguredSource) async {
        await mutate(SourceMutation(operation: "remove", source: source, sourceID: source.id, enabled: nil,
                                    expectedRevision: revision, credentialAction: .none, credential: nil))
    }

    func beginPairing() async throws -> PairingChallenge {
        let response = try await client.pair()
        guard let challenge = response.pairing else { throw ClientError.invalidEnvelope }
        return challenge
    }

    func loadDiagnostics() async throws -> [String: JSONValue] {
        try await client.diagnostics().diagnostics ?? [:]
    }

    func managerBecameVisible() async {
        await start()
        pollTask?.cancel()
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(2))
                await self?.refresh()
            }
        }
    }

    func managerBecameHidden() {
        pollTask?.cancel(); pollTask = nil
    }

    func quit() async {
        pollTask?.cancel()
        if ownership == .owned { await supervisor.stop() }
        NSApplication.shared.terminate(nil)
    }

    private func applyIdentity(_ daemon: String?, _ pairing: String?) {
        guard let daemon, let pairing else { return }
        let next = (daemon, pairing)
        if let identity, identity != next {
            NotificationCenter.default.post(name: .pairingInvalidated, object: nil)
        }
        identity = next
    }
}

extension Notification.Name { static let pairingInvalidated = Notification.Name("QuackRidgePairingInvalidated") }

enum SidebarDestination: String, CaseIterable, Identifiable {
    case overview = "Overview", sources = "Sources", pairing = "Pairing", diagnostics = "Diagnostics"
    var id: String { rawValue }
    var symbol: String {
        switch self { case .overview: "gauge.with.dots.needle.50percent"; case .sources: "cylinder.split.1x2"; case .pairing: "link"; case .diagnostics: "stethoscope" }
    }
}
