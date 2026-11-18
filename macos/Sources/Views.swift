import SwiftUI
import AppKit
import UniformTypeIdentifiers

private let accent = Color(red: 0.12, green: 0.46, blue: 0.40)

struct RootView: View {
    @EnvironmentObject var model: AppModel
    @State private var dropTarget = false
    var body: some View {
        Group {
            if model.initialized { MessengerView() }
            else { WelcomeView() }
        }
        .tint(accent)
        .frame(minWidth: 920, minHeight: 640)
        .alert("Не удалось завершить действие", isPresented: Binding(get: { model.error != nil }, set: { if !$0 { model.error = nil } })) {
            Button("Понятно") { model.error = nil }
        } message: { Text(model.error ?? "") }
        .sheet(isPresented: $model.showAddContact) { AddContactView() }
        .sheet(isPresented: $model.showProfile) { ProfileView() }
        .task { await model.bootstrap() }
        .onDrop(of: [UTType.fileURL], isTargeted: $dropTarget) { providers in model.importProviders(providers); return true }
        .overlay { if dropTarget { RoundedRectangle(cornerRadius: 14).strokeBorder(accent, lineWidth: 3).padding(5).allowsHitTesting(false) } }
        .onPasteCommand(of: [UTType.fileURL]) { model.importProviders($0) }
        .onReceive(NSWorkspace.shared.notificationCenter.publisher(for: NSWorkspace.didWakeNotification)) { _ in
            model.notice = "Mac проснулся. Обновляем соединения…"
            Task { await model.refresh() }
        }
    }
}

struct BrandMark: View {
    var size: CGFloat = 56
    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: size * 0.28).fill(accent.gradient)
            Image(systemName: "bubble.left.and.bubble.right.fill")
                .font(.system(size: size * 0.46, weight: .semibold)).foregroundStyle(.white)
        }.frame(width: size, height: size)
    }
}

struct WelcomeView: View {
    @EnvironmentObject var model: AppModel
    var body: some View {
        HStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 28) {
                HStack(spacing: 12) { BrandMark(size: 42); Text("Fuse").font(.title3.weight(.semibold)) }
                Spacer()
                Text("Ближе.\nБез посредников.")
                    .font(.system(size: 43, weight: .semibold, design: .rounded)).tracking(-1.5)
                Text("Переписка и большие файлы\nнапрямую между вашими устройствами.")
                    .font(.title3).foregroundStyle(.secondary).lineSpacing(5)
                VStack(alignment: .leading, spacing: 16) {
                    FeatureRow(icon: "lock.shield", text: "Защищённое соединение Noise")
                    FeatureRow(icon: "arrow.up.doc", text: "Файлы без лимита общего размера")
                    FeatureRow(icon: "desktopcomputer", text: "Windows и Android · протокол 2.1")
                }.padding(.top, 6)
                Spacer()
                Text("LAN → прямой канал → relay")
                    .font(.caption.weight(.medium)).foregroundStyle(accent)
            }.padding(42).frame(maxWidth: .infinity).background(accent.opacity(0.06))
            VStack(alignment: .leading, spacing: 20) {
                Text("Ваше пространство\nдля общения").font(.system(size: 29, weight: .semibold)).tracking(-0.6)
                Text("Начните с локального профиля.\nБез почты, пароля и регистрации на сервере.")
                    .foregroundStyle(.secondary).lineSpacing(4)
                VStack(alignment: .leading, spacing: 8) {
                    Text("Как вас называть?").font(.callout.weight(.medium))
                    TextField("Ваше имя", text: $model.name).textFieldStyle(.roundedBorder).controlSize(.large)
                        .onSubmit { Task { await model.createProfile() } }
                    Text("Имя хранится только на этом Mac.").font(.caption).foregroundStyle(.secondary)
                }.padding(.top, 8)
                Button { Task { await model.createProfile() } } label: {
                    HStack { Spacer(); if model.starting { ProgressView().controlSize(.small) }; Text("Создать ID"); Image(systemName: "arrow.right"); Spacer() }.padding(.vertical, 6)
                }.buttonStyle(.borderedProminent).controlSize(.large)
                    .disabled(model.starting || !model.startupChecked || model.damagedProfile || model.name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                if model.damagedProfile {
                    Label("Профиль требует восстановления. Ключ не заменён.", systemImage: "exclamationmark.triangle").font(.callout).foregroundStyle(.orange)
                } else {
                    Label("Ваш ID останется с вами после перезапуска.", systemImage: "key.horizontal")
                        .font(.caption).foregroundStyle(.secondary)
                }
            }.padding(48).frame(maxWidth: .infinity)
        }
    }
}
private struct FeatureRow: View {
    let icon: String
    let text: String
    var body: some View { Label(text, systemImage: icon).font(.callout).foregroundStyle(.secondary) }
}

