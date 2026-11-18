import SwiftUI
import AppKit

@main
struct P2PMessengerApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var delegate
    @StateObject private var model: AppModel

    init() {
        let args = CommandLine.arguments
        let profile: URL
        if let index = args.firstIndex(of: "--profile"), args.indices.contains(index + 1) {
            profile = URL(fileURLWithPath: args[index + 1], isDirectory: true).standardizedFileURL
        } else {
            profile = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/P2PMessenger", isDirectory: true)
        }
        _model = StateObject(wrappedValue: AppModel(profile: profile))
    }
    var body: some Scene {
        Window("Fuse", id: "main") {
            RootView().environmentObject(model).onAppear { delegate.model = model }
        }
        .defaultSize(width: 1100, height: 760)
        .windowResizability(.contentMinSize)
        .commands {
            CommandGroup(replacing: .newItem) {
                Button("Добавить контакт…") { model.showAddContact = true }.keyboardShortcut("n").disabled(!model.ready)
                Button("Мой профиль…") { model.showProfile = true }.keyboardShortcut(",").disabled(!model.initialized)
            }
            CommandGroup(replacing: .help) {
                Button("Инструкция Fuse") {
                    if let url = Bundle.main.url(forResource: "Инструкция", withExtension: "html") { NSWorkspace.shared.open(url) }
                }
            }
        }
        WindowGroup("Чат Fuse", id: "conversation", for: String.self) { $peer in
            if let peer { DetachedConversation(root: model, peer: peer) }
        }.defaultSize(width: 800, height: 740)
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    weak var model: AppModel?
    private var terminating = false
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        if !flag { sender.windows.first(where: { $0.canBecomeMain })?.makeKeyAndOrderFront(nil) }
        return true
    }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let model else { return .terminateNow }
        if terminating { return .terminateLater }
        if model.transfers.contains(where: { $0.active || $0.status == "queued" || $0.status == "receiving" }) || !model.queue.staged.isEmpty || model.queue.scanning || model.queue.submitting {
            let alert = NSAlert()
            alert.messageText = "Завершить приложение с незавершёнными передачами?"
            alert.informativeText = "Приём и отправка остановятся. Очередь и подтверждённые данные сохранятся; активные задачи продолжатся при следующем запуске. Чтобы продолжить работу в фоне, просто закройте окно."
            alert.addButton(withTitle: "Оставить работать")
            alert.addButton(withTitle: "Завершить")
            guard alert.runModal() == .alertSecondButtonReturn else { return .terminateCancel }
        }
        terminating = true
        Task {
            await model.stop()
            sender.reply(toApplicationShouldTerminate: true)
        }
        return .terminateLater
    }
}

struct DetachedConversation: View {
    @StateObject private var model: AppModel
    init(root: AppModel, peer: String) { _model = StateObject(wrappedValue: root.conversationModel(peer: peer)) }
    var body: some View {
        RootView().environmentObject(model)
            .onDisappear { Task { await model.stop() } }
    }
}
