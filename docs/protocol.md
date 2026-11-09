# Wire, discovery и локальное хранение

## Сетевой стек

Go 1.27.3, libp2p 0.41.1, kad-dht 0.30.2. Единственные транспорты/защита/mux: TCP / Noise / Yamux. Relay — Circuit v2. Клиент `ModeClient`, `ForceReachabilityPrivate`, без прямого TCP-listener; `NoListenAddrs` применяется **перед** `EnableRelay`. Сервер `ModeServer`, `ForceReachabilityPublic`.

AutoRelay использует статических кандидатов: min candidates 1, reservations `min(2,N)`, boot delay 0, backoff 15s, min interval 5s. Штатный AutoRelay продлевает ограниченные reservations, публикует изменения адресов и Protect/Unprotect соединения relay. Приложение фильтрует его действующие адреса, объединяет события до 750ms и перепубликует контакт каждые 5 минут ±20%. `submitted` означает успешный возврат `PutValue`, а не подтверждение всех удалённых реплик.

## Discovery

Протокол DHT `/p2pm/kad/1.0.0`. Ключ `/p2pm/<canonical PeerID>`. Значение — штатный `record.Envelope` с `peer.PeerRecord`. Проверяются домен `PeerRecordEnvelopeDomain`, тип payload, подпись, совпадение ID ключа/записи/подписанта, `Seq>0`, до 8 адресов, до 512 байт/адрес, до 8192 байт/envelope.

Адрес — `/ip4|ip6|dns4|dns6/HOST/tcp/PORT` или тот же транспорт плюс `/p2p/RELAY/p2p-circuit`. Завершающий ID получателя не входит в запись; он задаётся `peer.AddrInfo.ID`. Произвольные цепочки relay и другие транспорты отклоняются. Обычный режим запрещает непубличные IP; connection gater дополнительно проверяет фактически разрешённый IP непосредственно перед dial. DNS не используется как способ обойти фильтрацию локальных адресов.

При выборе записи побеждает наибольший Seq. При равном Seq побеждает лексикографически больший SHA-256 **RawPayload**; конфликт можно диагностировать callback валидатора. Подпись не доказывает владение адресом; окончательная аутентификация — при Noise-подключении к ожидаемому Peer ID.

`state.json`: `{"version":1,"seq":UINT64}`. Перед созданием публикации сохраняется `max(last+1, positive UnixNano)`. При переполнении/повреждении возникает ошибка. Запись: временный защищённый файл → Sync → atomic replace, на Windows `MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH)`, на Unix rename + fsync каталога.

`peer-cache.json`: schema version 1, map canonical ID → `{envelope: base64, seen: RFC3339Nano}`, список ранее аутентифицированной инфраструктуры. Записи проверяются при загрузке. Меньший Seq не вытесняет больший. Обычный send может использовать кеш до 5 минут; `find` выполняет GetValue, при его отказе источник явно `cache_fallback`. Более новая сохранённая версия, чем пришедшая из DHT, возвращается с `cache_newer`.

## Chat

Протокол `/p2p-messenger/chat/1.0.0`. Одна попытка: новый stream, один Message, один Reply, закрытие. Limited circuit разрешён через `network.WithAllowLimitedConn(ctx,"p2pm-chat")`.

Фрейм — unsigned big-endian uint32 длина, затем UTF-8 JSON. Максимум 524288 байт, проверка до выделения памяти. JSON допускает только поля схемы; невалидный UTF-8 и лишний JSON после объекта отклоняются. Максимум декодированного text — 65536 байт; даже 65536 нулевых символов с JSON-экранированием укладываются в лимит фрейма.

```json
{"version":1,"type":"message","message_id":"550e8400-e29b-41d4-a716-446655440000","timestamp":"2026-11-03T16:00:00Z","text":"Привет"}
```

UUID строго v4/RFC4122, канонический нижний регистр, криптографический источник. Timestamp RFC3339Nano проверяется синтаксически, не является доверенным временем. Отправитель определяется по `Stream.Conn().RemotePeer()`, а не из payload.

```json
{"version":1,"type":"ack","message_id":"550e8400-e29b-41d4-a716-446655440000","status":"stored"}
{"version":1,"type":"error","message_id":"550e8400-e29b-41d4-a716-446655440000","code":"MESSAGE_ID_CONFLICT"}
```

ACK: только `stored` / `duplicate`, ожидаемые version/ID/аутентифицированный пир. Ошибки: UNSUPPORTED_VERSION, INVALID_MESSAGE, MESSAGE_ID_CONFLICT, STORAGE_ERROR, RATE_LIMITED. Неразбираемый/слишком большой фрейм может быть reset без ответа. После первого Write любой неопределённый исход консервативно `delivery_unknown`.

В libp2p 0.41.1 circuit `ConnState()` не заполняет поле Security. Проверки используют единственный настроенный secure transport Noise, аутентифицированный RemotePublicKey и соответствующий ему Peer ID; тест подмены назначения отклоняется. Проверка ограниченного circuit не полагается на пустое поле Security или отсутствие текста в дампе.

## JSONL и дедупликация

Каждая строка — Event с `schema_version:1`, `event:message|status`, `direction:in|out`, `peer_id`, `message_id`, `local_time`, `status`, необязательными `code`/`route`. `message` присутствует лишь в событии нового сообщения и содержит неизменяемый wire Message. Максимум строки — 1 MiB. Статусные события не дублируют текст.

Индекс `(direction,peer,id)` содержит offset/length, SHA-256 сериализованного Message, текущие status/code/route. Получатель сериализует проверку и append+Sync. Точный повтор — duplicate; изменённый timestamp/текст — конфликт. Индекс обновляется только после успешной синхронизации. При запуске восстанавливается потоковым чтением. Тексты не удерживаются в индексе. Исходящие pending после сбоя получают дополнительное событие delivery_unknown.

## IPC

Windows: `\\.\pipe\p2pm-<SHA256(SID + NUL + normalized absolute directory)[:16]>`, owner-only DACL, remote clients rejected в go-winio. Unix socket используется лишь для разработки/тестов. Фрейминг тот же, 512 KiB. Одно соединение — один запрос.

Request: `version:1`, `request_id`, `method`, `params`, `deadline` (RFC3339Nano, сервер ограничивает максимумом 30s). Params: peer_id/message/message_id/limit по методу. Response: `version:1`, тот же request_id, `result` либо `error`; отдельный финальный фрейм `done:true`. History/peers идут отдельными результатами. Максимум 32 одновременных IPC-соединения. Клиент не повторяет send при разрыве IPC; идентификатор сообщения создаётся до запроса и сохраняется сервером перед сетью.
