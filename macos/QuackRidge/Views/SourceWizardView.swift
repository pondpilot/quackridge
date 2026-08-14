import SwiftUI

struct SourceWizardView: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var connector = "postgres"
    @State private var id = "", name = "", alias = "", host = "", database = "", user = "", password = ""
    @State private var port = "5432", sslMode = "require", path = "", dsn = "", driver = "", databaseType = "odbc"
    @State private var step = 0
    @State private var working = false

    var body: some View {
        VStack(spacing: 0) {
            HStack { Text(step == 0 ? "Add a Source" : "Review Source").font(.title2.bold()); Spacer(); Button("Cancel") { clearAndDismiss() }.keyboardShortcut(.cancelAction) }
                .padding(QRSpace.lg)
            Divider()
            Form {
                if step == 0 {
                    Picker("Connector", selection: $connector) {
                        Text("PostgreSQL").tag("postgres"); Text("MySQL / MariaDB").tag("mysql")
                        Text("SQLite").tag("sqlite"); Text("DuckDB").tag("duckdb"); Text("ODBC").tag("odbc")
                    }.onChange(of: connector) { _, value in port = value == "mysql" ? "3306" : "5432"; sslMode = value == "mysql" ? "preferred" : "require" }
                    TextField("Source ID", text: $id).textContentType(.username)
                    TextField("Display name", text: $name)
                    TextField("Catalog alias", text: $alias)
                    connectorFields
                    if connector == "postgres" || connector == "mysql" || connector == "odbc" {
                        TextField("Username", text: $user)
                        SecureField("Password", text: $password).privacySensitive().accessibilityLabel("Database password")
                    }
                    Text("Credentials are sent only to the local Go backend and stored in your Keychain. They cannot be revealed by the normal management API.")
                        .font(.caption).foregroundStyle(.secondary)
                } else {
                    LabeledContent("Name", value: name); LabeledContent("Connector", value: connector)
                    LabeledContent("Catalog", value: alias)
                    Text("QuackRidge will validate this source, save its non-secret settings, and attach it read-only.").foregroundStyle(.secondary)
                }
            }.formStyle(.grouped).padding(.horizontal, QRSpace.md)
            Divider()
            HStack {
                if step > 0 { Button("Back") { step = 0 } }
                Spacer()
                Button(step == 0 ? "Review" : "Test and Add") { if step == 0 { step = 1 } else { Task { await submit() } } }
                    .buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction).disabled(!valid || working)
            }.padding(QRSpace.lg)
        }.frame(width: 620, height: 650).interactiveDismissDisabled(working)
    }

    @ViewBuilder private var connectorFields: some View {
        switch connector {
        case "postgres", "mysql":
            TextField("Host", text: $host); TextField("Port", text: $port); TextField("Database", text: $database)
            Picker("TLS", selection: $sslMode) {
                if connector == "postgres" { Text("Require").tag("require"); Text("Verify full").tag("verify-full"); Text("Disable").tag("disable") }
                else { Text("Preferred").tag("preferred"); Text("Required").tag("required"); Text("Verify identity").tag("verify_identity"); Text("Disabled").tag("disabled") }
            }
        case "sqlite", "duckdb":
            HStack { TextField("Database file", text: $path); Button("Choose…") { chooseFile() } }
            Text("The file is opened read-only. Select an absolute local path.").font(.caption).foregroundStyle(.secondary)
        default:
            Picker("Connection", selection: Binding(get: { dsn.isEmpty ? "driver" : "dsn" }, set: { if $0 == "dsn" { driver = "" } else { dsn = "" } })) {
                Text("Driver").tag("driver"); Text("DSN").tag("dsn")
            }
            if dsn.isEmpty { TextField("ODBC driver", text: $driver) } else { TextField("ODBC DSN", text: $dsn) }
            TextField("Database type", text: $databaseType)
        }
    }

    private var valid: Bool {
        guard !id.isEmpty, !name.isEmpty, !alias.isEmpty, id.allSatisfy({ $0.isLetter || $0.isNumber || $0 == "-" || $0 == "_" }) else { return false }
        if connector == "sqlite" || connector == "duckdb" { return path.hasPrefix("/") }
        if connector == "odbc" { return !(dsn.isEmpty && driver.isEmpty) }
        return !host.isEmpty && !database.isEmpty && Int(port) != nil && !user.isEmpty && !password.isEmpty
    }

    private func source() -> ConfiguredSource {
        var options: [String: JSONValue]
        switch connector {
        case "postgres", "mysql":
            options = ["host": .string(host), "port": .number(Double(Int(port) ?? 0)), "database": .string(database), "user": .string(user), "ssl_mode": .string(sslMode)]
        case "sqlite", "duckdb": options = ["path": .string(path)]
        default:
            options = ["database_type": .string(databaseType)]
            if dsn.isEmpty { options["driver"] = .string(driver) } else { options["dsn"] = .string(dsn) }
        }
        return ConfiguredSource(id: id, name: name, alias: alias, type: connector,
                                databaseType: connector == "odbc" ? databaseType : connector, enabled: true, credentialRef: "", options: options)
    }

    private func credential() -> Data? {
        guard connector == "postgres" || connector == "mysql" || connector == "odbc" else { return nil }
        if connector == "odbc" { return try? JSONEncoder().encode(["username": user, "password": password]) }
        return password.data(using: .utf8)
    }

    private func submit() async {
        working = true
        model.alertMessage = nil
        let configured = source(), secret = credential()
        let action: CredentialAction = secret == nil ? .none : .replace
        let test = SourceMutation(operation: "test", source: configured, sourceID: nil, enabled: nil, expectedRevision: model.revision, credentialAction: action, credential: secret)
        await model.mutate(test)
        guard model.alertMessage == nil else { working = false; return }
        let add = SourceMutation(operation: "add", source: configured, sourceID: nil, enabled: nil, expectedRevision: model.revision, credentialAction: action, credential: secret)
        await model.mutate(add)
        password = ""; working = false
    }

    private func chooseFile() {
        let panel = NSOpenPanel(); panel.canChooseDirectories = false; panel.allowsMultipleSelection = false
        if panel.runModal() == .OK { path = panel.url?.path ?? "" }
    }
    private func clearAndDismiss() { password = ""; dismiss() }
}
