# sd-sync-immich

macOS-приложение в menu bar: ждёт подключения выбранной SD-карты и загружает новые фото/видео в [Immich](https://immich.app).

**Требования:** macOS (Apple Silicon или Intel), Immich v3.

## Возможности

- Иконка в menu bar с текущим статусом и спиннером при синхронизации
- Настройки в подменю: Immich URL, API key, выбор SD-карты
- По умолчанию ничего не синхронизирует — только после выбора карты
- Автозапуск через LaunchAgent
- Автоматически находит фото и видео в стандартных каталогах камер
- Поддерживает распространённые фото, RAW и видеоформаты Immich
- Совместимость с **Immich v3** ([migration guide](https://immich.app/blog/v3-migration))
- Дедупликация: watermark по дате файла + заголовок `x-immich-checksum` (как в [immich-go](https://github.com/simulot/immich-go))
- Параллельная синхронизация (настраивается в config)
- Уведомление macOS после успешной синхронизации
- Автоматическая и ручная проверка обновлений через GitHub Releases

## Установка

### Из GitHub Releases (рекомендуется)

1. Откройте [Releases](https://github.com/brofrong/sd-sync-iimich/releases/latest) и скачайте архив/бинарник:
   - **`sd-sync-immich`** — universal binary (Apple Silicon + Intel)
   - **`sd-sync-immich-darwin-arm64`** — только Apple Silicon
   - **`sd-sync-immich-darwin-amd64`** — только Intel
2. Сделайте файл исполняемым и установите автозапуск:

```bash
chmod +x sd-sync-immich
./sd-sync-immich install
```

Команда `install`:
- копирует бинарник в `~/Applications/sd-sync-immich`
- создаёт LaunchAgent `com.sd-sync-immich` (автозапуск при входе в систему)

Проверить checksum (опционально):

```bash
shasum -a 256 -c checksums.txt
```

### Сборка из исходников

Нужны [Go](https://go.dev/dl/) 1.26+ и Xcode Command Line Tools (`xcode-select --install`).

```bash
git clone https://github.com/brofrong/sd-sync-iimich.git
cd sd-sync-iimich
CGO_ENABLED=1 go build -o sd-sync-immich ./cmd/sd-sync-immich
./sd-sync-immich install
```

### Удаление

```bash
~/Applications/sd-sync-immich uninstall
# или, если бинарник в текущей папке:
./sd-sync-immich uninstall
```

## Первый запуск

1. Запустите `./sd-sync-immich run` или дождитесь автозапуска после `install`
2. В menu bar → **Настройки**:
   - Укажите **Immich URL** (например `https://immich.example.com`)
   - Укажите **API Key** (Immich → Settings → API Keys)
3. Вставьте SD-карту → **Настройки → SD-карта** → выберите её
4. При подключении карты синхронизация запустится автоматически

Опция **Настройки → Автопоиск медиа с камер** включена по умолчанию. Она
добавляет к пользовательским путям известные структуры носителей:

- `DCIM/` — Canon, Nikon, Fujifilm, Olympus, GoPro, DJI и другие DCF-камеры
- `PRIVATE/M4ROOT/CLIP/` — Sony XAVC S
- `PRIVATE/AVCHD/BDMV/STREAM/` — Sony, Panasonic и Canon AVCHD
- `MP_ROOT/` — старые режимы Sony MP4
- `PRIVATE/XDROOT/Clip/` и `XDROOT/Clip/` — профессиональные камеры Sony

Автопоиск включает форматы, поддерживаемые Immich: распространённые изображения,
RAW (`ARW`, `CR2/CR3`, `NEF`, `RAF`, `RW2`, `ORF`, `DNG` и другие) и видео
(`MP4`, `MOV`, `MTS/M2TS`, `MXF` и другие). При отключении используются только
`scan.paths` и `scan.extensions` из конфигурации.

Конфиг: `~/.config/sd-sync-immich/config.yaml` (пример: [`config.example.yaml`](config.example.yaml))  
Логи: `~/Library/Logs/sd-sync-immich/app.log` (открыть из меню **Открыть логи**)

## Команды

| Команда | Описание |
|---------|----------|
| `run` | Запуск с иконкой в menu bar |
| `install` | Установить автозапуск |
| `uninstall` | Удалить автозапуск |
| `scan-test [mount]` | Проверить сканирование (по умолчанию `/Volumes/Brofrong`) |

## Обновления

Релизная версия проверяет обновления при запуске и затем каждые 12 часов.
Ручная проверка доступна через **Проверить обновления…** в menu bar. При
обнаружении новой версии приложение показывает release notes и запрашивает
подтверждение. Перед атомарной заменой бинарника проверяется SHA-256 из
`checksums.txt`, после чего LaunchAgent перезапускает приложение.

Локальная сборка с версией `dev` не обновляется автоматически.

## API Key

Создайте в Immich: **Settings → API Keys → Create** с правом `upload`.

## Релизы

Project skill `.cursor/skills/release` анализирует изменения после последнего
тега, предлагает `major`, `minor` или `patch` и показывает release notes.
После подтверждения он запускает:

```bash
scripts/release.sh <major|minor|patch>
```

Скрипт требует чистую ветку `main`, совпадающую с `origin/main`, вычисляет
следующую SemVer-версию и после отдельного подтверждения отправляет новый тег.
GitHub Actions собирает релиз только при push тега `v*`; ручного запуска
release workflow нет.

## Лицензия

[MIT](LICENSE)
