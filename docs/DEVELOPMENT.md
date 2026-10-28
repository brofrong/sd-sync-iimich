# Для разработчиков и продвинутых пользователей

Подробности, которые не нужны для обычной установки: сборка, команды,
релизы, сайт. Как пользоваться приложением — в [README](../README.ru.md).

## Бинарник для командной строки (macOS)

Ассеты релиза:
- **`sd-sync-immich`** — universal binary (Apple Silicon + Intel)
- **`sd-sync-immich-darwin-arm64`** — только Apple Silicon
- **`sd-sync-immich-darwin-amd64`** — только Intel

```bash
chmod +x sd-sync-immich
./sd-sync-immich install
```

Команда `install`:
- собирает из бинарника `~/Applications/SD Sync Immich.app` и подписывает его ad hoc
- создаёт LaunchAgent `com.sd-sync-immich` (автозапуск при входе в систему) и сразу запускает приложение
- удаляет бинарник `~/Applications/sd-sync-immich`, если он остался от v0.3.0

Проверить checksum (опционально, в папке со скачанными файлами):

```bash
shasum -a 256 -c checksums.txt --ignore-missing
```

## Конфиг и папки камер

Настройки лежат в `config.yaml` (macOS и Linux — `~/.config/sd-sync-immich`,
Windows — `%APPDATA%\sd-sync-immich`; пример — [`config.example.yaml`](../config.example.yaml)).

Опция **Настройки → Искать папки камер автоматически** включена по умолчанию.
Она добавляет к пользовательским путям известные структуры носителей:

- `DCIM/` — Canon, Nikon, Fujifilm, Olympus, GoPro, DJI и другие DCF-камеры
- `PRIVATE/M4ROOT/CLIP/` — Sony XAVC S
- `PRIVATE/AVCHD/BDMV/STREAM/` — Sony, Panasonic и Canon AVCHD
- `MP_ROOT/` — старые режимы Sony MP4
- `PRIVATE/XDROOT/Clip/` и `XDROOT/Clip/` — профессиональные камеры Sony

Автопоиск включает форматы, поддерживаемые Immich: распространённые
изображения, RAW (`ARW`, `CR2/CR3`, `NEF`, `RAF`, `RW2`, `ORF`, `DNG` и
другие) и видео (`MP4`, `MOV`, `MTS/M2TS`, `MXF` и другие). При отключении
используются только `scan.paths` и `scan.extensions` из конфигурации.

## Команды

Бинарник — `~/Applications/SD Sync Immich.app/Contents/MacOS/sd-sync-immich`
(или скачанный `sd-sync-immich`). На Windows и Linux есть только `run` (он же
запуск без аргументов) и `scan-test`. На Windows программа собрана без окна
консоли, поэтому вывод `scan-test` виден только через конвейер, например
`& "$env:LOCALAPPDATA\Programs\SD Sync Immich\sd-sync-immich.exe" scan-test E:\ | more`
в PowerShell.

| Команда | Описание |
|---------|----------|
| `run` | Запуск с иконкой в menu bar |
| `install` | Установить `SD Sync Immich.app` в `~/Applications` с автозапуском |
| `uninstall` | Удалить приложение и автозапуск |
| `scan-test [mount]` | Проверить сканирование (по умолчанию `/Volumes/Brofrong`) |

## Удаление на macOS

Удаляет приложение, LaunchAgent и бинарник v0.3.0 (настройки и история
остаются в `~/.config/sd-sync-immich`):

```bash
~/Applications/"SD Sync Immich.app"/Contents/MacOS/sd-sync-immich uninstall
```

Если приложение ставилось из DMG в `/Applications`, выполните команду из
него, а затем перетащите приложение в Корзину:

```bash
/Applications/"SD Sync Immich.app"/Contents/MacOS/sd-sync-immich uninstall
```

Установленное через Homebrew удаляется `brew uninstall --cask sd-sync-immich`
(вместе с LaunchAgent), с `--zap` — ещё и настройки, история и журнал.

## Homebrew

