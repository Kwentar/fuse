import Foundation
import SwiftUI
import UniformTypeIdentifiers

struct StagedFile: Codable, Identifiable {
    let id: String
    let batchID: String
    let peerID: String
    let path: String
    let size: Int64
    var paused = false
    var error: String?
    var name: String { URL(fileURLWithPath: path).lastPathComponent }
}
struct ImportFile: Sendable { let path: String; let size: Int64 }
struct FileScan: Sendable { var files: [ImportFile] = []; var errors: [String] = [] }

enum FileEnumeration {
    static func scan(_ urls: [URL]) -> FileScan {
        var result = FileScan()
        var visited = Set<String>()
        let keys: Set<URLResourceKey> = [.isDirectoryKey, .isSymbolicLinkKey, .isRegularFileKey, .fileSizeKey]
        func add(_ url: URL) {
            do {
                let values = try url.resourceValues(forKeys: keys)
                if values.isSymbolicLink == true { result.errors.append("\(url.lastPathComponent): симлинк пропущен"); return }
                guard values.isRegularFile == true else { return }
                let path = url.standardizedFileURL.path
                guard visited.insert(path).inserted else { return }
                result.files.append(ImportFile(path: path, size: Int64(values.fileSize ?? 0)))
            } catch { result.errors.append("\(url.lastPathComponent): \(error.localizedDescription)") }
        }
        for url in urls {
            do {
                let values = try url.resourceValues(forKeys: keys)
                if values.isSymbolicLink == true { result.errors.append("\(url.lastPathComponent): симлинк пропущен"); continue }
                if values.isDirectory == true {
                    guard let enumerator = FileManager.default.enumerator(at: url, includingPropertiesForKeys: Array(keys), options: [], errorHandler: { path, error in
                        result.errors.append("\(path.lastPathComponent): \(error.localizedDescription)"); return true
                    }) else { result.errors.append("Не удалось прочитать папку \(url.lastPathComponent)"); continue }
                    for case let file as URL in enumerator { add(file) }
                } else { add(url) }
            } catch { result.errors.append("\(url.lastPathComponent): \(error.localizedDescription)") }
        }
        return result
    }
}

@MainActor
final class FileQueue: ObservableObject {
    @Published private(set) var staged: [StagedFile] = []
    @Published var issues: [String] = []
    @Published private(set) var scanning = false
    @Published private(set) var submitting = false
    @Published private(set) var importCount = 0
    let core: CoreBridge
    private var loaded = false
    private var enabled = false
    private var worker: Task<Void, Never>?
    private var inFlight: String?

    init(core: CoreBridge) { self.core = core }
    func activate() throws {
        if !loaded {
            let path = core.profile.appendingPathComponent("macos-staging.json")
            if FileManager.default.fileExists(atPath: path.path) {
                staged = try JSONDecoder().decode([StagedFile].self, from: Data(contentsOf: path))
            }
            loaded = true
        }
        enabled = true
        kick()
    }
    func suspend() { enabled = false; worker?.cancel() }
    func stage(_ urls: [URL], peer: String) async {
        scanning = true
        defer { scanning = false }
        let scan = await Task.detached(priority: .userInitiated) { FileEnumeration.scan(urls) }.value
        issues.append(contentsOf: scan.errors)
        let batch = UUID().uuidString.lowercased()
        let rows = scan.files.map { StagedFile(id: UUID().uuidString.lowercased(), batchID: batch, peerID: peer, path: $0.path, size: $0.size) }
        staged.append(contentsOf: rows)
        do {
            try persist() // UUIDs are durable before any potentially uncertain IPC call.
            importCount = rows.count
            kick()
        } catch {
            staged.removeAll { $0.batchID == batch }
            issues.append("Не удалось сохранить очередь: \(error.localizedDescription)")
        }
    }
    func setPaused(_ paused: Bool, ids: Set<String>? = nil, peer: String? = nil) {
        for index in staged.indices where (ids == nil || ids!.contains(staged[index].id)) && (peer == nil || staged[index].peerID == peer) {
            staged[index].paused = paused
            if !paused { staged[index].error = nil }
        }
        do { try persist(); kick() } catch { issues.append(error.localizedDescription) }
    }
    private func kick() {
        guard enabled, worker == nil else { return }
        worker = Task { [weak self] in
            guard let self else { return }
            self.submitting = true
            defer { self.worker = nil; self.submitting = false }
            while self.enabled && !Task.isCancelled {
                guard let item = self.staged.first(where: { !$0.paused && $0.error == nil }) else { break }
                self.inFlight = item.id
                do {
                    let reply = try await self.core.call(["--", "queue-file", item.peerID, item.path, item.id]).checked()
                    let record = try reply.decode(Transfer.self).first
                    guard record?.fileID == item.id else { throw CoreFailure(message: "IPC_RESULT_UNKNOWN") }
                    if self.staged.first(where: { $0.id == item.id })?.paused == true {
                        _ = try await self.core.call(["pause-file", item.id]).checked()
                    }
                    let old = self.staged
                    self.staged.removeAll { $0.id == item.id }
                    do { try self.persist() } catch { self.staged = old; throw error }
                } catch {
                    if let failure = error as? CoreFailure, failure.message.contains("IPC_RESULT_UNKNOWN") || failure.message.contains("NODE_NOT_RUNNING") {
                        self.inFlight = nil
                        break // Next successful poll retries the exact durable UUID.
                    }
                    if let index = self.staged.firstIndex(where: { $0.id == item.id }) {
                        self.staged[index].error = error.localizedDescription
                        do { try self.persist() } catch { self.issues.append(error.localizedDescription); break }
                    }
                }
                self.inFlight = nil
                await Task.yield()
            }
        }
    }
    private func persist() throws {
        try PrivateJSON.write(JSONEncoder().encode(staged), directory: core.profile, name: "macos-staging.json")
    }
}

enum PrivateJSON {
    static func write(_ data: Data, directory: URL, name: String) throws {
        let temp = directory.appendingPathComponent(".ui-" + UUID().uuidString)
        guard FileManager.default.createFile(atPath: temp.path, contents: nil, attributes: [.posixPermissions: 0o600]) else { throw CoreFailure(message: "STORAGE_ERROR") }
        defer { try? FileManager.default.removeItem(at: temp) }
        let handle = try FileHandle(forWritingTo: temp)
        defer { try? handle.close() }
        try handle.write(contentsOf: data)
        try handle.synchronize()
        guard rename(temp.path, directory.appendingPathComponent(name).path) == 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO) }
        let fd = open(directory.path, O_RDONLY)
        if fd >= 0 { defer { close(fd) }; guard fsync(fd) == 0 else { throw POSIXError(POSIXErrorCode(rawValue: errno) ?? .EIO) } }
    }
}
