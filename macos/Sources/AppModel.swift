import SwiftUI
import AppKit
import Darwin
import UniformTypeIdentifiers

@MainActor
final class AppModel: ObservableObject {
    @Published var name = ""
    @Published var peerID = ""
    @Published var initialized = false
    @Published var ready = false
    @Published var starting = false
    @Published var startupChecked = false
    @Published var damagedProfile = false
    @Published var status: NodeStatus?
    @Published var contacts: [Contact] = []
    @Published var selectedID: String?
    @Published var messages: [ChatEvent] = []
    @Published var transfers: [Transfer] = []
    @Published var drafts: [String: String] = [:]
    @Published var sendingPeers: Set<String> = []
    @Published var busyFiles: Set<String> = []
    @Published var savingFiles: Set<String> = []
    @Published var error: String?
    @Published var connectionError: String?
    @Published var notice: String?
    @Published var showAddContact = false
    @Published var showProfile = false
    @Published var historyLimit = 100
    @Published var downloadDirectory = ""
    @Published var uploadRate: Double = 0
    @Published var downloadRate: Double = 0
    @Published var showQueue = false
    let core: CoreBridge
    let queue: FileQueue
    private let ownsLifecycle: Bool
    private var initialPeer: String?
    private var lastCounters: (Date, Int64, Int64)?
    private var pollTask: Task<Void, Never>?
    private var polling = false
    private var stopping = false
    private var transientMessages: [ChatEvent] = []

    init(profile: URL, core: CoreBridge? = nil, queue: FileQueue? = nil, peer: String? = nil) {
        let bridge = core ?? CoreBridge(profile: profile)
        self.core = bridge
        self.queue = queue ?? FileQueue(core: bridge)
        self.ownsLifecycle = core == nil
        self.initialPeer = peer
    }
    func conversationModel(peer: String) -> AppModel { AppModel(profile: core.profile, core: core, queue: queue, peer: peer) }
    var selectedContact: Contact? { contacts.first { $0.peerID == selectedID } }
    var selectedRoute: Route? { selectedID.flatMap { status?.routes?[$0] } }
    var selectedTransfers: [Transfer] { transfers.filter { $0.peerID == selectedID } }
    var activeTransfers: Int { transfers.filter(\.active).count }
    var draft: String {
        get { drafts[selectedID ?? ""] ?? "" }
        set { if let selectedID { drafts[selectedID] = newValue } }
    }
    var canSend: Bool { ready && selectedID != nil && !sendingPeers.contains(selectedID ?? "") && !draft.isEmpty && draft.utf8.count <= 65536 }

