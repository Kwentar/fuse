import SwiftUI
import AppKit
import UniformTypeIdentifiers

struct QueueView: View {
    @EnvironmentObject var model: AppModel
    @ObservedObject var queue: FileQueue
    var peer: String?
    @State private var selection = Set<String>()
    @State private var filter = "pending"
    @State private var busy = false
    @State private var showIssues = false
    private var records: [Transfer] {
        model.transfers.filter { (peer == nil || $0.peerID == peer) && (filter == "all" || !$0.finished || !($0.exportError ?? "").isEmpty) }
    }
    private var staged: [StagedFile] {
        let submitted = Set(model.transfers.map(\.fileID))
        return queue.staged.filter { (peer == nil || $0.peerID == peer) && !submitted.contains($0.id) }
    }
    var body: some View {
        VStack(spacing: 0) {
            queueHeader
            Divider()
            if queue.scanning {
                HStack { ProgressView().controlSize(.small); Text("Перечисляем файлы в папках…").font(.caption); Spacer() }.padding(16)
            }
            if records.isEmpty && staged.isEmpty && !queue.scanning {
                ContentUnavailableView(filter == "pending" ? "Все передачи завершены" : "Очередь пока пуста", systemImage: "tray", description: Text("Перетащите файлы или папки сюда.\nТакже работают выбор нескольких файлов и ⌘V из Finder."))
                    .frame(maxHeight: .infinity)
            } else {
                List(selection: $selection) {
                    ForEach(staged) { item in
                        StagedQueueRow(item: item).padding(.vertical, 6).tag(item.id)
                    }
                    ForEach(records) { transfer in
                        QueueRow(transfer: transfer, contactName: model.contacts.first { $0.peerID == transfer.peerID }?.name)
                            .padding(.vertical, 6).tag(transfer.fileID)
                    }
                }.listStyle(.inset)
            }
            Divider()
            downloadFooter
            if !queue.issues.isEmpty {
                HStack {
                    Label("Замечания при добавлении: \(queue.issues.count)", systemImage: "exclamationmark.triangle").font(.caption)
                    Spacer()
                    Button("Посмотреть") { showIssues = true }.controlSize(.small)
                }.padding(12).background(.orange.opacity(0.1))
            }
        }
        .sheet(isPresented: $showIssues) {
            VStack(alignment: .leading, spacing: 16) {
                Text("Результат добавления файлов").font(.title2)
                Text("Доступные файлы добавлены в очередь. Эти файлы требуют внимания:").foregroundStyle(.secondary)
                ScrollView { Text(queue.issues.joined(separator: "\n")).font(.callout).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading) }.frame(height: 260)
                HStack { Button("Очистить список") { queue.issues = []; showIssues = false }; Spacer(); Button("Готово") { showIssues = false }.keyboardShortcut(.cancelAction) }
            }.padding(24).frame(width: 540)
        }
    }
    private var queueHeader: some View {
        VStack(spacing: 0) {
            HStack(alignment: .top, spacing: 12) {
                VStack(alignment: .leading, spacing: 6) {
                    Text("Очередь файлов").font(.title2.weight(.semibold))
                    Text("2 отправки одновременно · остальные ждут").font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                VStack(alignment: .trailing, spacing: 5) {
                    Label(Display.rate(model.uploadRate), systemImage: "arrow.up").foregroundStyle(.tint)
                    Label(Display.rate(model.downloadRate), systemImage: "arrow.down").foregroundStyle(.secondary)
                }.font(.caption.monospacedDigit())
            }.padding(20)
            HStack {
                Picker("Показывать", selection: $filter) {
                    Text("Незавершённые").tag("pending")
                    Text("Все файлы").tag("all")
                }.pickerStyle(.segmented).frame(width: 250)
                Spacer()
                Button { model.chooseFile() } label: { Label("Добавить", systemImage: "plus") }.disabled(!model.ready || model.selectedID == nil)
            }.padding(.horizontal, 20).padding(.bottom, 12)
            HStack(spacing: 12) {
                Button(selection.isEmpty ? "Выбрать всё" : "Снять выделение") {
                    selection = selection.isEmpty ? Set(records.map(\.fileID) + staged.map(\.id)) : []
                }
                Button(selection.isEmpty ? "Пауза всей очереди" : "Пауза выбранных") { act("pause-files") }
                Button(selection.isEmpty ? "Продолжить очередь" : "Продолжить выбранные") { act("resume-files") }
                if busy { ProgressView().controlSize(.small) }
                Spacer()
            }.controlSize(.small).padding(.horizontal, 20).padding(.bottom, 12).disabled(busy)
        }
    }
    private var downloadFooter: some View {
            HStack(alignment: .top) {
                Image(systemName: "folder")
                VStack(alignment: .leading, spacing: 4) {
                    Text("Входящие сохраняются автоматически").font(.caption.weight(.medium))
                    Text(model.downloadDirectory.isEmpty ? "Папка ещё не настроена" : model.downloadDirectory)
                        .font(.system(size: 10)).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                }
                Spacer()
                Button("Изменить…") { model.chooseDownloadFolder() }.controlSize(.small).disabled(!model.ready)
            }.padding(16)
    }
    private func act(_ action: String) {
        busy = true
        let ids = selection.isEmpty ? nil : selection
        Task { await model.batchAction(action, ids: ids, peer: peer); busy = false }
    }
}

