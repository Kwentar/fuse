import Foundation
import Darwin

struct CoreFailure: LocalizedError {
    let message: String
    var errorDescription: String? { Display.error(message) }
}
struct CommandResult {
    let rows: [Data]
    let exitCode: Int32
    let diagnostic: String
    func decode<T: Decodable>(_ type: T.Type) throws -> [T] { try rows.map { try JSONDecoder().decode(type, from: $0) } }
    func checked() throws -> CommandResult {
        if exitCode != 0 { throw CoreFailure(message: diagnostic) }
        return self
    }
}

// Both pipes are consumed concurrently, including for the long-lived daemon.
private final class PipeCollector: @unchecked Sendable {
    private let lock = NSLock()
    private var buffer = Data()
    private(set) var overflow = false
    func drain(_ handle: FileHandle, limit: Int, keepTail: Bool = false) {
        while true {
            let data = handle.availableData
            if data.isEmpty { break }
            lock.lock()
            if buffer.count + data.count <= limit { buffer.append(data) }
            else { overflow = true; if keepTail { buffer.append(data); buffer = buffer.suffix(limit) } }
            lock.unlock()
        }
    }
    var data: Data { lock.lock(); defer { lock.unlock() }; return buffer }
}

@MainActor
final class CoreBridge {
    let profile: URL
    let executable: URL
    private(set) var ownedProcess: Process?
    private var daemonPipes: [Pipe] = []
    private let daemonError = DiagnosticBuffer()

    init(profile: URL, executable: URL? = nil) {
        self.profile = profile
        self.executable = executable ?? Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/messenger")
    }
    var ownsRunningProcess: Bool { ownedProcess?.isRunning == true }
    var lastDaemonError: String { daemonError.text }

    func call(_ arguments: [String], input: String? = nil) async throws -> CommandResult {
        let executable = executable
        let args = ["--data-dir", profile.path, "--json"] + arguments
        return try await Self.execute(executable: executable, arguments: args, input: input)
    }
    nonisolated static func execute(executable: URL, arguments: [String], input: String?) async throws -> CommandResult {
        try await withCheckedThrowingContinuation { continuation in
            DispatchQueue.global(qos: .userInitiated).async {
                let process = Process()
                process.executableURL = executable
                process.arguments = arguments
                let output = Pipe(), error = Pipe(), stdin = Pipe()
                process.standardOutput = output
                process.standardError = error
                process.standardInput = stdin
                do { try process.run() } catch { continuation.resume(throwing: error); return }
                let out = PipeCollector(), err = PipeCollector()
                let group = DispatchGroup()
                group.enter()
                DispatchQueue.global().async { out.drain(output.fileHandleForReading, limit: 96 << 20); group.leave() }
                group.enter()
                DispatchQueue.global().async { err.drain(error.fileHandleForReading, limit: 64 << 10, keepTail: true); group.leave() }
                DispatchQueue.global().async {
                    if let input { try? stdin.fileHandleForWriting.write(contentsOf: Data(input.utf8)) }
                    try? stdin.fileHandleForWriting.close()
                }
                let timeout = DispatchWorkItem { if process.isRunning { process.interrupt() } }
                DispatchQueue.global().asyncAfter(deadline: .now() + 40, execute: timeout)
                process.waitUntilExit()
                timeout.cancel()
                group.wait()
                if out.overflow {
                    continuation.resume(throwing: CoreFailure(message: "Ответ ядра слишком большой. Уменьшите объём истории."))
                    return
                }
                let rows = out.data.split(separator: 10).filter { !$0.isEmpty }.map { Data($0) }
                let stderr = String(decoding: err.data, as: UTF8.self)
                let parsed = stderr.split(separator: "\n").compactMap { line -> String? in
                    (try? JSONSerialization.jsonObject(with: Data(line.utf8)) as? [String: String])?["error"]
                }.joined(separator: "\n")
                continuation.resume(returning: CommandResult(rows: rows, exitCode: process.terminationStatus,
                    diagnostic: parsed.isEmpty ? stderr : parsed))
            }
        }
    }
    func start() throws {
        guard !ownsRunningProcess else { return }
        guard profile.appendingPathComponent("control.sock").path.utf8.count <= 103 else { throw CoreFailure(message: "IPC_PATH_TOO_LONG") }
        let process = Process()
        process.executableURL = executable
        process.arguments = ["--data-dir", profile.path, "--json", "--gui", "run"]
        let output = Pipe(), error = Pipe()
        process.standardOutput = output
        process.standardError = error
        process.standardInput = FileHandle.nullDevice
        daemonError.clear()
        let diagnostic = daemonError
        output.fileHandleForReading.readabilityHandler = { handle in
            if handle.availableData.isEmpty { handle.readabilityHandler = nil }
        }
        error.fileHandleForReading.readabilityHandler = { handle in
            let data = handle.availableData
            if data.isEmpty { handle.readabilityHandler = nil } else { diagnostic.append(data) }
        }
        do { try process.run() }
        catch let launchError {
            output.fileHandleForReading.readabilityHandler = nil
            error.fileHandleForReading.readabilityHandler = nil
            throw launchError
        }
        ownedProcess = process
        daemonPipes = [output, error]
    }
    func stop() async {
        guard let process = ownedProcess else { return }
        if process.isRunning {
            process.interrupt()
            // SIGINT is handled by the Go core: file jobs persist before it exits.
            while process.isRunning { try? await Task.sleep(nanoseconds: 100_000_000) }
        }
        for pipe in daemonPipes { pipe.fileHandleForReading.readabilityHandler = nil }
        daemonPipes = []
        ownedProcess = nil
    }
}

private final class DiagnosticBuffer: @unchecked Sendable {
    private let lock = NSLock()
    private var data = Data()
    func append(_ value: Data) { lock.lock(); defer { lock.unlock() }; data.append(value); if data.count > 8192 { data = data.suffix(8192) } }
    func clear() { lock.lock(); defer { lock.unlock() }; data = Data() }
    var text: String { lock.lock(); defer { lock.unlock() }; return String(decoding: data, as: UTF8.self) }
}
