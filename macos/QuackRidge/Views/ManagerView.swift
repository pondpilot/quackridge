import SwiftUI

struct ManagerView: View {
    @EnvironmentObject private var model: AppModel
    @AppStorage("completedOnboarding") private var completedOnboarding = false
    var body: some View {
        Group {
        if !completedOnboarding { OnboardingView(completed: $completedOnboarding) }
        else { NavigationSplitView {
            List(SidebarDestination.allCases, selection: $model.selection) { destination in
                Label(destination.rawValue, systemImage: destination.symbol).tag(destination)
            }
            .navigationTitle("QuackRidge")
        } detail: {
            Group {
                switch model.selection ?? .overview {
                case .overview: OverviewView()
                case .sources: SourcesView()
                case .pairing: PairingView()
                case .diagnostics: DiagnosticsView()
                }
            }
            .environmentObject(model)
        } }
        }
        .sheet(isPresented: $model.showingSourceWizard) { SourceWizardView().environmentObject(model) }
        .alert("QuackRidge", isPresented: Binding(get: { model.alertMessage != nil }, set: { if !$0 { model.alertMessage = nil } })) {
            Button("OK", role: .cancel) { model.alertMessage = nil }
        } message: { Text(model.alertMessage ?? "") }
    }
}

struct OverviewView: View {
    @EnvironmentObject private var model: AppModel
    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: QRSpace.lg) {
                HStack {
                    VStack(alignment: .leading) {
                        Text("Local database bridge").font(.largeTitle.bold())
                        Text("Read-only access for PondPilot, running on this Mac.").foregroundStyle(.secondary)
                    }
                    Spacer()
                    StatusBadge(text: stateText, healthy: model.lifecycle == .ready)
                }
                SectionCard(title: "Service") {
                    LabeledContent("Endpoint", value: model.status?.endpoint ?? "Not available")
                    LabeledContent("Process", value: model.ownership == .external ? "Managed outside this app" : "Managed by QuackRidge")
                }
                SectionCard(title: "Sources") {
                    LabeledContent("Configured", value: String(model.configuration.sources.count))
                    Button(model.configuration.sources.isEmpty ? "Add your first source" : "Manage sources") {
                        model.selection = .sources
                        if model.configuration.sources.isEmpty { model.showingSourceWizard = true }
                    }.buttonStyle(.borderedProminent)
                }
                SectionCard(title: "Connect PondPilot") {
                    Text("Pair PondPilot without copying the long-lived Quack token into the app.")
                    Button("Open Pairing") { model.selection = .pairing }
                }
            }.padding(QRSpace.xl)
        }.navigationTitle("Overview")
    }
    private var stateText: String {
        switch model.lifecycle { case .stopped: "Stopped"; case .starting: "Starting"; case .ready: "Ready"; case .degraded: "Needs attention"; case .failed: "Failed" }
    }
}
