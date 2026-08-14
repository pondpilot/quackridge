import SwiftUI

struct SourcesView: View {
    @EnvironmentObject private var model: AppModel
    @State private var search = ""
    @State private var pendingRemoval: ConfiguredSource?
    var body: some View {
        Group {
            if model.configuration.sources.isEmpty {
                ContentUnavailableView("No Sources", systemImage: "cylinder.split.1x2", description: Text("Add a read-only database connection to make it available in PondPilot."), actions: {
                    Button("Add Source") { model.showingSourceWizard = true }.buttonStyle(.borderedProminent)
                })
            } else {
                List(filtered) { source in
                    HStack {
                        Image(systemName: symbol(source.type)).foregroundStyle(.tint)
                        VStack(alignment: .leading) { Text(source.name); Text("\(source.type) · \(source.alias)").font(.caption).foregroundStyle(.secondary) }
                        Spacer()
                        Toggle("Enabled", isOn: Binding(get: { source.enabled }, set: { value in Task { await model.setEnabled(source, enabled: value) } }))
                            .labelsHidden().toggleStyle(.switch).accessibilityLabel("Enable \(source.name)")
                        Menu { Button("Remove Source…", role: .destructive) { pendingRemoval = source } } label: { Image(systemName: "ellipsis.circle") }.menuStyle(.borderlessButton)
                    }.accessibilityIdentifier("source-row-\(source.id)")
                }.searchable(text: $search)
            }
        }
        .navigationTitle("Sources")
        .toolbar { Button { model.showingSourceWizard = true } label: { Label("Add Source", systemImage: "plus") } }
        .confirmationDialog("Remove \(pendingRemoval?.name ?? "source")?", isPresented: Binding(get: { pendingRemoval != nil }, set: { if !$0 { pendingRemoval = nil } })) {
            Button("Remove Source and Keychain Credential", role: .destructive) { if let source = pendingRemoval { Task { await model.remove(source) } }; pendingRemoval = nil }
            Button("Cancel", role: .cancel) { pendingRemoval = nil }
        } message: { Text("This removes the source configuration and its stored credential. The database itself is not changed.") }
    }
    private var filtered: [ConfiguredSource] {
        search.isEmpty ? model.configuration.sources : model.configuration.sources.filter { $0.name.localizedCaseInsensitiveContains(search) || $0.id.localizedCaseInsensitiveContains(search) }
    }
    private func symbol(_ type: String) -> String { type == "sqlite" || type == "duckdb" ? "doc.badge.gearshape" : "cylinder" }
}
