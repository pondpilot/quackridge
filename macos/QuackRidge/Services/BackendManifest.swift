import CryptoKit
import Foundation

struct BackendManifest: Decodable, Sendable {
    let schemaVersion: Int
    let productVersion: String
    let managementProtocolVersion: Int
    let minimumOS: String
    let architectures: [String]
    let helper: ManifestFile
    let extensions: [ManifestFile]

    enum CodingKeys: String, CodingKey {
        case schemaVersion = "schema_version"
        case productVersion = "product_version"
        case managementProtocolVersion = "management_protocol_version"
        case minimumOS = "minimum_os"
        case architectures, helper, extensions
    }
}

struct ManifestFile: Decodable, Sendable {
    let path: String
    let sha256: String
    let size: Int
}

enum ManifestError: Error { case missing, unsupported, unsafePath, sizeMismatch, digestMismatch, architectureMismatch }

extension BackendManifest {
    static func loadAndVerify(bundle: Bundle = .main) throws -> (Self, URL) {
        guard let manifestURL = bundle.url(forResource: "backend-manifest", withExtension: "json"),
              bundle.resourceURL != nil else { throw ManifestError.missing }
        let contents = bundle.bundleURL.appendingPathComponent("Contents", isDirectory: true)
        let manifest = try JSONDecoder().decode(Self.self, from: Data(contentsOf: manifestURL))
        guard manifest.schemaVersion == 1, manifest.managementProtocolVersion == managementProtocolVersion else { throw ManifestError.unsupported }
        #if arch(arm64)
        let architecture = "arm64"
        #elseif arch(x86_64)
        let architecture = "amd64"
        #else
        throw ManifestError.architectureMismatch
        #endif
        guard manifest.architectures.contains(architecture) else { throw ManifestError.architectureMismatch }
        for file in [manifest.helper] + manifest.extensions {
            guard !file.path.hasPrefix("/"), !file.path.split(separator: "/").contains("..") else { throw ManifestError.unsafePath }
            let url = contents.appendingPathComponent(file.path).standardizedFileURL
            guard url.path.hasPrefix(contents.standardizedFileURL.path + "/") else { throw ManifestError.unsafePath }
            let data = try Data(contentsOf: url, options: .mappedIfSafe)
            guard data.count == file.size else { throw ManifestError.sizeMismatch }
            let digest = SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
            guard digest == file.sha256.lowercased() else { throw ManifestError.digestMismatch }
        }
        return (manifest, contents.appendingPathComponent(manifest.helper.path))
    }
}
