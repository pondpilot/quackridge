import Foundation

enum PreviewFixtures {
    static let stopped = ServiceStatus(state: "stopped", endpoint: nil, startedAt: nil, sources: [], lastError: nil, capabilities: [])
    static let healthy = ServiceStatus(state: "ready", endpoint: "quack:127.0.0.1:9494", startedAt: .now, sources: [SourceHealth(id: "warehouse", name: "Warehouse", type: "postgres", health: "ready", errorCode: nil)], lastError: nil, capabilities: [])
    static let degraded = ServiceStatus(state: "degraded", endpoint: "quack:127.0.0.1:9494", startedAt: .now, sources: [SourceHealth(id: "warehouse", name: "Warehouse", type: "postgres", health: "unavailable", errorCode: "QR_SOURCE_UNAVAILABLE")], lastError: nil, capabilities: [])
}