struct MessengerView: View {
    @EnvironmentObject var model: AppModel
    @Environment(\.openWindow) private var openWindow
    @State private var search = ""
    var filtered: [Contact] { search.isEmpty ? model.contacts : model.contacts.filter { $0.name.localizedCaseInsensitiveContains(search) || $0.peerID.contains(search) } }
    var body: some View {
        NavigationSplitView {
            VStack(spacing: 0) {
                HStack(spacing: 10) {
                    BrandMark(size: 32)
                    Text("Fuse").font(.headline)
                    Spacer()
                }.padding(.horizontal, 16).padding(.top, 14).padding(.bottom, 22)
                HStack {
                    Text("СООБЩЕНИЯ").font(.system(size: 10, weight: .semibold)).tracking(1.3).foregroundStyle(.secondary)
                    Spacer()
                    Button { model.showAddContact = true } label: { Image(systemName: "plus") }.buttonStyle(.plain)
                        .help("Добавить контакт по Peer ID").accessibilityLabel("Добавить контакт").disabled(!model.ready)
                }.padding(.horizontal, 18)
                HStack(spacing: 6) {
                    Image(systemName: "magnifyingglass").foregroundStyle(.tertiary)
                    TextField("Найти контакт", text: $search).textFieldStyle(.plain)
                }.padding(8).background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8)).padding(12)
                if filtered.isEmpty {
                    VStack(spacing: 10) {
                        Image(systemName: "person.2").font(.title2).foregroundStyle(.tertiary)
                        Text(search.isEmpty ? "Здесь появятся ваши контакты" : "Ничего не найдено").font(.callout).foregroundStyle(.secondary).multilineTextAlignment(.center)
                        if search.isEmpty { Button("Добавить по ID") { model.showAddContact = true }.disabled(!model.ready) }
                    }.padding(24)
                    Spacer()
                } else {
                    List(selection: Binding(get: { model.selectedID }, set: { model.select($0) })) {
                        ForEach(filtered) { contact in
                            HStack(spacing: 10) {
                                Avatar(name: contact.name, size: 38)
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(contact.name).font(.system(size: 13, weight: .semibold)).lineLimit(1)
                                    Text(Display.shortID(contact.peerID)).font(.system(size: 10, design: .monospaced)).foregroundStyle(.secondary)
                                }
                                Spacer(minLength: 0)
                            }.padding(.vertical, 6).tag(contact.peerID)
                                .contextMenu {
                                    Button("Открыть чат в отдельном окне") { openWindow(id: "conversation", value: contact.peerID) }
                                    Button("Копировать Peer ID") { model.copyID(contact.peerID) }
                                }
                        }
                    }.listStyle(.sidebar)
                }
                Divider()
                Button { model.showQueue.toggle() } label: {
                    HStack { Label("Очередь файлов", systemImage: "tray.full"); Spacer(); Text(String(model.transfers.filter { !$0.finished }.count + model.queue.staged.count)).monospacedDigit() }.padding(14)
                }.buttonStyle(.plain)
                Divider()
                Button { model.showProfile = true } label: {
                    HStack(spacing: 10) {
                        Avatar(name: model.name, size: 34)
                        VStack(alignment: .leading, spacing: 4) {
                            Text(model.name).font(.callout.weight(.semibold)).foregroundStyle(.primary).lineLimit(1)
                            HStack(spacing: 5) {
                                Circle().fill(model.ready && model.status?.state == "online" ? accent : .orange).frame(width: 6, height: 6)
                                Text(model.ready ? (model.status?.state == "online" ? "Сеть готова" : "Ограниченная связность") : "Ядро не подключено")
                                    .font(.system(size: 10)).foregroundStyle(.secondary)
                            }
                        }
                        Spacer()
                        Image(systemName: "ellipsis").foregroundStyle(.secondary)
                    }.padding(16).contentShape(Rectangle())
                }.buttonStyle(.plain)
            }.navigationSplitViewColumnWidth(min: 235, ideal: 260, max: 320)
        } detail: {
            VStack(spacing: 0) {
                if let issue = model.connectionError {
                    HStack {
                        Image(systemName: "exclamationmark.triangle")
                        Text(issue).font(.caption).lineLimit(2)
                        Spacer()
                        Button("Перезапустить подключение") { Task { await model.reconnect() } }.disabled(model.starting)
                    }.padding(12).background(.orange.opacity(0.1))
                }
                if model.showQueue {
                    HStack { Button("Вернуться в чат") { model.showQueue = false }; Spacer() }.padding(.horizontal, 20).padding(.top, 10)
                    QueueView(queue: model.queue)
                }
                else if model.selectedContact != nil { ConversationView() }
                else { EmptyConversationView() }
                if let notice = model.notice {
                    HStack {
                        Image(systemName: "info.circle")
                        Text(notice).font(.caption)
                        Spacer()
                        Button { model.notice = nil } label: { Image(systemName: "xmark") }.buttonStyle(.plain).help("Закрыть уведомление")
                    }.foregroundStyle(.secondary).padding(.horizontal, 20).padding(.vertical, 9).background(.quaternary.opacity(0.3))
                }
            }.background(Color(nsColor: .textBackgroundColor))
        }
        .toolbar {
            ToolbarItem(placement: .automatic) {
                Button { model.showProfile = true } label: { Label("Мой ID", systemImage: "person.crop.circle") }
            }
        }
    }
}