private struct StagedQueueRow: View {
    let item: StagedFile
    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "doc.badge.ellipsis").font(.title2).foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 5) {
                Text(item.name).font(.callout.weight(.medium)).lineLimit(1)
                Text(item.error ?? (item.paused ? "На паузе до добавления в ядро" : "Ожидает добавления в очередь"))
                    .font(.caption).foregroundStyle(item.error == nil ? Color.secondary : Color.orange).lineLimit(2)
                Text(item.path).font(.system(size: 10)).foregroundStyle(.tertiary).lineLimit(1).truncationMode(.middle)
            }
            Spacer()
            Text(Display.bytes(item.size)).font(.caption.monospacedDigit())
        }
    }
}

private struct QueueRow: View {
    @EnvironmentObject var model: AppModel
    let transfer: Transfer
    let contactName: String?
    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: transfer.outgoing ? "arrow.up.doc" : "arrow.down.doc")
                .font(.title2).foregroundStyle(.tint).frame(width: 30)
            VStack(alignment: .leading, spacing: 5) {
                HStack { Text(transfer.name).font(.callout.weight(.medium)).lineLimit(1); Spacer(); Text(Display.bytes(transfer.size)).font(.caption.monospacedDigit()) }
                ProgressView(value: transfer.liveFraction).progressViewStyle(.linear)
                HStack {
                    Text(transfer.statusLabel)
                    Text("· " + (contactName ?? Display.shortID(transfer.peerID))).lineLimit(1)
                    Spacer()
                    Text(transfer.speed).monospacedDigit()
                    Text("\(Int(transfer.liveFraction * 100)) %").monospacedDigit()
                }.font(.system(size: 10)).foregroundStyle(.secondary)
                HStack {
                    Text("\(Display.bytes(transfer.progress)) подтверждено · \(Display.bytes(transfer.liveProgress)) передано")
                    Spacer()
                    Text(Route.label(transfer.route))
                }.font(.system(size: 10)).foregroundStyle(.tertiary)
                if let error = transfer.exportError, !error.isEmpty {
                    Text("Ошибка сохранения: " + Display.error(error)).font(.caption).foregroundStyle(.orange)
                } else if let error = transfer.error, !error.isEmpty { Text(Display.error(error)).font(.caption).foregroundStyle(.orange).lineLimit(2) }
            }
            if transfer.outgoing && !transfer.finished {
                Button { Task { await model.fileAction(transfer.status == "paused" ? "resume-file" : "pause-file", transfer: transfer) } } label: {
                    Image(systemName: transfer.status == "paused" ? "play.fill" : "pause.fill")
                }.buttonStyle(.borderless).help(transfer.status == "paused" ? "Продолжить" : "Пауза").disabled(!model.ready || model.busyFiles.contains(transfer.id))
            } else if let path = transfer.exportPath, !path.isEmpty {
                Button { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)]) } label: { Image(systemName: "folder") }.buttonStyle(.borderless).help("Показать в Finder")
            }
        }
    }
}

struct MessageEditor: NSViewRepresentable {
    @Binding var text: String
    let onFiles: () -> Bool
    func makeCoordinator() -> Coordinator { Coordinator(self) }
    func makeNSView(context: Context) -> NSScrollView {
        let scroll = NSScrollView()
        scroll.drawsBackground = false
        scroll.hasVerticalScroller = true
        let editor = FileAwareTextView()
        editor.isRichText = false
        editor.importsGraphics = false
        editor.font = .systemFont(ofSize: 14)
        editor.drawsBackground = false
        editor.isVerticallyResizable = true
        editor.isHorizontallyResizable = false
        editor.autoresizingMask = [.width]
        editor.textContainer?.widthTracksTextView = true
        editor.textContainerInset = NSSize(width: 5, height: 7)
        editor.delegate = context.coordinator
        editor.onFiles = onFiles
        editor.setAccessibilityLabel("Текст сообщения")
        scroll.documentView = editor
        return scroll
    }
    func updateNSView(_ scroll: NSScrollView, context: Context) {
        context.coordinator.parent = self
        if let editor = scroll.documentView as? FileAwareTextView {
            if editor.string != text { editor.string = text }
            editor.onFiles = onFiles
        }
    }
    final class Coordinator: NSObject, NSTextViewDelegate {
        var parent: MessageEditor
        init(_ parent: MessageEditor) { self.parent = parent }
        func textDidChange(_ notification: Notification) {
            if let editor = notification.object as? NSTextView { parent.text = editor.string }
        }
    }
}
final class FileAwareTextView: NSTextView {
    var onFiles: (() -> Bool)?
    override func paste(_ sender: Any?) { if onFiles?() != true { super.paste(sender) } }
}
