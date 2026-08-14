import SwiftUI

@main
struct QuackRidgeApp: App {
    @StateObject private var model = AppModel.live()

    var body: some Scene {
        MenuBarExtra("QuackRidge", systemImage: model.menuBarSymbol) {
            MenuBarView()
                .environmentObject(model)
        }
        .menuBarExtraStyle(.window)

        WindowGroup("QuackRidge", id: "manager") {
            ManagerView()
                .environmentObject(model)
                .frame(minWidth: 780, minHeight: 520)
                .task { await model.managerBecameVisible() }
                .onDisappear { model.managerBecameHidden() }
        }
        .commands { QuackRidgeCommands(model: model) }

        Settings {
            SettingsView()
                .environmentObject(model)
                .frame(width: 520, height: 330)
        }
    }
}

private struct QuackRidgeCommands: Commands {
    @ObservedObject var model: AppModel
    @Environment(\.openWindow) private var openWindow

    var body: some Commands {
        CommandGroup(after: .appInfo) {
            Button("Open QuackRidge") {
                NSApp.activate(ignoringOtherApps: true)
                openWindow(id: "manager")
            }
            .keyboardShortcut("0")
        }
        CommandGroup(replacing: .appTermination) {
            Button("Quit QuackRidge and Stop Service") {
                Task { await model.quit() }
            }
            .keyboardShortcut("q")
        }
    }
}