struct Avatar: View {
    let name: String
    var size: CGFloat = 36
    var body: some View {
        Text(String(name.prefix(1)).uppercased())
            .font(.system(size: size * 0.38, weight: .semibold, design: .rounded))
            .foregroundStyle(accent).frame(width: size, height: size)
            .background(accent.opacity(0.12), in: RoundedRectangle(cornerRadius: size * 0.32))
            .accessibilityHidden(true)
    }
}

struct EmptyConversationView: View {
    @EnvironmentObject var model: AppModel
    var body: some View {
        VStack(spacing: 20) {
            Spacer()
            ZStack {
                Circle().fill(accent.opacity(0.04)).frame(width: 170, height: 170)
                Circle().strokeBorder(accent.opacity(0.1), style: StrokeStyle(lineWidth: 1, dash: [4, 7])).frame(width: 145, height: 145)
                BrandMark(size: 72)
            }
            Text("Хороший разговор начинается с ID").font(.title2.weight(.semibold))
            Text("Поделитесь своим ID и добавьте собеседника.\nДля файлов и поиска в локальной сети\nвы должны добавить друг друга.")
                .multilineTextAlignment(.center).foregroundStyle(.secondary).lineSpacing(5)
            HStack(spacing: 12) {
                Button("Копировать мой ID") { model.copyID() }.disabled(model.peerID.isEmpty)
                Button("Добавить контакт") { model.showAddContact = true }.buttonStyle(.borderedProminent).disabled(!model.ready)
            }.controlSize(.large).padding(.top, 8)
            Spacer()
            Label("Закрытие окна не останавливает приём. Выход — ⌘Q.", systemImage: "info.circle")
                .font(.caption).foregroundStyle(.tertiary).padding(.bottom, 28)
        }.frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

struct ConversationView: View {
    @EnvironmentObject var model: AppModel
    @State private var tab = "chat"
    @Environment(\.openWindow) private var openWindow
    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                Avatar(name: model.selectedContact?.name ?? "", size: 40)
                VStack(alignment: .leading, spacing: 5) {
                    Text(model.selectedContact?.name ?? "").font(.title3.weight(.semibold))
                    HStack(spacing: 5) {
                        Circle().fill(model.ready && model.selectedRoute?.connected == true ? accent : .secondary.opacity(0.5)).frame(width: 6, height: 6)
                        Text(model.ready ? (model.selectedRoute?.label ?? "Соединение ещё не установлено") : "Состояние соединения неизвестно")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
                Spacer()
                Picker("Вид", selection: $tab) {
                    Text("Чат").tag("chat")
                    Text("Очередь").tag("files")
                }.pickerStyle(.segmented).frame(width: 170)
                Button { if let id = model.selectedID { model.copyID(id) } } label: { Image(systemName: "doc.on.doc") }
                    .buttonStyle(.plain).help("Копировать ID собеседника")
                Button { if let id = model.selectedID { openWindow(id: "conversation", value: id) } } label: { Image(systemName: "macwindow.on.rectangle") }
                    .buttonStyle(.plain).help("Открыть отдельное окно чата")
            }.padding(.horizontal, 24).padding(.vertical, 17)
            Divider()
            if tab == "chat" { chatTimeline } else { QueueView(queue: model.queue, peer: model.selectedID) }
            Divider()
            composer
        }
    }
    private var chatTimeline: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 16) {
                    Label("Доставлено = сохранено получателем, ещё не прочитано", systemImage: "checkmark.shield")
                        .font(.system(size: 10)).foregroundStyle(.tertiary).padding(.vertical, 8)
                    if model.messages.count >= model.historyLimit {
                        Button("Загрузить более ранние сообщения") { Task { await model.loadOlder() } }.font(.caption)
                    }
                    if model.messages.isEmpty && model.selectedTransfers.isEmpty {
                        VStack(spacing: 12) {
                            Image(systemName: "bubble.left").font(.system(size: 34, weight: .light)).foregroundStyle(accent.opacity(0.5))
                            Text("Пока тихо").font(.headline)
                            Text("Напишите первое сообщение или отправьте файл.").font(.callout).foregroundStyle(.secondary)
                        }.frame(maxWidth: .infinity).padding(.vertical, 60)
                    }
                    ForEach(timeline) { item in
                        if let message = item.message { MessageBubble(event: message) }
                        if let file = item.file { FileCard(transfer: file) }
                    }
                    if model.sendingPeers.contains(model.selectedID ?? "") {
                        HStack { Spacer(); ProgressView().controlSize(.small); Text("Отправляется…").font(.caption).foregroundStyle(.secondary) }
                    }
                    Color.clear.frame(height: 1).id("bottom")
                }.padding(.horizontal, 24).padding(.vertical, 12)
            }
            .onChange(of: model.selectedID) { proxy.scrollTo("bottom", anchor: .bottom) }
            .onChange(of: timeline.last?.id) { proxy.scrollTo("bottom", anchor: .bottom) }
        }
    }
    private var timeline: [TimelineItem] {
        (model.messages.map { TimelineItem(id: $0.id, time: $0.message.timestamp, message: $0, file: nil) }
         + model.selectedTransfers.map { TimelineItem(id: $0.id, time: $0.time, message: nil, file: $0) })
            .sorted { $0.time < $1.time }
    }
    private var fileList: some View {
        ScrollView {
            LazyVStack(spacing: 16) {
                HStack {
                    Text("Передачи").font(.headline)
                    Spacer()
                    Text("До 2 исходящих одновременно").font(.caption).foregroundStyle(.secondary)
                }.padding(.vertical, 8)
                if model.selectedTransfers.isEmpty {
                    ContentUnavailableView("Файлов пока нет", systemImage: "doc", description: Text("Выберите файл кнопкой со скрепкой."))
                }
                ForEach(model.selectedTransfers) { FileCard(transfer: $0, wide: true) }
            }.padding(24)
        }
    }
    private var composer: some View {
        VStack(spacing: 10) {
            MessageEditor(text: Binding(get: { model.draft }, set: { model.draft = $0 }), onFiles: { model.pasteFiles() })
                .font(.system(size: 14)).scrollContentBackground(.hidden)
                .frame(minHeight: 55, maxHeight: 100).padding(8)
                .background(.quaternary.opacity(0.3), in: RoundedRectangle(cornerRadius: 10))
                .overlay(alignment: .topLeading) {
                    if model.draft.isEmpty { Text("Написать сообщение…").font(.system(size: 14)).foregroundStyle(.tertiary).padding(.horizontal, 13).padding(.vertical, 16).allowsHitTesting(false) }
                }
                .accessibilityLabel("Текст сообщения")
            HStack(spacing: 12) {
                Button { model.chooseFile() } label: { Label("Файл", systemImage: "paperclip") }
                    .buttonStyle(.plain).disabled(!model.ready).help("Добавить файлы и папки в очередь")
                Spacer()
                if model.draft.utf8.count > 60000 {
                    Text("\(model.draft.utf8.count) / 65 536 байт").font(.caption).foregroundStyle(model.draft.utf8.count > 65536 ? .red : .secondary)
                } else {
                    Text("⌘↩ отправить · ↩ новая строка").font(.system(size: 10)).foregroundStyle(.tertiary)
                }
                Button { Task { await model.send() } } label: { Label("Отправить", systemImage: "arrow.up") }
                    .buttonStyle(.borderedProminent).keyboardShortcut(.return, modifiers: .command).disabled(!model.canSend)
            }
        }.padding(.horizontal, 24).padding(.vertical, 16)
    }
}
private struct TimelineItem: Identifiable {
    let id: String
    let time: String
    let message: ChatEvent?
    let file: Transfer?
}