Cask лежит в отдельном репозитории
[brofrong/homebrew-tap](https://github.com/brofrong/homebrew-tap). В нём
записаны версия и SHA-256 DMG из `checksums.txt` последнего релиза; их
обновляет workflow `bump.yml` того же репозитория — каждые 6 часов и по
кнопке **Run workflow**. После релиза ничего делать не нужно, а чтобы cask
обновился сразу: `gh workflow run bump.yml -R brofrong/homebrew-tap`.

## Flatpak

Всё для Flatpak лежит в `packaging/flatpak/`: манифест, `.desktop`,
metainfo и `node-sources.json`. Сборка идёт без сети, как на Flathub:

- Go-модули берутся из `vendor/`. В CI перед сборкой выполняется
  `go mod vendor`, а для Flathub каждый релиз прикладывает архив
  `sd-sync-immich-vendor.tar.xz`.
- npm-пакеты берутся из `node-sources.json`. После каждого изменения
  `frontend/package-lock.json` его нужно перегенерировать:

  ```bash
  pip install "git+https://github.com/flatpak/flatpak-builder-tools.git#subdirectory=node"
  flatpak-node-generator npm frontend/package-lock.json -o packaging/flatpak/node-sources.json
  ```

- TypeScript-привязки (`frontend/bindings`) лежат в git: Wails CLI в такой
  сборке не запускается. `scripts/build-frontend.sh` их обновляет, а CI
  проверяет, что они закоммичены свежими.

Workflow `flatpak.yml` собирает бандл и прогоняет `flatpak-builder-lint`,
когда меняется упаковка или зависимости; его можно запустить и вручную.
Бандл остаётся артефактом запуска, а релиз прикладывает его как
`SD-Sync-Immich-x86_64.flatpak`.

Во Flatpak приложение не проверяет обновления: его обновляет Flatpak.
Автозапуск включается через портал Background, а UUID и метки карт
берутся у UDisks2 (в песочнице нет `/dev/disk`).

Перед каждым релизом в metainfo добавляется строка `<release>`.

## Прежнее имя репозитория

До v0.6.0 репозиторий назывался `sd-sync-iimich` (опечатка). Его
переименовали в `sd-sync-immich`, потому что Flathub требует, чтобы по id
`io.github.brofrong.sd-sync-immich` открывался репозиторий.

- Ссылки на код, релизы и API GitHub перенаправляет сам. Новый репозиторий
  со старым именем создавать нельзя: перенаправления пропадут.
- Версии до v0.5.5 включительно не принимают перенаправление API на
  `api.github.com/repositories/…` и не видят обновлений: их нужно обновить
  вручную, скачав новую версию. Начиная с v0.6.0 апдейтер спрашивает новое
  имя, а при 404 — старое.
- GitHub Pages старый адрес не перенаправляет. Страницы
  `brofrong.github.io/sd-sync-iimich/` и `…/downloads/` переадресуют на новый
  сайт из репозитория `brofrong/brofrong.github.io`.

## Обновление с v0.3.0

Ничего делать не нужно: v0.3.0 скачает обновление как обычно, а новая версия
при первом запуске сама соберёт `~/Applications/SD Sync Immich.app`, перенастроит
LaunchAgent на него, удалит старый бинарник `~/Applications/sd-sync-immich` и
перезапустится уже из приложения. Настройки, выбранная карта и история
синхронизаций сохраняются.

## Как устроены обновления

Релизная версия проверяет обновления при запуске и затем каждые 12 часов.
Открытие **Настроек** тоже проверяет (не чаще раза в 10 минут), там же —
ручная проверка в разделе **Обновления**. При обнаружении новой версии
приложение показывает release notes и запрашивает подтверждение.

- **macOS:** перед атомарной заменой исполняемого файла в приложении
  проверяется SHA-256 из `checksums.txt`, затем приложение заново
  подписывается и перезапускается.
- **Windows:** приложение скачивает новый установщик, проверяет SHA-256 из
  `checksums.txt`, запускает его в тихом режиме и закрывается; установщик
  заменяет программу и снова запускает её.
- **Linux:** приложение скачивает новый AppImage, проверяет SHA-256 из
  `checksums.txt`, заменяет им свой файл и перезапускается (папка с AppImage
  должна быть доступна на запись). Запущенное не из AppImage приложение
  только сообщает о новой версии.

**Выход** из меню завершает приложение до следующего входа в систему (или
до ручного запуска).

Локальная сборка с версией `dev` не обновляется автоматически.

## Сборка из исходников

Нужны [Go](https://go.dev/dl/) 1.26+, [Node.js](https://nodejs.org) 22+ и
Xcode Command Line Tools (`xcode-select --install`). Wails CLI запускается
через `go run`, отдельно ставить его не нужно.

```bash
git clone https://github.com/brofrong/sd-sync-immich.git
cd sd-sync-immich
scripts/build-app.sh dev dist
open dist   # SD Sync Immich.app, SD-Sync-Immich.zip и (с dmgbuild) SD-Sync-Immich.dmg
# или сразу установить с автозапуском:
dist/sd-sync-immich install
```

Только фронтенд и бинарник для разработки:

```bash
scripts/build-frontend.sh
CGO_ENABLED=1 go build -o sd-sync-immich ./cmd/sd-sync-immich
```

## Релизы

Project skill `.cursor/skills/release` анализирует изменения после последнего
тега, предлагает `major`, `minor` или `patch` и показывает release notes.
После подтверждения он запускает:

```bash
scripts/release.sh <major|minor|patch>
```

Скрипт требует чистую ветку `main`, совпадающую с `origin/main`, вычисляет
следующую SemVer-версию и после отдельного подтверждения отправляет новый тег.
При push тега `v*` GitHub Actions собирает macOS, Windows и Linux и публикует
один релиз со всеми файлами и общим `checksums.txt`. Ручной запуск release
workflow — пробная сборка: те же файлы появляются как артефакты запуска,
релиз не публикуется (только на теге `v*` с отмеченным `publish`):

```bash
gh workflow run release.yml --ref <ветка>
```

Релиз публикуется, только когда собрались все три системы. Если сборка одной
из них упала (например, сбой сети), откройте запуск тега в Actions и нажмите
**Re-run failed jobs**: пересоберутся упавшие задания, и релиз опубликуется.

Сборка локально: macOS — `scripts/build-app.sh <версия> dist`; Linux —
`scripts/build-linux.sh <версия> dist` (на Mac — в Docker, AppImage под
архитектуру машины, `BUILD_ARCH=amd64` — x86_64; папка для результата — внутри
репозитория, контейнер получает только его); Windows — на Windows
`scripts/build-windows.ps1 -Version <версия> -OutDir dist` (нужен NSIS).

## Сайт

Страница [brofrong.github.io/sd-sync-immich](https://brofrong.github.io/sd-sync-immich/)
лежит в `site/` (обычный HTML, без сборки) и публикуется на GitHub Pages
при пуше в `main` (`.github/workflows/pages.yml`). Версию, размеры файлов и
число звёзд она берёт из GitHub при открытии, ссылки ведут на последний
релиз — после релиза её трогать не нужно. Скриншоты в `site/assets` сняты с
браузерной версии окна: `VITE_MOCK=1 npx vite` в `frontend/`, адрес вида
`/?lang=ru&section=gallery&state=done&update=none&numbers=off`, окно 960×620.
