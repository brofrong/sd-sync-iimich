# sd-sync-immich

macOS-приложение в menu bar: ждёт подключения выбранной SD-карты и загружает новые фото/видео в [Immich](https://immich.app).

**Требования:** macOS (Apple Silicon или Intel), Immich v3.

## Возможности

- Иконка в menu bar с текущим статусом и спиннером при синхронизации
- Настройки в подменю: Immich URL, API key, выбор SD-карты
- По умолчанию ничего не синхронизирует — только после выбора карты
- Автозапуск через LaunchAgent
- Сканирует `DCIM/` и `PRIVATE/M4ROOT/CLIP/` (JPG, ARW, MP4)
- Совместимость с **Immich v3** ([migration guide](https://immich.app/blog/v3-migration))
- Дедупликация: watermark по дате файла + заголовок `x-immich-checksum` (как в [immich-go](https://github.com/simulot/immich-go))
- Параллельная синхронизация (настраивается в config)
- Уведомление macOS после успешной синхронизации

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

Конфиг: `~/.config/sd-sync-immich/config.yaml` (пример: [`config.example.yaml`](config.example.yaml))  
Логи: `~/Library/Logs/sd-sync-immich/app.log` (открыть из меню **Открыть логи**)

## Команды

| Команда | Описание |
|---------|----------|
| `run` | Запуск с иконкой в menu bar |
| `install` | Установить автозапуск |
| `uninstall` | Удалить автозапуск |
| `scan-test [mount]` | Проверить сканирование (по умолчанию `/Volumes/Brofrong`) |

## API Key

Создайте в Immich: **Settings → API Keys → Create** с правом `upload`.

## Релизы

Новый релиз публикуется автоматически при push тега `v*` (например `v0.1.0`):

```bash
git tag v0.1.0
git push origin v0.1.0
```

Сборка также доступна вручную через **Actions → Release → Run workflow**.

## Лицензия

[MIT](LICENSE)