struct MessageBubble: View {
    @EnvironmentObject var model: AppModel
    let event: ChatEvent
    var body: some View {
        HStack {
            if event.outgoing { Spacer(minLength: 70) }
            VStack(alignment: .leading, spacing: 8) {
                Text(event.message.text).font(.system(size: 14)).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                HStack(spacing: 5) {
                    Text(Display.time(event.message.timestamp))
                    if event.outgoing {
                        Image(systemName: event.status == "delivered" ? "checkmark.circle" : (event.status == "pending" ? "clock" : "exclamationmark.circle"))
                        Text(event.statusLabel)
                    }
                }.font(.system(size: 10)).foregroundStyle(.secondary)
                if event.retryable {
                    if event.status == "delivery_unknown" {
                        Text("Сообщение могло дойти. Повтор сохранит прежний ID.").font(.caption).foregroundStyle(.secondary)
                    }
                    if let code = event.code, !code.isEmpty { Text(Display.error(code)).font(.caption).foregroundStyle(.secondary) }
                    Button("Повторить с тем же ID") { Task { await model.retry(event) } }
                        .font(.caption).disabled(!model.ready || model.sendingPeers.contains(event.peerID))
                }
            }.padding(14)
                .background(event.outgoing ? accent.opacity(0.11) : Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 14))
                .frame(maxWidth: 540, alignment: event.outgoing ? .trailing : .leading)
            if !event.outgoing { Spacer(minLength: 70) }
        }
    }
}

