import Foundation

struct Contact: Decodable, Identifiable, Equatable {
    let peerID: String
    let name: String
    var id: String { peerID }
    enum CodingKeys: String, CodingKey { case peerID = "peer_id", name }
}

struct Route: Decodable, Equatable {
    let stage: String
    let route: String
    var label: String {
        switch stage {
        case "trying_lan": return "Проверяем локальную сеть"
        case "negotiating": return "Согласуем соединение"
        case "trying_direct": return "Проверяем прямой канал / NAT"
        case "trying_relay": return "Подключаем relay"
        case "unreachable", "disconnected": return "Нет соединения"
        case "connected": return Self.label(route)
        default: return "Соединение ещё не установлено"
        }
    }
    static func label(_ route: String?) -> String {
        switch route {
        case "lan": return "Локальная сеть"
        case "direct": return "Напрямую"
        case "relay": return "Через relay"
        default: return "Маршрут ещё не выбран"
        }
    }
    var connected: Bool { stage == "connected" && ["lan", "direct", "relay"].contains(route) }
}

struct NodeStatus: Decodable {
    let peerID: String
    let state: String
    let bootstrap: String
    let dht: String
    let reservations: Int
    let publication: String
    let lastError: String?
    let connections: Int
    let routes: [String: Route]?
    let config: NodeConfig
    let transfers: TransferCounters?
    enum CodingKeys: String, CodingKey {
        case peerID = "peer_id", lastError = "last_error"
        case state, bootstrap, dht, reservations, publication, connections, routes, config, transfers
    }
}
struct TransferCounters: Decodable {
    let uploaded: Int64
    let downloaded: Int64
}
struct NodeConfig: Decodable {
    let smartRouting: Bool
    let allowPrivate: Bool
    enum CodingKeys: String, CodingKey { case smartRouting = "smart_routing", allowPrivate = "allow_private_addresses" }
}
struct PeerIdentity: Decodable {
    let peerID: String
    enum CodingKeys: String, CodingKey { case peerID = "peer_id" }
}
struct ChatPayload: Codable, Equatable {
    let text: String
    let timestamp: String
}
struct ChatEvent: Codable, Identifiable, Equatable {
    let messageID: String
    let peerID: String
    let direction: String
    var status: String
    var code: String?
    let route: String?
    let message: ChatPayload
    var id: String { direction + ":" + peerID + ":" + messageID }
    var outgoing: Bool { direction == "out" }
    var retryable: Bool { outgoing && ["not_sent", "delivery_unknown", "rejected"].contains(status) }
    var statusLabel: String {
        switch status {
        case "pending": return "Отправляется"
        case "delivered": return "Доставлено"
        case "stored": return "Получено"
        case "delivery_unknown": return "Исход неизвестен"
        case "not_sent": return "Не отправлено"
        case "rejected": return "Отклонено получателем"
        default: return status
        }
    }
    enum CodingKeys: String, CodingKey {
        case messageID = "message_id", peerID = "peer_id"
        case direction, status, code, route, message
    }
}
struct Delivery: Decodable {
    let messageID: String
    let status: String
    let code: String?
    enum CodingKeys: String, CodingKey { case messageID = "message_id", status, code }
}

struct Transfer: Decodable, Identifiable, Equatable {
    let fileID: String
    let peerID: String
    let name: String
    let size: Int64
    let progress: Int64
    let sha256: String
    let direction: String
    let status: String
    let path: String
    let time: String
    let route: String?
    let error: String?
    let transferred: Int64?
    let bytesPerSecond: Double?
    let exportPath: String?
    let exportError: String?
    var id: String { direction + ":" + peerID + ":" + fileID }
    var outgoing: Bool { direction == "out" }
    var finished: Bool { status == "delivered" || status == "stored" }
    var fraction: Double { size > 0 ? min(1, max(0, Double(progress) / Double(size))) : (finished ? 1 : 0) }
    var active: Bool { outgoing && ["preparing", "connecting", "sending", "reconnecting"].contains(status) }
    var liveProgress: Int64 { max(progress, min(size, transferred ?? progress)) }
    var liveFraction: Double { size > 0 ? min(1, max(0, Double(liveProgress) / Double(size))) : (finished ? 1 : 0) }
    var speed: String { Display.rate(bytesPerSecond ?? 0) }
    var statusLabel: String {
        switch status {
        case "queued", "waiting": return "В очереди"
        case "preparing": return "Подготовка · проверяем SHA-256"
        case "connecting": return "Подключение"
        case "sending": return "Передача"
        case "receiving": return "Приём · ожидаем блоки"
        case "reconnecting": return "Переподключение"
        case "paused": return "Приостановлено"
        case "delivered": return "Доставлено"
        case "stored": return "Получено и проверено"
        case "exporting": return "Сохраняем в папку загрузок"
        case "error", "failed": return "Ошибка"
        default: return status
        }
    }
    enum CodingKeys: String, CodingKey {
        case fileID = "file_id", peerID = "peer_id"
        case name, size, progress, sha256, direction, status, path, time, route, error, transferred
        case bytesPerSecond = "bytes_per_second", exportPath = "export_path", exportError = "export_error"
    }
}

