import SwiftUI

struct OnboardingView: View {
    @Binding var completed: Bool
    @State private var page = 0
    var body: some View {
        VStack(spacing: QRSpace.xl) {
            Spacer()
            Image(systemName: page == 0 ? "mountain.2.fill" : page == 1 ? "lock.shield.fill" : "link.circle.fill")
                .font(.system(size: 72)).foregroundStyle(.tint).accessibilityHidden(true)
            Text(titles[page]).font(.largeTitle.bold())
            Text(messages[page]).multilineTextAlignment(.center).foregroundStyle(.secondary).frame(maxWidth: 480)
            Spacer()
            HStack { if page > 0 { Button("Back") { page -= 1 } }; Spacer(); Button(page == 2 ? "Get Started" : "Continue") { if page == 2 { completed = true } else { page += 1 } }.buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction) }
        }.padding(QRSpace.xl).frame(minWidth: 700, minHeight: 480)
    }
    private let titles = ["Your local database bridge", "Secrets stay in Keychain", "Connect to PondPilot"]
    private let messages = ["QuackRidge runs on this Mac and exposes only a local Quack endpoint. It does not upload database contents.", "Database passwords are handled by the Go backend and stored in your login Keychain. Use dedicated read-only database accounts.", "Add a source, then create a short-lived pairing challenge. The long-lived Quack token never enters the normal app interface."]
}
