import Darwin
import Foundation

protocol ManagementServing: Sendable {
    func status() async throws -> ManagementResponse<EmptyPayload>
    func configuration() async throws -> ManagementResponse<EmptyPayload>
    func mutate(_ mutation: SourceMutation) async throws -> ManagementResponse<EmptyPayload>
    func handshake() async throws -> ManagementResponse<HandshakeResult>
    func pair() async throws -> ManagementResponse<EmptyPayload>
    func diagnostics() async throws -> ManagementResponse<EmptyPayload>
}

actor ManagementClient: ManagementServing {
    let socketPath: String
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    init(socketPath: String) {
        self.socketPath = socketPath
        decoder.dateDecodingStrategy = .iso8601
    }

    func handshake() async throws -> ManagementResponse<HandshakeResult> {
        try await call(operation: "handshake", payload: Optional<EmptyPayload>.none)
    }

    func status() async throws -> ManagementResponse<EmptyPayload> {
        try await call(operation: "status", payload: Optional<EmptyPayload>.none)
    }

    func configuration() async throws -> ManagementResponse<EmptyPayload> {
        try await call(operation: "configuration", payload: Optional<EmptyPayload>.none)
    }

    func mutate(_ mutation: SourceMutation) async throws -> ManagementResponse<EmptyPayload> {
        try await call(operation: "source_\(mutation.operation)", payload: mutation)
    }

    func pair() async throws -> ManagementResponse<EmptyPayload> {
        try await call(operation: "pair_start", payload: PairingStart(origins: ["https://app.pondpilot.io"], ttlSeconds: 120))
    }

    func diagnostics() async throws -> ManagementResponse<EmptyPayload> {
        try await call(operation: "diagnostics", payload: Optional<EmptyPayload>.none)
    }

    private func call<Payload: Encodable, Result: Decodable>(operation: String, payload: Payload?) async throws -> ManagementResponse<Result> {
        try Task.checkCancellation()
        let requestID = UUID().uuidString.lowercased()
        var frame = try encoder.encode(ManagementRequest(requestID: requestID, operation: operation, payload: payload))
        frame.append(0x0A)
        guard frame.count <= managementFrameLimit else { throw ClientError.frameTooLarge }

        let descriptor = socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw ClientError.system(errno) }
        defer { Darwin.close(descriptor) }
        try connect(descriptor)
        try writeAll(descriptor, frame)
        let responseData = try readFrame(descriptor)
        try Self.validateEnvelopeKeys(responseData)
        let response = try decoder.decode(ManagementResponse<Result>.self, from: responseData)
        guard response.version == managementProtocolVersion else { throw ClientError.incompatibleProtocol }
        guard response.requestID == requestID else { throw ClientError.requestMismatch }
        if let error = response.error { throw error }
        return response
    }

    private func connect(_ descriptor: Int32) throws {
        guard socketPath.utf8.count < MemoryLayout.size(ofValue: sockaddr_un().sun_path) else { throw ClientError.pathTooLong }
        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        withUnsafeMutablePointer(to: &address.sun_path) { pointer in
            pointer.withMemoryRebound(to: CChar.self, capacity: socketPath.utf8.count + 1) { destination in
                _ = socketPath.withCString { source in strcpy(destination, source) }
            }
        }
        let length = socklen_t(MemoryLayout<sa_family_t>.size + socketPath.utf8.count + 1)
        let result = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.connect(descriptor, $0, length) }
        }
        guard result == 0 else { throw ClientError.system(errno) }
        var timeout = timeval(tv_sec: 30, tv_usec: 0)
        _ = setsockopt(descriptor, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
        _ = setsockopt(descriptor, SOL_SOCKET, SO_SNDTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
    }

    private func writeAll(_ descriptor: Int32, _ data: Data) throws {
        try data.withUnsafeBytes { bytes in
            var offset = 0
            while offset < bytes.count {
                let count = Darwin.write(descriptor, bytes.baseAddress!.advanced(by: offset), bytes.count - offset)
                if count < 0 && errno == EINTR { continue }
                guard count > 0 else { throw ClientError.system(errno) }
                offset += count
            }
        }
    }

    private func readFrame(_ descriptor: Int32) throws -> Data {
        var result = Data()
        var byte: UInt8 = 0
        while result.count < managementFrameLimit {
            let count = Darwin.read(descriptor, &byte, 1)
            if count < 0 && errno == EINTR { continue }
            guard count > 0 else { throw ClientError.truncatedFrame }
            if byte == 0x0A { return result }
            result.append(byte)
        }
        throw ClientError.frameTooLarge
    }

    static func validateEnvelopeKeys(_ data: Data) throws {
        let value = try JSONSerialization.jsonObject(with: data)
        guard let object = value as? [String: Any] else { throw ClientError.invalidEnvelope }
        let allowed: Set<String> = [
            "version", "request_id", "ok", "error", "error_code", "message", "result",
            "status", "configuration", "revision", "diagnostics", "version_info",
            "daemon_instance_id", "pairing_generation", "pairing", "pairing_state",
            "manual_reveal", "sensitive", "certificates",
        ]
        guard Set(object.keys).isSubset(of: allowed) else { throw ClientError.unknownField }
    }
}

enum ClientError: Error, Equatable {
    case system(Int32), frameTooLarge, truncatedFrame, incompatibleProtocol, requestMismatch, pathTooLong, invalidEnvelope, unknownField
}
