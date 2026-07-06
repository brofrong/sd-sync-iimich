# sd-sync-immich

macOS-приложение в systray: ждёт подключения выбранной SD-карты и загружает новые фото/видео в [Immich](https://immich.app).

## Возможности

- Иконка в menu bar с текущим статусом
- Настройки в подменю: Immich URL, API key, выбор SD-карты
- По умолчанию ничего не синхронизирует — только после выбора карты
- Автозапуск через LaunchAgent
- Сканирует `DCIM/` и `PRIVATE/M4ROOT/CLIP/` (JPG, ARW, MP4)
- Совместимость с **Immich v3** ([migration guide](https://immich.app/blog/v3-migration))
- Дедупликация: локальная БД + заголовок `x-immich-checksum` (как в [immich-go](https://github.com/simulot/immich-go))
- Параллельная синхронизация (настраивается в config)

## Сборка

```bash
go build -o sd-sync-immich ./cmd/sd-sync-immich
```

## Установка

```bash
./sd-sync-immich install
```

Бинарник копируется в `~/Applications/sd-sync-immich`, создаётся LaunchAgent.

## Первый запуск

1. Запустите `./sd-sync-immich run` или дождитесь автозапуска после `install`
2. В systray → **Настройки**:
   - Укажите **Immich URL** (например `https://immich.example.com`)
   - Укажите **API Key** (Immich → Settings → API Keys)
3. Вставьте SD-карту → **Настройки → SD-карта** → выберите её
4. При подключении карты синхронизация запустится автоматически

Конфиг: `~/.config/sd-sync-immich/config.yaml`  
Логи: `~/Library/Logs/sd-sync-immich/app.log` (открыть из меню **Открыть логи**)

## Команды

| Команда | Описание |
|---------|----------|
| `run` | Запуск с systray |
| `install` | Установить автозапуск |
| `uninstall` | Удалить автозапуск |
| `scan-test [mount]` | Проверить сканирование (по умолчанию `/Volumes/Brofrong`) |

## API Key

Создайте в Immich: **Settings → API Keys → Create** с правом upload.
