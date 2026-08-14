import Foundation

let managementProtocolVersion = 2
let managementFrameLimit = 64 * 1024

enum JSONValue: Codable, Equatable, Sendable {
    case string(String), number(Double), bool(Bool), object([String: JSONValue]), array([JSONValue]), null

    init(from decoder: Decoder) throws {
        let value = try decoder.singleValueContainer()
        if value.decodeNil() { self = .null }
        else if let item = try? value.decode(Bool.self) { self = .bool(item) }
        else if let item = try? value.decode(Double.self) { self = .number(item) }
        else if let item = try? value.decode(String.self) { self = .string(item) }
        else if let item = try? value.decode([String: JSONValue].self) { self = .object(item) }
        else { self = .array(try value.decode([JSONValue].self)) }
    }

    func encode(to encoder: Encoder) throws {
        var value = encoder.singleValueContainer()
        switch self {
        case .string(let item): try value.encode(item)
        case .number(let item): try value.encode(item)
        case .bool(let item): try value.encode(item)
        case .object(let item): try value.encode(item)
        case .array(let item): try value.encode(item)
        case .null: try value.encodeNil()
        }
    }
}

struct EmptyPayload: Codable, Sendable {}

struct ManagementRequest<Payload: Encodable>: Encodable {
    let version = managementProtocolVersion
    let requestID: String
    let operation: String
    let payload: Payload?

    enum CodingKeys: String, CodingKey {
        case version, operation, payload
        case requestID = "request_id"
    }
}

struct ManagementResponse<Result: Decodable>: Decodable {
    let version: Int
    let requestID: String
    let ok: Bool
    let error: ManagementError?
    let result: Result?
    let versionInfo: Result?
    let status: ServiceStatus?
    let configuration: ConfigurationDocument?
    let revision: String?
    let daemonInstanceID: String?
    let pairingGeneration: String?
    let pairing: PairingChallenge?
    let diagnostics: [String: JSONValue]?

    enum CodingKeys: String, CodingKey {
        case version, ok, error, result, status, configuration, revision, pairing, diagnostics
        case versionInfo = "version_info"
        case requestID = "request_id"
        case daemonInstanceID = "daemon_instance_id"
        case pairingGeneration = "pairing_generation"
    }
}

struct PairingStart: Encodable, Sendable {
    let origins: [String]
    let ttlSeconds: Int
    enum CodingKeys: String, CodingKey { case origins; case ttlSeconds = "ttl_seconds" }
}

struct PairingChallenge: Codable, Equatable, Sendable {
    let url: URL
    let nonce: String
    let expiresAt: Date
    enum CodingKeys: String, CodingKey { case url, nonce; case expiresAt = "expires_at" }
}

struct ManagementError: Error, Codable, Equatable, Sendable {
    let code: String
    let message: String
    let field: String?
    let recovery: [String: String]?
}

struct ServiceStatus: Codable, Equatable, Sendable {
    let state: String
    let endpoint: String?
    let startedAt: Date?
    let sources: [SourceHealth]
    let lastError: String?
    let capabilities: [String]

    enum CodingKeys: String, CodingKey {
        case state, endpoint, sources, capabilities
        case startedAt = "started_at"
        case lastError = "last_error"
    }
}

struct SourceHealth: Codable, Equatable, Identifiable, Sendable {
    let id: String
    let name: String
    let type: String
    let health: String
    let errorCode: String?
    enum CodingKeys: String, CodingKey { case id, name, type, health; case errorCode = "error_code" }
}

struct ConfigurationDocument: Codable, Equatable, Sendable {
    let version: Int
    var sources: [ConfiguredSource]
}

struct ConfiguredSource: Codable, Equatable, Identifiable, Sendable {
    var id: String
    var name: String
    var alias: String
    var type: String
    var databaseType: String?
    var enabled: Bool
    var credentialRef: String
    var options: [String: JSONValue]

    enum CodingKeys: String, CodingKey {
        case id, name, alias, type, enabled, options
        case databaseType = "database_type"
        case credentialRef = "credential_ref"
    }
}

enum CredentialAction: String, Codable, Sendable { case none, keep, replace, remove }

struct SourceMutation: Encodable, Sendable {
    let operation: String
    let source: ConfiguredSource
    let sourceID: String?
    let enabled: Bool?
    let expectedRevision: String?
    let credentialAction: CredentialAction
    let credential: Data?

    enum CodingKeys: String, CodingKey {
        case operation, source, enabled, credential
        case sourceID = "source_id"
        case expectedRevision = "expected_revision"
        case credentialAction = "credential_action"
    }
}

struct HandshakeResult: Decodable, Sendable {
    let product: String
    let productVersion: String
    let managementProtocolVersion: Int
    let quackProtocolVersion: Int
    let capabilities: [String]
    enum CodingKeys: String, CodingKey {
        case product, capabilities
        case productVersion = "product_version"
        case managementProtocolVersion = "management_protocol_version"
        case quackProtocolVersion = "quack_protocol_version"
    }
}

struct LifecycleEvent: Decodable, Sendable {
    let type: String
    let timestamp: Date
    let phase: String?
    let readiness: BackendReadiness?
    let code: String?
    let message: String?
}

struct BackendReadiness: Decodable, Equatable, Sendable {
    let pid: Int32
    let daemonInstanceID: String
    let pairingGeneration: String
    let lifecycleState: String
    let endpoint: String
    let controlPath: String
    let productVersion: String
    let managementProtocolVersion: Int
    enum CodingKeys: String, CodingKey {
        case pid, endpoint
        case daemonInstanceID = "daemon_instance_id"
        case pairingGeneration = "pairing_generation"
        case lifecycleState = "lifecycle_state"
        case controlPath = "control_path"
        case productVersion = "product_version"
        case managementProtocolVersion = "management_protocol_version"
    }
}