    func bootstrap() async {
        guard !starting, !stopping else { return }
        starting = true
        defer { starting = false; startupChecked = true }
        do {
            guard FileManager.default.isExecutableFile(atPath: core.executable.path) else {
                throw CoreFailure(message: "В приложении не найден исполняемый файл ядра. Распакуйте приложение целиком.")
            }
            guard core.profile.appendingPathComponent("control.sock").path.utf8.count <= 103 else { throw CoreFailure(message: "IPC_PATH_TOO_LONG") }
            let entries = (try? FileManager.default.contentsOfDirectory(atPath: core.profile.path)) ?? []
            if !entries.contains("identity.key") {
                if entries.contains(where: { ![".DS_Store", "node.lock"].contains($0) }) {
                    damagedProfile = true
                    throw CoreFailure(message: "В папке есть данные профиля, но ключ identity.key отсутствует. Новый ID автоматически не создаётся. Восстановите профиль из резервной копии.")
                }
                return
            }
            initialized = true
            if let data = try? Data(contentsOf: core.profile.appendingPathComponent("profile.json")),
               let profile = try? JSONDecoder().decode(ProfileName.self, from: data) { name = profile.name }
            if name.isEmpty { name = "Мой профиль" }
            let identity = try await core.call(["id"]).checked().decode(PeerIdentity.self)
            guard let id = identity.first else { throw CoreFailure(message: "Ядро не вернуло Peer ID.") }
            peerID = id.peerID
            if let data = try? Data(contentsOf: core.profile.appendingPathComponent("ui-pending.json")) {
                transientMessages = (try? JSONDecoder().decode([ChatEvent].self, from: data)) ?? []
            }
            try await connect()
        } catch { self.error = error.localizedDescription; if initialized { connectionError = error.localizedDescription } }
    }
    func createProfile() async {
        guard !starting, !damagedProfile else { return }
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, trimmed.utf8.count <= 128 else { error = Display.error("INVALID_NAME"); return }
        starting = true
        defer { starting = false }
        do {
            _ = try await core.call(["init"]).checked()
            initialized = true
            name = trimmed
            try savePrivate(JSONEncoder().encode(ProfileName(name: name)), named: "profile.json")
            peerID = try await core.call(["id"]).checked().decode(PeerIdentity.self).first?.peerID ?? ""
            try await connect()
            showProfile = true
        } catch { self.error = error.localizedDescription }
    }
    private func connect() async throws {
        if let result = try? await core.call(["status"]).checked().decode(NodeStatus.self), let current = result.first {
            guard current.transfers != nil else { throw CoreFailure(message: "Сейчас работает ядро 0.2. Завершите старое приложение через ⌘Q и нажмите «Перезапустить подключение». Профиль и ID сохранятся; перед обновлением будет создана резервная копия.") }
            status = current
        } else {
            try core.start()
            var connected = false
            for _ in 0..<600 {
                if stopping { return }
                if let result = try? await core.call(["status"]).checked().decode(NodeStatus.self), let current = result.first {
                    status = current; connected = true; break
                }
                if !core.ownsRunningProcess { break }
                try await Task.sleep(nanoseconds: 200_000_000)
            }
            guard connected else { throw CoreFailure(message: core.lastDaemonError.isEmpty ? "Не удалось запустить сетевое ядро." : core.lastDaemonError) }
        }
        ready = true
        connectionError = nil
        try queue.activate()
        do {
            let rows = try await core.call(["download-dir"]).checked().decode(DownloadFolder.self)
            downloadDirectory = rows.first?.path ?? ""
            if downloadDirectory.isEmpty {
                let path = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Downloads/P2PMessenger").path
                downloadDirectory = try await core.call(["--", "download-dir", path]).checked().decode(DownloadFolder.self).first?.path ?? path
            }
        } catch { notice = "Автосохранение требует внимания: " + error.localizedDescription }
        try await loadContacts()
        await refresh()
        startPolling()
    }
    private func startPolling() {
        pollTask?.cancel()
        pollTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: 1_500_000_000)
                guard !Task.isCancelled, let self, !self.stopping else { return }
                await self.refresh()
            }
        }
    }
    func reconnect() async {
        guard !starting, !stopping else { return }
        starting = true
        defer { starting = false }
        do { try await connect() } catch { self.error = error.localizedDescription }
    }
    func refresh() async {
        guard !polling, initialized, !stopping else { return }
        polling = true
        defer { polling = false }
        let peer = selectedID
        do {
            async let state = core.call(["status"])
            async let files = core.call(["files"])
            let newState = try await state.checked().decode(NodeStatus.self).first
            let newFiles = try await files.checked().decode(Transfer.self)
            status = newState
            if let counters = newState?.transfers {
                let now = Date()
                if let previous = lastCounters, now.timeIntervalSince(previous.0) > 0, counters.uploaded >= previous.1, counters.downloaded >= previous.2 {
                    uploadRate = Double(counters.uploaded - previous.1) / now.timeIntervalSince(previous.0)
                    downloadRate = Double(counters.downloaded - previous.2) / now.timeIntervalSince(previous.0)
                } else { uploadRate = 0; downloadRate = 0 }
                lastCounters = (now, counters.uploaded, counters.downloaded)
            }
            transfers = newFiles
            ready = true
            connectionError = nil
            try queue.activate()
            if let peer {
                let events = try await core.call(["history", peer, "--limit", String(historyLimit)]).checked().decode(ChatEvent.self)
                let known = Set(events.map(\.id))
                let previous = transientMessages.count
                transientMessages.removeAll { known.contains($0.id) }
                if previous != transientMessages.count { try persistTransient() }
                if peer == selectedID { messages = events + transientMessages.filter { $0.peerID == peer } }
            }
        } catch {
            // Keep visible history, but never retain a stale connected badge.
            ready = false
            uploadRate = 0; downloadRate = 0; lastCounters = nil
            connectionError = error.localizedDescription
        }
    }
    func select(_ peer: String?) {
        selectedID = peer
        showQueue = false
        messages = []
        historyLimit = 100
        Task { await refreshSelection() }
    }
    private func refreshSelection() async {
        let peer = selectedID
        guard let peer else { return }
        do {
            let events = try await core.call(["history", peer, "--limit", String(historyLimit)]).checked().decode(ChatEvent.self)
            if peer == selectedID { messages = events + transientMessages.filter { $0.peerID == peer && !Set(events.map(\.id)).contains($0.id) } }
        } catch { self.error = error.localizedDescription }
    }
    func loadOlder() async { historyLimit += 100; await refreshSelection() }
    private func loadContacts() async throws {
        contacts = try await core.call(["contacts"]).checked().decode(Contact.self).sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
        if selectedID == nil { select(initialPeer ?? contacts.first?.peerID); initialPeer = nil }
    }
    func addContact(id: String, name: String) async throws {
        // '--' terminates CLI option parsing even for names beginning with '--'.
        _ = try await core.call(["--", "add-contact", id.trimmingCharacters(in: .whitespacesAndNewlines), name.trimmingCharacters(in: .whitespacesAndNewlines)]).checked()
        try await loadContacts()
        select(id.trimmingCharacters(in: .whitespacesAndNewlines))
        showAddContact = false
    }
    func send() async {
        guard canSend, let target = selectedID else { return }
        let text = draft
        drafts[target] = ""
        sendingPeers.insert(target)
        defer { sendingPeers.remove(target) }
        do {
            let result = try await core.call(["send", target, "--stdin"], input: text)
            let delivery = try result.decode(Delivery.self).first
            if let delivery {
                if delivery.status != "delivered" {
                    let event = ChatEvent(messageID: delivery.messageID, peerID: target, direction: "out", status: delivery.status,
                        code: delivery.code, route: nil, message: ChatPayload(text: text, timestamp: ISO8601DateFormatter().string(from: Date())))
                    transientMessages.append(event)
                    try persistTransient()
                }
            } else if result.exitCode != 0 {
                // Without a returned UUID do not silently create a replacement send.
                drafts[target] = text
                throw CoreFailure(message: result.diagnostic)
            }
            if result.exitCode != 0 && delivery == nil { throw CoreFailure(message: result.diagnostic) }
            await refresh()
        } catch {
            self.error = error.localizedDescription
            if drafts[target, default: ""].isEmpty { drafts[target] = text }
        }
    }
    func retry(_ event: ChatEvent) async {
        guard ready, !sendingPeers.contains(event.peerID) else { return }
        sendingPeers.insert(event.peerID)
        defer { sendingPeers.remove(event.peerID) }
        do {
            let result = try await core.call(["retry", event.messageID])
            let delivery = try result.decode(Delivery.self).first
            if delivery == nil { _ = try result.checked() }
            if let index = transientMessages.firstIndex(where: { $0.id == event.id }), let delivery {
                transientMessages[index].status = delivery.status
                transientMessages[index].code = delivery.code
                try persistTransient()
            }
            await refresh()
        } catch { self.error = error.localizedDescription }
    }
    func chooseFile() {
        guard ready, let target = selectedID else { return }
        let panel = NSOpenPanel()
        panel.title = "Добавить файлы и папки в очередь"
        panel.prompt = "Добавить"
        panel.canChooseDirectories = true
        panel.canChooseFiles = true
        panel.allowsMultipleSelection = true
        panel.begin { [weak self] response in
            guard response == .OK else { return }
            Task { @MainActor in await self?.importFiles(panel.urls, to: target) }
        }
    }
    func sendFile(_ url: URL, to peer: String) async {
        await importFiles([url], to: peer)
    }
    func importFiles(_ urls: [URL], to peer: String? = nil) async {
        guard let peer = peer ?? selectedID else { error = "Сначала выберите контакт."; return }
        let hasDirectory = await Task.detached { urls.contains { (try? $0.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) == true } }.value
        if hasDirectory {
            let alert = NSAlert()
            alert.messageText = "Добавить содержимое папок?"
            alert.informativeText = "Все вложенные файлы будут отправлены по отдельности, без структуры папок. Оригинальные байты фото и видео сохраняются. Симлинки пропускаются. Совпадающие имена у получателя получат суффиксы (2), (3) и далее."
            alert.addButton(withTitle: "Добавить файлы")
            alert.addButton(withTitle: "Отмена")
            let response: NSApplication.ModalResponse
            if let window = NSApp.keyWindow {
                response = await withCheckedContinuation { continuation in alert.beginSheetModal(for: window) { continuation.resume(returning: $0) } }
            } else { response = alert.runModal() }
            guard response == .alertFirstButtonReturn else { return }
        }
        showQueue = true
        await queue.stage(urls, peer: peer)
        notice = "Добавлено файлов: \(queue.importCount). Остальные ждут свободного места в очереди."
        await refresh()
    }
    func importProviders(_ providers: [NSItemProvider]) {
        guard let target = selectedID else { error = "Сначала выберите контакт."; return }
        Task {
            var urls: [URL] = []
            for provider in providers where provider.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier) {
                let url: URL? = await withCheckedContinuation { continuation in
                    provider.loadItem(forTypeIdentifier: UTType.fileURL.identifier, options: nil) { value, _ in
                        if let url = value as? URL { continuation.resume(returning: url) }
                        else if let data = value as? Data { continuation.resume(returning: URL(dataRepresentation: data, relativeTo: nil)) }
                        else { continuation.resume(returning: nil) }
                    }
                }
                if let url { urls.append(url) }
            }
            if !urls.isEmpty { await importFiles(urls, to: target) }
        }
    }
    func pasteFiles() -> Bool {
        guard let urls = NSPasteboard.general.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL], !urls.isEmpty else { return false }
        Task { await importFiles(urls) }
        return true
    }
    func batchAction(_ action: String, ids: Set<String>? = nil, peer: String? = nil) async {
        let pause = action == "pause-files"
        queue.setPaused(pause, ids: ids, peer: peer)
        do { transfers = try await core.call(["files"]).checked().decode(Transfer.self) }
        catch { self.error = error.localizedDescription; return }
        let selected = transfers.filter { $0.outgoing && !$0.finished && (ids == nil || ids!.contains($0.fileID)) && (peer == nil || $0.peerID == peer) }.map(\.fileID)
        var failures: [String] = []
        for offset in stride(from: 0, to: selected.count, by: 128) {
            let chunk = Array(selected[offset..<min(offset + 128, selected.count)])
            do {
                let input = String(decoding: try JSONEncoder().encode(chunk), as: UTF8.self)
                let result = try await core.call([action, "--stdin"], input: input).checked().decode(BatchReply.self)
                failures += result.compactMap { $0.error.map(Display.error) }
            } catch { failures.append(error.localizedDescription) }
        }
        if !failures.isEmpty { error = failures.prefix(6).joined(separator: "\n") }
        await refresh()
    }
    func chooseDownloadFolder() {
        let panel = NSOpenPanel()
        panel.title = "Автоматически сохранять входящие файлы в…"
        panel.canChooseFiles = false; panel.canChooseDirectories = true; panel.canCreateDirectories = true
        panel.begin { [weak self] response in
            guard response == .OK, let url = panel.url else { return }
            Task { @MainActor in
                guard let self else { return }
                do {
                    self.downloadDirectory = try await self.core.call(["--", "download-dir", url.path]).checked().decode(DownloadFolder.self).first?.path ?? url.path
                } catch { self.error = error.localizedDescription }
            }
        }
    }
    func fileAction(_ action: String, transfer: Transfer) async {
        guard !busyFiles.contains(transfer.id) else { return }
        busyFiles.insert(transfer.id)
        defer { busyFiles.remove(transfer.id) }
        do {
            _ = try await core.call([action, transfer.fileID]).checked()
            await refresh()
        } catch { self.error = error.localizedDescription }
    }
    func saveFile(_ transfer: Transfer) {
        guard transfer.direction == "in", transfer.status == "stored", !savingFiles.contains(transfer.id) else { return }
        let panel = NSSavePanel()
        panel.title = "Сохранить полученный файл"
        panel.nameFieldStringValue = transfer.name
        panel.canCreateDirectories = true
        panel.begin { [weak self] response in
            guard response == .OK, let destination = panel.url else { return }
            Task { @MainActor in
                guard let self else { return }
                self.savingFiles.insert(transfer.id)
                defer { self.savingFiles.remove(transfer.id) }
                do {
                    let source = URL(fileURLWithPath: transfer.path)
                    guard source.deletingLastPathComponent().standardizedFileURL == self.core.profile.appendingPathComponent("files").standardizedFileURL || (transfer.exportPath == transfer.path && !(transfer.exportPath ?? "").isEmpty) else {
                        throw CoreFailure(message: "Недопустимый путь входящего файла.")
                    }
                    try await Task.detached(priority: .userInitiated) { try FileExport.copy(source: source, destination: destination, expectedSize: transfer.size) }.value
                    self.notice = "Файл сохранён: \(destination.lastPathComponent)"
                } catch { self.error = error.localizedDescription }
            }
        }
    }
    func copyID(_ id: String? = nil) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(id ?? peerID, forType: .string)
        notice = "Peer ID скопирован"
    }
    func stop() async {
        stopping = true
        pollTask?.cancel()
        ready = false
        if ownsLifecycle { queue.suspend(); await core.stop() }
    }
    private func persistTransient() throws { try savePrivate(JSONEncoder().encode(transientMessages), named: "ui-pending.json") }
    private func savePrivate(_ data: Data, named name: String) throws {
        try PrivateJSON.write(data, directory: core.profile, name: name)
    }
}