enum Display {
    static func rate(_ value: Double) -> String {
        guard value.isFinite, value > 0 else { return "0 КБ/с" }
        return bytes(Int64(min(value, Double(Int64.max / 2)))) + "/с"
    }
    static func bytes(_ value: Int64) -> String {
        ByteCountFormatter.string(fromByteCount: value, countStyle: .binary)
    }
    static func shortID(_ id: String) -> String { id.count > 20 ? String(id.prefix(10)) + "…" + String(id.suffix(8)) : id }
    static func time(_ raw: String) -> String {
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let date = iso.date(from: raw) ?? ISO8601DateFormatter().date(from: raw)
        guard let date else { return "" }
        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "ru_RU")
        formatter.dateFormat = Calendar.current.isDateInToday(date) ? "HH:mm" : "d MMM, HH:mm"
        return formatter.string(from: date)
    }
    static func error(_ raw: String) -> String {
        let messages: [(String, String)] = [
            ("SELF_CONTACT", "Это ваш собственный ID. Укажите ID собеседника."),
            ("INVALID_PEER", "Неверный Peer ID. Скопируйте полный ID собеседника."),
            ("INVALID_NAME", "Введите имя длиной от 1 до 128 байт UTF-8."),
            ("CONTACT_REQUIRED", "Для передачи файлов нужно добавить друг друга в контакты."),
            ("CONTACT_NOT_FOUND", "Собеседник пока не найден. Проверьте ID и подключение."),
            ("RESOURCE_LIMIT", "Ядро занято. Одновременно можно отправлять не более двух файлов."),
            ("FILE_BUSY", "Эта передача уже выполняется."),
            ("FILE_CHANGED", "Исходный файл изменился. Продолжение невозможно; выберите его как новую передачу."),
            ("FILE_ACK_INVALID", "Получено неверное подтверждение. Доставка не подтверждена."),
            ("FILE_NOT_FOUND", "Задача передачи не найдена."),
            ("INVALID_FILE_NAME", "Имя файла содержит недопустимые символы или превышает 255 байт."),
            ("HASH_MISMATCH", "Контрольная сумма не совпала. Файл не принят как готовый."),
            ("INTERRUPTED", "Прервано при завершении приложения. Можно продолжить."),
            ("context canceled", "Передача приостановлена. Подтверждённые данные сохранены."),
            ("NETWORK_RECONNECT", "Восстанавливаем соединение с подтверждённой позиции."),
            ("NETWORK_UNAVAILABLE", "Сеть недоступна. Проверьте соединение и нажмите «Продолжить»."),
            ("IPC_RESULT_UNKNOWN", "Исход операции неизвестен. Проверьте историю; повтор сообщения использует прежний ID."),
            ("NODE_ALREADY_RUNNING", "Профиль уже открыт другим процессом."),
            ("NODE_NOT_RUNNING", "Нет связи с сетевым ядром. Нажмите «Перезапустить подключение»."),
            ("IPC_PATH_TOO_LONG", "Путь профиля слишком длинный для Unix-сокета. Выберите более короткий путь."),
            ("IDENTITY_ERROR", "Не удалось прочитать ключ профиля. ID не изменён. Восстановите профиль из резервной копии."),
            ("PERMISSION_ERROR", "Небезопасные права профиля или нет доступа к файлу."),
            ("unsafe file permissions", "Небезопасные права профиля. Каталог должен иметь права 0700, файлы — 0600."),
            ("STORAGE_ERROR", "Ошибка сохранения. Проверьте свободное место и права доступа."),
            ("no space left", "Недостаточно места на диске. Освободите место и продолжите передачу."),
            ("permission denied", "Нет доступа к файлу или папке. Проверьте разрешения macOS."),
            ("operation not permitted", "macOS запретила доступ. Проверьте разрешения для приложения в Системных настройках."),
            ("no such file", "Файл не найден. Возможно, он был перемещён или удалён."),
            ("ACK_TIMEOUT", "Подтверждение не получено. Сообщение могло быть доставлено; повторите его с прежним ID."),
            ("NO_USABLE_ADDRESS", "Собеседник пока недоступен. Он должен открыть приложение и подключиться к сети.")
        ]
        for (code, message) in messages where raw.localizedCaseInsensitiveContains(code) { return message }
        return raw.isEmpty ? "Операция не завершена." : raw
    }
}
