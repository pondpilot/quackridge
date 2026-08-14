import SwiftUI

struct PairingView: View {
    @EnvironmentObject private var model: AppModel
    @State private var challenge: PairingChallenge?
    @State private var message = "Pair PondPilot without revealing the service token to this app."
    var body: some View {
        VStack(spacing: QRSpace.lg) {
            Image(systemName: "link.circle.fill").font(.system(size: 64)).foregroundStyle(.tint)
            Text("Pair PondPilot").font(.largeTitle.bold())
            Text(message).multilineTextAlignment(.center).foregroundStyle(.secondary).frame(maxWidth: 460)
            if let challenge {
                Text(challenge.nonce).font(.system(.title3, design: .monospaced).weight(.semibold)).textSelection(.enabled)
                Text("Expires \(challenge.expiresAt.formatted(.relative(presentation: .named)))").font(.caption)
                HStack { Button("Copy Challenge") { NSPasteboard.general.clearContents(); NSPasteboard.general.setString(challenge.nonce, forType: .string) }
                    Button("Open PondPilot") { NSWorkspace.shared.open(URL(string: "https://app.pondpilot.io")!) }.buttonStyle(.borderedProminent) }
            } else {
                Button("Create Pairing Challenge") { Task { await begin() } }.buttonStyle(.borderedProminent)
            }
        }.frame(maxWidth: .infinity, maxHeight: .infinity).padding(QRSpace.xl).navigationTitle("Pairing")
            .onReceive(NotificationCenter.default.publisher(for: .pairingInvalidated)) { _ in challenge = nil; message = "The backend identity changed. Pair PondPilot again." }
    }
    private func begin() async {
        do { challenge = try await model.beginPairing(); message = "Enter this one-time challenge in PondPilot. The long-lived token stays inside Go." }
        catch { message = "A pairing challenge could not be created. Try again after checking service health." }
    }
}
