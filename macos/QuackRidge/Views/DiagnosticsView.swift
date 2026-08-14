import SwiftUI

struct DiagnosticsView: View {
    @EnvironmentObject private var model: AppModel
    @State private var diagnostics: [String: JSONValue] = [:]
    @State private var loading = false
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: QRSpace.lg) {
                SectionCard(title: "Service") {
                    LabeledContent("State", value: model.status?.state ?? "Unavailable")
                    LabeledContent("Management protocol", value: String(managementProtocolVersion))
                    LabeledContent("Ownership", value: model.ownership == .external ? "External" : "App-owned")
                }
                SectionCard(title: "Security posture") {
                    Label("Local-user management socket", systemImage: "checkmark.shield")
                    Label("Credentials remain in Keychain", systemImage: "key")
                    Label("Source errors are sanitized", systemImage: "eye.slash")
                }
                SectionCard(title: "Live checks") {
                    if loading { ProgressView() }
                    ForEach(diagnostics.keys.sorted(), id: \.self) { key in LabeledContent(key.replacingOccurrences(of: "_", with: " ").capitalized, value: printable(diagnostics[key])) }
                    Button("Run Diagnostics") { Task { await load() } }.disabled(loading)
                }
                SectionCard(title: "Support") {
                    Text("Support reports include versions and sanitized health only. They are never uploaded automatically.")
                    Button("Save Sanitized Report…") { saveReport() }
                }
            }.padding(QRSpace.xl)
        }.navigationTitle("Diagnostics").task { await load() }
    }
    private func load() async { loading = true; defer { loading = false }; diagnostics = (try? await model.loadDiagnostics()) ?? [:] }
    private func printable(_ value: JSONValue?) -> String {
        switch value { case .string(let item): item; case .number(let item): String(item); case .bool(let item): item ? "Yes" : "No"; case .array(let item): "\(item.count) items"; case .object(let item): "\(item.count) values"; default: "Not available" }
    }
    private func saveReport() {
        let panel = NSSavePanel(); panel.nameFieldStringValue = "QuackRidge-Support.json"; panel.allowedContentTypes = [.json]
        guard panel.runModal() == .OK, let url = panel.url else { return }
        let report: [String: Any] = ["product": "QuackRidge", "management_protocol": managementProtocolVersion,
                                     "service_state": model.status?.state ?? "unavailable", "source_count": model.configuration.sources.count]
        if let data = try? JSONSerialization.data(withJSONObject: report, options: [.prettyPrinted, .sortedKeys]) { try? data.write(to: url, options: .atomic) }
    }
}
