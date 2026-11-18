# Сборка и запуск Fuse для macOS

Клиент находится в `macos/Sources`, ресурсы — в `macos/Resources`, проверки оболочки — в `macos/Tests`. SwiftUI/AppKit управляет общим Go-ядром через `Process`, аргументы и JSONL; сетевой код не дублируется. Базовые требования — macOS 14+, Go 1.27.3 и Command Line Tools со Swift/SDK macOS. Проверяемая архитектура — Apple Silicon arm64.

## Сборка из отдельного клона

```sh
xcode-select --install  # если Command Line Tools ещё не установлены
scripts/setup-go-macos.sh
scripts/build-macos.sh
open "dist/macos-0.4.1-arm64/Fuse.app"
```

`setup-go-macos.sh` загружает официальный Go 1.27.3 для Apple Silicon, проверяет закреплённый SHA-256 и распаковывает его в `.tools/go`, без системной установки. Уже установленный Go можно указать через `GO=/absolute/path/to/go scripts/build-macos.sh`. Зависимости закреплены в `go.mod`/`go.sum`, сборка выполняется с `-mod=readonly`. При первом запуске сборки нужен доступ к Go module proxy.

`SDK` выбирает установленный macOS SDK. `ARCH=x86_64` выбирает Intel-цель; её запуск не подтверждён. `GOPATH` и `GOCACHE` можно задать снаружи, иначе используются локальные `.cache/gopath` и `.cache/go-build`. Swift module cache и временные инструменты находятся в `.build`. Эти каталоги, `.app` и `dist` исключены из Git.

Результат — `dist/macos-0.4.1-arm64/Fuse.app`. Внутри `Contents/Helpers/messenger` находится общее ядро. Техническое имя GUI executable остаётся `P2PMessenger`, bundle ID — `app.p2pmessenger.macos`: видимое имя Fuse не меняет идентичность установленного клиента.

## Сеть и профиль

В публичном `internal/config/default-network.json` списки пусты. Для нового профиля нажмите «Создать ID» в GUI либо выполните:

```sh
"dist/macos-0.4.1-arm64/Fuse.app/Contents/Helpers/messenger" init
```

Затем заполните `bootstrap_nodes` и `relay_nodes` в `~/Library/Application Support/P2PMessenger/config.json` по [инструкции по relay](relay-servers.md) и перезапустите подключение. Пустые списки дают `NODE_NOT_READY`; это не повреждение профиля. При настроенном прежнем профиле повторный init не нужен. Настройки инфраструктуры не коммитятся. `--gui run` включает smart routing, сохраняя конфигурацию на диске.

Основной профиль: `~/Library/Application Support/P2PMessenger`. Папка автоматического приёма: `~/Downloads/P2PMessenger`. Для отдельного тестового профиля:

```sh
open -n "dist/macos-0.4.1-arm64/Fuse.app" --args --profile /tmp/fuse-test
```

Не используйте тестовый профиль как основной. При обновлении завершите прежний клиент через ⌘Q; два ядра не должны управлять одним профилем. Закрытие окна оставляет очередь в фоне. Ключ, контакты, история, сохранённые паузы и настройки сети остаются прежними. Перед первым `--gui run` новой версии ядро под lock создаёт независимую копию в соседнем каталоге `P2PMessenger Backups`; APFS clone либо потоковая копия. Повреждение ключа не заменяется новым ID.

`scripts/migrate-macos-profile.py SOURCE DESTINATION` предназначен для явно выбранного переноса остановленного профиля: блокирует исходник, сохраняет резервную копию и ключ, корректирует внутренние пути, отказывается заменять существующее назначение. Для переименования P2P Messenger в Fuse перенос не требуется.

## Проверки

```sh
go test ./... -count=1 -timeout=5m
go vet ./...
scripts/test-macos-ui.sh
P2PM_BATCH_TEST=1 go test ./internal/node -run TestThousandFileQueue -v -timeout=10m
```

Используйте Go 1.27.3 из PATH либо `.tools/go/bin/go`. Swift-проверки выполняются без GUI и пользовательского профиля: конкурентные pipes, буквальные аргументы, JSONL и неопределённый исход доставки, Int64, потоковое сохранение, перечисление 1010 путей и сохранение UUID/паузы. Обычные Go-тесты используют отдельные локальные узлы и включают 2.1/2.0, файловые границы, подтверждения, восстановление и безопасность Unix IPC.

Пробный запуск настоящей `.app` с временным профилем и локальным bootstrap (пользовательские серверы и профиль не используются):

```sh
mkdir -p .build
go build -o .build/smoke-bootstrap ./cmd/bootstrap
python3 scripts/smoke-macos.py --bootstrap-core .build/smoke-bootstrap
```

Сценарий дважды запускает GUI, проверяет его собственный helper, подключение к loopback-узлу, сохранение ключа и резервную копию, затем завершает только созданные тестовые процессы. Это не визуальная проверка всех элементов интерфейса.

Необязательная сетевая приёмка между двумя временными процессами Mac требует своей доступной инфраструктуры:

```sh
python3 scripts/acceptance-macos.py --mode lan --network-config .local/network.json --report macos-lan
python3 scripts/acceptance-macos.py --mode relay --network-config .local/network.json --report macos-relay
```

Локальный JSON содержит непустые `bootstrap_nodes` и `relay_nodes`. `--large` включает 10 ГиБ в обе стороны; `--peer-core PATH` выбирает альтернативный helper. Временные профили удаляются, подробные отчёты остаются в игнорируемом `docs/reports/macos`. Скрипт не называет второй Mac-процесс Windows или Android. Для реальной межплатформенной приёмки нужны соответствующие отдельные устройства.

## Подпись и состояние функций

По умолчанию используется ad-hoc подпись; `codesign --verify --deep --strict` проверяется сборочным скриптом. `SIGN_IDENTITY` позволяет выбрать имеющуюся identity, но Developer ID/notarization и распространение требуют отдельного процесса. Не отключайте Gatekeeper или firewall. Сборка несандбоксированная; Mac App Store и security-scoped bookmarks не реализованы.

Проверки текущего переноса и ограничения — в [RUNLOG](../RUNLOG.md#run-macos-import-20261118), изменения общего ядра — в [macOS core changes](macos-core-changes.md). Новые требования кликабельных ссылок, копирования, прокрутки, сортировки и relay остаются в [общем бэклоге](backlog.md). Перенос существующей оболочки сам по себе их не закрывает. Готовые сборки и GitHub Releases не публикуются.