struct FileCard: View {
    @EnvironmentObject var model: AppModel
    let transfer: Transfer
    var wide = false
    var body: some View {
        HStack {
            if transfer.outgoing && !wide { Spacer(minLength: 50) }
            VStack(alignment: .leading, spacing: 12) {
                HStack(alignment: .top, spacing: 12) {
                    Image(systemName: transfer.finished ? "doc.badge.checkmark" : "doc")
                        .font(.system(size: 23, weight: .light)).foregroundStyle(accent)
                        .frame(width: 42, height: 46).background(accent.opacity(0.08), in: RoundedRectangle(cornerRadius: 9))
                    VStack(alignment: .leading, spacing: 5) {
                        Text(transfer.name).font(.callout.weight(.semibold)).lineLimit(2).textSelection(.enabled)
                        Text("\(transfer.outgoing ? "Исходящий" : "Входящий") · \(Display.bytes(transfer.size))")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                    Spacer(minLength: 0)
                    if transfer.finished { Image(systemName: "checkmark.circle.fill").foregroundStyle(accent) }
                }
                if transfer.status == "preparing" {
                    ProgressView().progressViewStyle(.linear)
                } else { ProgressView(value: transfer.liveFraction).progressViewStyle(.linear) }
                HStack {
                    Text(transfer.statusLabel).font(.caption.weight(.medium))
                    Spacer()
                    Text("\(Int(transfer.liveFraction * 100)) % · \(transfer.speed)").font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                }
                Text("\(Display.bytes(transfer.progress)) подтверждено · \(Display.bytes(transfer.liveProgress)) / \(Display.bytes(transfer.size))")
                    .font(.system(size: 10, design: .monospaced)).foregroundStyle(.secondary)
                if let route = transfer.route, !route.isEmpty {
                    Label(Route.label(route), systemImage: route == "relay" ? "server.rack" : "point.3.connected.trianglepath.dotted")
                        .font(.caption).foregroundStyle(.secondary)
                }
                if let error = transfer.error, !error.isEmpty {
                    Text(Display.error(error)).font(.caption).foregroundStyle(transfer.status == "paused" ? .orange : .secondary)
                }
                if let error = transfer.exportError, !error.isEmpty { Text("Ошибка автосохранения: " + Display.error(error)).font(.caption).foregroundStyle(.orange) }
                HStack {
                    if transfer.outgoing && !transfer.finished && transfer.status != "paused" {
                        Button("Пауза") { Task { await model.fileAction("pause-file", transfer: transfer) } }.disabled(!model.ready || model.busyFiles.contains(transfer.id))
                    } else if transfer.outgoing && !transfer.finished {
                        Button("Продолжить") { Task { await model.fileAction("resume-file", transfer: transfer) } }.disabled(!model.ready || model.busyFiles.contains(transfer.id))
                    } else if !transfer.outgoing && transfer.finished {
                        if let path = transfer.exportPath, !path.isEmpty {
                            Button("Показать в Finder") { NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: path)]) }
                        } else { Text("Автосохранение…").font(.caption).foregroundStyle(.secondary) }
                    }
                    Spacer()
                    Text(Display.time(transfer.time)).font(.system(size: 10)).foregroundStyle(.tertiary)
                }
            }.padding(16).frame(maxWidth: wide ? .infinity : 440)
                .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 14))
                .overlay(RoundedRectangle(cornerRadius: 14).strokeBorder(.quaternary, lineWidth: 1))
                .help(transfer.sha256.isEmpty ? "SHA-256 вычисляется" : "SHA-256: " + transfer.sha256)
            if !transfer.outgoing && !wide { Spacer(minLength: 50) }
        }
    }
}