private struct ProfileName: Codable { let name: String }
private struct DownloadFolder: Decodable { let path: String }
private struct BatchReply: Decodable { let error: String? }

enum FileExport {
    static func copy(source: URL, destination: URL, expectedSize: Int64) throws {
        let sourceFD = open(source.path, O_RDONLY | O_NOFOLLOW | O_NONBLOCK)
        guard sourceFD >= 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO) }
        defer { close(sourceFD) }
        var info = stat()
        guard fstat(sourceFD, &info) == 0, info.st_mode & S_IFMT == S_IFREG, info.st_size == expectedSize else { throw CoreFailure(message: "Полученный файл отсутствует или изменился.") }
        let temp = destination.deletingLastPathComponent().appendingPathComponent(".p2pm-save-" + UUID().uuidString)
        let targetFD = open(temp.path, O_CREAT | O_EXCL | O_WRONLY | O_NOFOLLOW, 0o600)
        guard targetFD >= 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO) }
        defer { close(targetFD); unlink(temp.path) }
        var buffer = [UInt8](repeating: 0, count: 1 << 20)
        var copied: Int64 = 0
        while true {
            let count = read(sourceFD, &buffer, buffer.count)
            if count == 0 { break }
            if count < 0 { if errno == EINTR { continue }; throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO) }
            try buffer.withUnsafeBytes { bytes in
                var offset = 0
                while offset < count {
                    let n = write(targetFD, bytes.baseAddress!.advanced(by: offset), count - offset)
                    if n < 0 && errno == EINTR { continue }
                    guard n > 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO) }
                    offset += n
                }
            }
            copied += Int64(count)
        }
        guard copied == expectedSize else { throw CoreFailure(message: "Полученный файл изменился во время сохранения.") }
        guard fsync(targetFD) == 0, Darwin.rename(temp.path, destination.path) == 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO) }
    }
}
