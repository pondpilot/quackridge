import ServiceManagement
import SwiftUI

struct SettingsView: View {
    @EnvironmentObject private var model: AppModel
    @State private var launchAtLogin = SMAppService.mainApp.status == .enabled
    @State private var registrationMessage: String?
    var body: some View {
        Form {
            Section("General") {
                Toggle("Launch QuackRidge at login", isOn: $launchAtLogin).onChange(of: launchAtLogin) { _, enabled in updateLoginItem(enabled) }
                Text("This setting is off by default. macOS may ask you to approve it in System Settings.").font(.caption).foregroundStyle(.secondary)
                if let registrationMessage { Text(registrationMessage).foregroundStyle(.secondary) }
            }
            Section("Locations") {
                LabeledContent("Configuration", value: "~/Library/Application Support/QuackRidge")
                LabeledContent("Logs", value: "~/Library/Logs/QuackRidge")
            }
            Section("Advanced") {
                Text("Token rotation and guarded recovery are available only while the management protocol can enforce local authentication and one-time disclosure.")
                Button("Open Releases Page") { NSWorkspace.shared.open(URL(string: "https://github.com/pondpilot/quackridge/releases")!) }
            }
        }.formStyle(.grouped).padding()
    }
    private func updateLoginItem(_ enabled: Bool) {
        do { if enabled { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }; registrationMessage = nil }
        catch { launchAtLogin = SMAppService.mainApp.status == .enabled; registrationMessage = "macOS did not accept the change. Review Login Items in System Settings." }
    }
}