struct AddContactView: View {
    @EnvironmentObject var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var id = ""
    @State private var error: String?
    @State private var saving = false
    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Label("Новый контакт", systemImage: "person.badge.plus").font(.title2.weight(.semibold))
            Text("Обменяйтесь ID и добавьте друг друга. Это нужно для файлов и поиска в локальной сети.").foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 8) {
                Text("Имя контакта").font(.callout.weight(.medium))
                TextField("Например, Алексей", text: $name)
                Text("Peer ID собеседника").font(.callout.weight(.medium)).padding(.top, 8)
                TextField("12D3KooW…", text: $id).font(.system(.body, design: .monospaced))
            }.textFieldStyle(.roundedBorder).controlSize(.large)
            Text("Имя — ваша локальная подпись, а не подтверждённое имя владельца. Повторный ID обновит имя без дубликата.").font(.caption).foregroundStyle(.secondary)
            if let error { Text(error).font(.callout).foregroundStyle(.red) }
            HStack {
                Button("Отмена") { dismiss() }.keyboardShortcut(.cancelAction)
                Spacer()
                if saving { ProgressView().controlSize(.small) }
                Button("Добавить") {
                    saving = true
                    Task {
                        defer { saving = false }
                        do { try await model.addContact(id: id, name: name) } catch { self.error = error.localizedDescription }
                    }
                }.buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction).disabled(saving || name.isEmpty || id.isEmpty)
            }
        }.padding(30).frame(width: 460)
    }
}

