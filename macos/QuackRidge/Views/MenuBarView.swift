import SwiftUI

struct MenuBarView: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.openWindow) private var openWindow
    var body: some View {
        VStack(alignment: .leading, spacing: QRSpace.md) {
            HStack {
                Image(systemName: model.menuBarSymbol).font(.title2)
                VStack(alignment: .leading) {
                    Text("QuackRidge").font(.headline)
                    Text(model.status?.endpoint ?? "Service unavailable").font(.caption).foregroundStyle(.secondary)
                }
            }
            Divider()
            Label("\(model.configuration.sources.count) sources", systemImage: "cylinder.split.1x2")
            Button("Open QuackRidge") { NSApp.activate(ignoringOtherApps: true); openWindow(id: "manager") }
                .keyboardShortcut("0")
            Button("Refresh") { Task { await model.refresh() } }
            Divider()
            Button("Quit and Stop Service") { Task { await model.quit() } }
        }.padding(QRSpace.md).frame(width: 280)
    }
}
