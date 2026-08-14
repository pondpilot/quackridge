@preconcurrency import Foundation
import Darwin

protocol ServiceSupervising: Sendable {
    func start() async throws -> BackendReadiness
    func stop() async
    func ownedPID() async -> Int32?
}

actor ServiceSupervisor: ServiceSupervising {
    private var process: Process?
    private var lifecycleWriter: FileHandle?
    private var eventListener: Int32 = -1
    private var eventDirectory: URL?
    private var restarts: [Date] = []

    func ownedPID() -> Int32? { process?.isRunning == true ? process?.processIdentifier : nil }

    func start() async throws -> BackendReadiness {
        if let process, process.isRunning { throw SupervisorError.alreadyRunning }
        let (_, helperURL) = try BackendManifest.loadAndVerify()
        let paths = try Self.applicationPaths()
        try FileManager.default.createDirectory(at: paths.state, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let event = try Self.makeEventListener(in: paths.temporary)
        eventListener = event.descriptor
        eventDirectory = event.directory

        let lifecycle = Pipe()
        _ = fcntl(lifecycle.fileHandleForReading.fileDescriptor, F_SETFD, 0)
        let stdout = Pipe(), stderr = Pipe()
        Self.discard(stdout.fileHandleForReading)
        Self.discard(stderr.fileHandleForReading)

        let child = Process()
        child.executableURL = helperURL
        child.arguments = ["serve", "--config", paths.configuration.path, "--control", paths.control.path,
                           "--extensions", paths.extensions.path, "--event-socket", event.path,
                           "--lifecycle-fd", String(lifecycle.fileHandleForReading.fileDescriptor),
                           "--startup-timeout", "60s", "--json"]
        child.environment = Self.minimalEnvironment(paths: paths)
        child.standardOutput = stdout.fileHandleForWriting
        child.standardError = stderr.fileHandleForWriting
        process = child
        lifecycleWriter = lifecycle.fileHandleForWriting
        do {
            try child.run()
            try lifecycle.fileHandleForReading.close()
            try stdout.fileHandleForWriting.close()
            try stderr.fileHandleForWriting.close()
            return try await withThrowingTaskGroup(of: BackendReadiness.self) { group in
                group.addTask { try await Self.receiveReadiness(listener: event.descriptor, expectedPID: child.processIdentifier) }
                group.addTask {
                    try await Task.sleep(for: .seconds(60))
                    throw SupervisorError.startupTimeout
                }
                let readiness = try await group.next()!
                group.cancelAll()
                return readiness
            }
        } catch {
            await stop()
            throw error
        }
    }

    func stop() async {
        try? lifecycleWriter?.close()
        lifecycleWriter = nil
        guard let child = process else { cleanup(); return }
        if child.isRunning {
            for _ in 0..<50 where child.isRunning { try? await Task.sleep(for: .milliseconds(100)) }
        }
        if child.isRunning { kill(child.processIdentifier, SIGTERM) }
        if child.isRunning {
            for _ in 0..<20 where child.isRunning { try? await Task.sleep(for: .milliseconds(100)) }
        }
        if child.isRunning { kill(child.processIdentifier, SIGKILL) }
        process = nil
        cleanup()
    }

    private func cleanup() {
        if eventListener >= 0 { Darwin.close(eventListener); eventListener = -1 }
        if let eventDirectory { try? FileManager.default.removeItem(at: eventDirectory) }
        eventDirectory = nil
    }

    private struct Paths: Sendable {
        let state: URL, temporary: URL, configuration: URL, control: URL, extensions: URL
    }

    private static func applicationPaths() throws -> Paths {
        let manager = FileManager.default
        guard let state = manager.urls(for: .applicationSupportDirectory, in: .userDomainMask).first else { throw SupervisorError.noHome }
        let root = state.appendingPathComponent("QuackRidge", isDirectory: true)
        let temporary = manager.temporaryDirectory
        guard let extensions = Bundle.main.resourceURL?.appendingPathComponent("Backend/extensions", isDirectory: true) else { throw SupervisorError.missingResources }
        return Paths(state: root, temporary: temporary, configuration: root.appendingPathComponent("config.json"),
                     control: root.appendingPathComponent("control.sock"), extensions: extensions)
    }

    private static func minimalEnvironment(paths: Paths) -> [String: String] {
        guard let accountPointer = getpwuid(getuid()) else { return [
            "TMPDIR": paths.temporary.path,
            "LANG": "en_US.UTF-8",
            "LC_ALL": "en_US.UTF-8",
        ] }
        let account = accountPointer.pointee
        let home = String(cString: account.pw_dir)
        let user = String(cString: account.pw_name)
        return ["HOME": home, "USER": user, "LOGNAME": user, "TMPDIR": paths.temporary.path,
                "LANG": "en_US.UTF-8", "LC_ALL": "en_US.UTF-8"]
    }

    private static func makeEventListener(in temporary: URL) throws -> (descriptor: Int32, directory: URL, path: String) {
        let directory = temporary.appendingPathComponent("quackridge-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
        let path = directory.appendingPathComponent("event.sock").path
        let descriptor = socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else { throw SupervisorError.system(errno) }
        let flags = fcntl(descriptor, F_GETFL)
        guard flags >= 0, fcntl(descriptor, F_SETFL, flags | O_NONBLOCK) == 0 else {
            let code = errno; Darwin.close(descriptor); try? FileManager.default.removeItem(at: directory); throw SupervisorError.system(code)
        }
        var address = sockaddr_un(); address.sun_family = sa_family_t(AF_UNIX)
        guard path.utf8.count < MemoryLayout.size(ofValue: address.sun_path) else { Darwin.close(descriptor); throw SupervisorError.pathTooLong }
        withUnsafeMutablePointer(to: &address.sun_path) { pointer in
            pointer.withMemoryRebound(to: CChar.self, capacity: path.utf8.count + 1) { destination in
                _ = path.withCString { source in strcpy(destination, source) }
            }
        }
        let length = socklen_t(MemoryLayout<sa_family_t>.size + path.utf8.count + 1)
        let bound = withUnsafePointer(to: &address) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.bind(descriptor, $0, length) } }
        guard bound == 0, chmod(path, 0o600) == 0, Darwin.listen(descriptor, 1) == 0 else {
            let code = errno; Darwin.close(descriptor); try? FileManager.default.removeItem(at: directory); throw SupervisorError.system(code)
        }
        return (descriptor, directory, path)
    }

    private static func receiveReadiness(listener: Int32, expectedPID: Int32) async throws -> BackendReadiness {
        try await Task.detached {
            var connection: Int32 = -1
            while connection < 0 {
                try Task.checkCancellation()
                connection = Darwin.accept(listener, nil, nil)
                if connection < 0 && errno == EINTR { continue }
                if connection < 0 && (errno == EAGAIN || errno == EWOULDBLOCK) {
                    try await Task.sleep(for: .milliseconds(50))
                    continue
                }
                guard connection >= 0 else { throw SupervisorError.system(errno) }
            }
            defer { Darwin.close(connection) }
            var timeout = timeval(tv_sec: 0, tv_usec: 250_000)
            _ = setsockopt(connection, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
            var uid: uid_t = 0, gid: gid_t = 0
            guard getpeereid(connection, &uid, &gid) == 0, uid == getuid() else { throw SupervisorError.untrustedPeer }
            var peerPID: pid_t = 0; var length = socklen_t(MemoryLayout<pid_t>.size)
            guard getsockopt(connection, SOL_LOCAL, LOCAL_PEERPID, &peerPID, &length) == 0, peerPID == expectedPID else { throw SupervisorError.untrustedPeer }
            var frame = Data(), byte: UInt8 = 0
            while frame.count < managementFrameLimit {
                let count = Darwin.read(connection, &byte, 1)
                if count < 0 && errno == EINTR { continue }
                if count < 0 && (errno == EAGAIN || errno == EWOULDBLOCK) {
                    try Task.checkCancellation()
                    continue
                }
                guard count > 0 else { throw SupervisorError.truncatedEvent }
                if byte == 0x0A {
                    let decoder = JSONDecoder(); decoder.dateDecodingStrategy = .iso8601
                    let event = try decoder.decode(LifecycleEvent.self, from: frame)
                    if event.type == "failure" { throw SupervisorError.backendFailure(event.code ?? "QR_INTERNAL") }
                    if let readiness = event.readiness {
                        guard readiness.pid == expectedPID, readiness.managementProtocolVersion == managementProtocolVersion else { throw SupervisorError.untrustedPeer }
                        return readiness
                    }
                    frame.removeAll(keepingCapacity: true)
                } else { frame.append(byte) }
            }
            throw SupervisorError.frameTooLarge
        }.value
    }

    private static func discard(_ handle: FileHandle) {
        Task.detached {
            while !Task.isCancelled {
                let data = try? handle.read(upToCount: 64 * 1024)
                if data == nil || data?.isEmpty == true { break }
            }
            try? handle.close()
        }
    }
}

enum SupervisorError: Error {
    case alreadyRunning, startupTimeout, noHome, missingResources, pathTooLong, untrustedPeer, truncatedEvent, frameTooLarge
    case system(Int32), backendFailure(String)
}