struct ProfileView: View {
    @EnvironmentObject var model: AppModel
    @Environment(\.dismiss) private var dismiss
    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            HStack {
                Avatar(name: model.name, size: 48)
                VStack(alignment: .leading, spacing: 4) { Text(model.name).font(.title2.weight(.semibold)); Text("Локальный профиль").foregroundStyle(.secondary) }
                Spacer()
                Button("Готово") { dismiss() }.keyboardShortcut(.cancelAction)
            }
            VStack(alignment: .leading, spacing: 10) {
                Text("ВАШ ПОСТОЯННЫЙ PEER ID").font(.system(size: 10, weight: .semibold)).tracking(1)
                Text(model.peerID.isEmpty ? "ID пока не прочитан" : model.peerID).font(.system(size: 13, design: .monospaced)).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
                Button { model.copyID() } label: { Label("Копировать ID", systemImage: "doc.on.doc") }.disabled(model.peerID.isEmpty)
            }.padding(16).frame(maxWidth: .infinity, alignment: .leading).background(accent.opacity(0.07), in: RoundedRectangle(cornerRadius: 12))
            Text("Отправьте ID собеседнику привычным способом и попросите его ID. Оба приложения должны быть открыты: офлайн-доставки нет.").font(.callout).foregroundStyle(.secondary)
            Divider()
            Text("Соединение с сетью").font(.headline)
            Grid(alignment: .leading, horizontalSpacing: 30, verticalSpacing: 10) {
                info("Bootstrap", model.ready ? model.status?.bootstrap : nil)
                info("DHT", model.ready ? model.status?.dht : nil)
                info("Relay-резервации", model.ready ? String(model.status?.reservations ?? 0) : nil)
                info("Публикация адреса", model.ready ? model.status?.publication : nil)
                info("Ядро", model.core.ownsRunningProcess ? "Запущено приложением" : "Внешний процесс / не запущено")
            }.font(.callout)
            Text("Готовность сети не означает, что выбранный собеседник доступен. Маршрут каждого чата показан отдельно.").font(.caption).foregroundStyle(.secondary)
            if model.status?.config.smartRouting == false {
                Label("Внешнее ядро запущено без smart routing. Перезапустите его с --gui для LAN-поиска.", systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
            }
            if model.status?.config.allowPrivate == true {
                Label("В выбранном профиле разрешены приватные DHT-адреса. Это тестовая конфигурация.", systemImage: "exclamationmark.triangle").font(.caption).foregroundStyle(.orange)
            }
            if let issue = model.status?.lastError, !issue.isEmpty { Text(Display.error(issue)).font(.caption).foregroundStyle(.orange) }
            HStack {
                Button("Перезапустить подключение") { Task { await model.reconnect() } }.disabled(model.starting)
                Spacer()
                Button("Папка профиля") { NSWorkspace.shared.open(model.core.profile) }
            }
            HStack {
                VStack(alignment: .leading) {
                    Text("Автоматическое сохранение входящих").font(.caption.weight(.medium))
                    Text(model.downloadDirectory).font(.system(size: 10)).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                }
                Spacer()
                Button("Папка…") { model.chooseDownloadFolder() }.disabled(!model.ready)
            }
            Text("Закрытие окна оставляет приложение в Dock и продолжает приём. ⌘Q завершает своё ядро и сохраняет прогресс. Внешнее ядро приложение не останавливает.").font(.caption).foregroundStyle(.secondary)
        }.padding(30).frame(width: 500)
    }
    private func info(_ label: String, _ value: String?) -> some View {
        GridRow { Text(label).foregroundStyle(.secondary); Text(localized(value)).textSelection(.enabled) }
    }
    private func localized(_ value: String?) -> String {
        guard let value else { return "Нет данных" }
        return ["connected": "Подключён", "connecting": "Подключение", "ready": "Готов", "starting": "Запуск", "published": "Опубликован", "submitted": "Отправлен в DHT", "empty": "Нет соседей", "unreachable": "Недоступен", "not_attempted": "Ожидание", "degraded": "Ограничено", "failed": "Ошибка", "ok": "Готов" ][value] ?? value
    }
}
