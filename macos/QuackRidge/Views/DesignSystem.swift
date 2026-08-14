import SwiftUI

enum QRSpace { static let xs: CGFloat = 6, sm: CGFloat = 10, md: CGFloat = 16, lg: CGFloat = 24, xl: CGFloat = 32 }

struct StatusBadge: View {
    let text: String
    let healthy: Bool
    var body: some View {
        Label(text, systemImage: healthy ? "checkmark.circle.fill" : "exclamationmark.triangle.fill")
            .font(.caption.weight(.semibold))
            .foregroundStyle(healthy ? .green : .orange)
            .padding(.horizontal, QRSpace.sm).padding(.vertical, QRSpace.xs)
            .background(.quaternary, in: Capsule())
            .accessibilityElement(children: .combine)
    }
}

struct SectionCard<Content: View>: View {
    let title: LocalizedStringKey
    @ViewBuilder let content: Content
    var body: some View {
        VStack(alignment: .leading, spacing: QRSpace.md) {
            Text(title).font(.headline)
            content
        }
        .padding(QRSpace.lg).frame(maxWidth: .infinity, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    }
}
