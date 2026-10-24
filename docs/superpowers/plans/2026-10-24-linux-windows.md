# Linux и Windows (бета) — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** то же приложение на Windows 10/11 (x64, установщик `.exe`) и настольном Linux (x64, AppImage), помеченное «бета»; плюс значки GitHub/issue, кнопка «Выход» и 10-секундный предохранитель автоформатирования на всех системах.

**Architecture:** системный код живёт в файлах `_darwin.go` / `_windows.go` / `_linux.go` за одинаковыми экспортируемыми API в пакетах `volume`, `power`, `notify`, `install`, `i18n`, `gui/icon`, новом `desktop`; общие типы — в файлах без суффикса. Обновление и упаковка разделены по системам. CI собирает и тестирует все три системы; релиз собирает файлы со всех трёх.

**Tech Stack:** Go 1.26, Wails `v3.0.0-beta.34` (WebView2 / WebKitGTK 4.1, NSIS, AppImage), `golang.org/x/sys/windows`, `github.com/godbus/dbus/v5` (UDisks2, logind, трей), Svelte 5, GitHub Actions (macos-latest, windows-latest, ubuntu-22.04), Docker для локальных Linux-тестов.

**Spec:** `docs/superpowers/specs/2026-10-24-linux-windows-design.md`

## Global Constraints

- Wails ровно `v3.0.0-beta.34`; Mac-сборка и её поведение не меняются (CGO-флаги `-O2 -g -mmacosx-version-min=13.0`, Mac-файлы релиза с прежними именами).
- Имена файлов релиза: `SD-Sync-Immich-Setup-x64.exe`, `SD-Sync-Immich-x86_64.AppImage`; все файлы всех систем — в одном `checksums.txt`.
- Репозиторий: `https://github.com/brofrong/sd-sync-iimich`; новый issue — `https://github.com/brofrong/sd-sync-iimich/issues/new`.
- Данные: Linux — конфиг `~/.config/sd-sync-immich`, логи `~/.local/state/sd-sync-immich`; Windows — конфиг `%APPDATA%\sd-sync-immich`, логи `%LOCALAPPDATA%\sd-sync-immich\logs`; macOS — как сейчас.
- Windows ставится для текущего пользователя в `%LOCALAPPDATA%\Programs\SD Sync Immich`, ярлык в «Пуск»; автозапуск — `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`; Linux — `~/.config/autostart/sd-sync-immich.desktop`.
- Предохранитель: кнопка подтверждения автоформатирования неактивна **10 секунд** («Включить (10)»…), «Отмена» сразу; текст: «Все файлы с карты будут удалены после проверки, что каждое фото и видео есть в Immich. Это нельзя отменить.» — на всех системах.
- Никаких прав администратора/root для обычной работы; форматирование, требующее их, → `volume.ErrNeedsAdmin` → `format_failed` с понятным текстом, без запроса прав.
- Все строки UI — на 8 языках, паритет ключей проходит. Go не отдаёт тексты в UI.
- **Безопасность разработки:** на машине работает живая Mac-установка и может быть вставлена карта. Не запускать собранное приложение, не вызывать `diskutil`/`launchctl`/`caffeinate`/`osascript`, не трогать `~/.config/sd-sync-immich`, `/Volumes`. Тесты — только через швы. Docker-контейнер получает только каталог репозитория.
- Проверка перед коммитом: Mac — `go vet ./... && go test -race ./...` (с CGO-флагами); Windows — `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...` и компиляция тестов `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /dev/null <pkg>` для изменённых пакетов; Linux — `scripts/test-linux.sh` (Docker). Фронтенд — `scripts/build-frontend.sh && cd frontend && npm run check`.

## Review Focus

1. Linux: кард-ридер USB сообщает `removable=0`, но карта смонтирована в `/media/<user>/…` → карта видна как съёмная. Тест: Task 3 `TestLinuxRemovableByMountPoint`.
2. Windows: между проверкой и форматированием под той же буквой диска оказалась другая карта → форматирования нет (сверка серийного номера). Тест: Task 4 `TestWindowsEraseRefusesOtherSerial`.
3. Linux: приложение запущено не из AppImage (`$APPIMAGE` пуст) → обновление ничего не заменяет и отдаёт ошибку «скачайте с GitHub». Тест: Task 6 `TestLinuxUpdateWithoutAppImage`.
4. Windows: пути с `\` → в базе и истории всегда `/` (иначе история и курсор не совпадут с другими системами). Тест: Task 1 `TestScanRelativePathsUseSlash` (в CI на Windows).
5. Windows без `HOME`: конфиг и `~` в `db_path` разрешаются через профиль пользователя. Тест: Task 1 `TestDirOnWindows` / `TestExpandPathWithoutHome`.

---

### Task 1: Каркас для трёх систем и CI

**Files:**
- Create: `internal/volume/volume.go` (общие `Info`, `Event`, `EventType`, `EventMounted`, `EventUnmounted`, `ErrErasedNotRemounted`, `ErrNeedsAdmin`, `ErrUnsupported`), `internal/volume/stub_other.go` (`//go:build !darwin`: заглушки с `ErrUnsupported` для API ниже, удаляются задачами 2–4), аналогично `internal/power/power_other.go`, `internal/notify/notify.go` (общие `Message`, `Action`, `DefaultAction`) + `notify_other.go` (заглушка `Center`, лог вместо уведомлений), `internal/install/install_other.go` (заглушки), `internal/gui/icon/icon_other.go`, `internal/i18n/system_windows.go`, `internal/i18n/system_linux.go`, `internal/desktop/desktop.go` + `_darwin.go` + `_windows.go` + `_linux.go`, `internal/config/dir_darwin.go` / `dir_windows.go` / `dir_linux.go`, `internal/logx/dir_*.go`, `scripts/test-linux.sh`.
- Modify: `internal/volume/*_darwin.go` (убрать перенесённые типы), `internal/notify/*_darwin.go`, `internal/config/config.go` (`ConfigPath`, `ResolvedDBPath`, `Default().State.DBPath = ""`), `internal/logx/log.go`, `internal/gui/service.go` (`RevealLog`, `OpenImmich` через `desktop`), `.github/workflows/ci.yml`.

**Interfaces:**
- Produces (на всех системах, эти сигнатуры — контракт для задач 2–8):
  - `volume`: `ListRemovable() ([]Info, error)`, `GetInfo(mountPoint string) (*Info, error)`, `FindByUUID(uuid string) (*Info, error)`, `NewWatcher() (*Watcher, error)`, `(*Watcher).Events() <-chan Event`, `(*Watcher).Close()`, `Eject(mountPoint string) error`, `Erase(mountPoint, wantUUID string) (Info, error)`; ошибки `ErrErasedNotRemounted`, `ErrNeedsAdmin`, `ErrUnsupported`.
  - `power.KeepAwake() (release func())`.
  - `notify.NewCenter(svc *notifications.NotificationService, onResponse func(actionID string, data map[string]string)) *Center`, `(*Center).Service() application.Service`, `Send(Message)`, `Remove(id string)`.
  - `install.AutostartAvailable(executable string) bool`, `AutostartEnabled(executable string) bool`, `SetAutostart(enabled bool, executable string) error`, `UnderLaunchd() bool`, `Translocated(path string) bool`, `ErrTranslocated` (на Windows/Linux `Translocated`/`UnderLaunchd` — `false`).
  - `i18n.SystemLanguages() []string`; `icon.IdlePNG()`, `icon.SyncingPNG(frame int)`, `icon.FrameCount`, `icon.Template bool` (`true` только на Mac).
  - `desktop.OpenURL(url string) error`, `desktop.Open(path string) error`, `desktop.Reveal(path string) error`.
  - `config.Dir() (string, error)`; `ResolvedDBPath()` при пустом `db_path` → `Dir()/state.db`; `logx.LogDir()` по Global Constraints.
- [ ] **Step 1: Failing tests.** `internal/config`: `TestDirOnWindows` (`//go:build windows`, `APPDATA` → `…\sd-sync-immich`), `TestResolvedDBPathDefaultsToDir` (пустой `db_path` → `Dir()/state.db`), `TestExpandPathWithoutHome` (на Windows `~` раскрывается через `os.UserHomeDir`, переменная `HOME` не задана); `internal/scanner`: `TestScanRelativePathsUseSlash` (вложенный файл → `RelativePath == "DCIM/100X/A.JPG"`); `internal/i18n`: `TestSystemLanguagesFromLang` (`//go:build linux`, `LANG=de_DE.UTF-8` → `["de-DE"]` или с `de` первым — как требует `Resolve`).
- [ ] **Step 2:** FAIL (на Mac — `go test`; Windows-тесты — компиляция `GOOS=windows … go test -c`).
- [ ] **Step 3: Implement.** Перенести типы, добавить заглушки и системные файлы; `ci.yml`: матрица `macos-latest` (как сейчас), `windows-latest`, `ubuntu-22.04` (`apt-get install libgtk-3-dev libwebkit2gtk-4.1-dev`), `go vet ./...` + `go test ./...` на каждой, фронтенд-сборка перед Go; триггер — push в любую ветку и PR. `scripts/test-linux.sh`: Docker `golang:1.26-bookworm`, ставит GTK/WebKit dev-пакеты, монтирует только репозиторий, гоняет `scripts/build-frontend.sh`, `go vet ./...`, `go test ./...`.
- [ ] **Step 4:** Mac-тесты PASS; `GOOS=windows … go vet ./...` чисто; `scripts/test-linux.sh` PASS.
- [ ] **Step 5: Commit** `build: compile and test on macOS, Windows and Linux`.

### Task 2: Тома и извлечение — Windows

**Files:** Create `internal/volume/volume_windows.go`, `watcher_windows.go`, `eject_windows.go`, `volume_windows_test.go`; delete Windows-часть `stub_other.go`.

**Interfaces:** реализует контракт `volume` из Task 1 (кроме `Erase`).
- Карта: том с буквой и `GetDriveType == DRIVE_REMOVABLE`; `Name` — метка тома; `UUID` — серийный номер тома `XXXX-XXXX` (верхний регистр, hex); `MountPoint` — `E:\`. Наблюдатель — опрос раз в 2 с с событиями по разнице снимков. Извлечение — безопасное извлечение без прав администратора; занятый том → ошибка с причиной.
- Все вызовы Win32 — за швами (`var` функции), тесты на чистой логике.
- [ ] **Step 1: Failing tests** (`//go:build windows`): `TestFormatSerial` (0x1234ABCD → `1234-ABCD`), `TestWatcherDiff` (снимки до/после → события mounted/unmounted, порядок стабилен), `TestListRemovableFiltersFixed` (фейковые диски: A фиксированный, E съёмный → только E).
- [ ] **Step 2:** FAIL (компиляция тестов под Windows).
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `GOOS=windows … go vet` и `go test -c` чисто; Mac/Linux не затронуты.
- [ ] **Step 5: Commit** `feat(volume): removable cards on Windows`.

### Task 3: Тома и извлечение — Linux

**Files:** Create `internal/volume/volume_linux.go`, `watcher_linux.go`, `eject_linux.go`, `udisks_linux.go`, `volume_linux_test.go`; delete Linux-часть `stub_other.go`; `go.mod` (`github.com/godbus/dbus/v5`).

**Interfaces:** реализует контракт `volume` (кроме `Erase`).
- Карта: точка монтирования под `/media/<user>/` или `/run/media/<user>/` (из `/proc/self/mountinfo`) **или** устройство с `/sys/block/<disk>/removable == 1`; `UUID` — UUID файловой системы (из `/dev/disk/by-uuid`), верхний регистр; `Name` — метка или базовое имя точки монтирования. Наблюдатель — опрос `mountinfo` раз в 2 с. Извлечение — UDisks2: `Filesystem.Unmount`, затем `Drive.PowerOff` (если не поддерживается — `Drive.Eject`), с `auth.no_user_interaction=true`.
- Чтение `mountinfo`, `/sys`, `/dev/disk/by-uuid` и D-Bus — за швами; тесты на фикстурах.
- [ ] **Step 1: Failing tests** (`//go:build linux`): `TestParseMountinfo` (строки с пробелами `\040` в пути), `TestLinuxRemovableByMountPoint` (Review Focus 1: `removable=0`, монтирование в `/media/u/CARD` → съёмная), `TestLinuxIgnoresSystemMounts` (`/`, `/boot`, `/snap/...` → нет), `TestUUIDFromByUUID`.
- [ ] **Step 2:** FAIL (`scripts/test-linux.sh`).
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** `scripts/test-linux.sh` PASS.
- [ ] **Step 5: Commit** `feat(volume): removable cards on Linux`.

### Task 4: Форматирование и запрет сна — Windows и Linux

**Files:** Create `internal/volume/erase_windows.go`, `erase_linux.go` (+ тесты), `internal/power/power_windows.go`, `power_linux.go` (+ тесты); remove remaining stubs.

**Interfaces:** `volume.Erase(mountPoint, wantUUID string) (Info, error)` с теми же правилами, что `erase_darwin.go` (прочитай его): отказ без совпадения ID, не съёмного устройства, не раздела, формата кроме exFAT/FAT32; тот же формат и имя; новый том ищется по устройству; после стирания без найденного тома — `ErrErasedNotRemounted`; нужна авторизация/права — `ErrNeedsAdmin` (без запроса). `power.KeepAwake()` — Windows `SetThreadExecutionState(ES_CONTINUOUS|ES_SYSTEM_REQUIRED)` на отдельном закреплённом потоке до `release`; Linux — `org.freedesktop.login1.Manager.Inhibit("sleep:idle", …)`, `release` закрывает дескриптор; ошибки — только лог.
- Windows: форматирование системным средством форматирования тома по букве диска; Linux: UDisks2 `Block.Format` (`exfat`/`vfat`, `label`), затем `Filesystem.Mount`, с `auth.no_user_interaction=true`.
- [ ] **Step 1: Failing tests:** Windows — `TestWindowsEraseRefusesOtherSerial` (Review Focus 2), `TestWindowsEraseAccessDeniedIsNeedsAdmin`, `TestWindowsEraseKeepsLabelAndFormat`; Linux — `TestLinuxEraseRefusesOtherUUID`, `TestLinuxEraseNotAuthorizedIsNeedsAdmin`, `TestLinuxEraseFindsNewVolumeByDevice`; power — `TestKeepAwakeReleaseIdempotent` на обеих (через швы).
- `internal/app`: `errors.Is(err, volume.ErrNeedsAdmin)` → новый код `format_needs_admin{name}` (действие `open_logs`, тон ошибки, без извлечения, подавление повторов как у `format_failed`); ключи `status.format_needs_admin.title/detail` и `notify.format_needs_admin.title/body` на 8 языках — ru: «Не удалось отформатировать» / «Для форматирования карты «{name}» нужны права администратора. Отформатируйте её в камере.»; уведомление — вид «Карта»; тест `TestFormatNeedsAdmin` в `internal/app`.
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** Windows-компиляция тестов, `scripts/test-linux.sh` и Mac-тесты PASS.
- [ ] **Step 5: Commit** `feat(volume,power): format and keep awake on Windows and Linux`.

### Task 5: Окно, трей, уведомления и язык — Windows и Linux

**Files:** Create `internal/notify/native_other.go` (вместо заглушки), `internal/gui/shell_darwin.go` / `shell_other.go`, `internal/gui/traycheck_linux.go`; Modify `internal/gui/app.go`, `internal/gui/tray.go`, `internal/gui/icon/icon_other.go`.

**Interfaces:**
- `notify.Center` на Windows/Linux — та же очередь и тот же `centerService`, что на Mac (вынести общую часть в файл без суффикса, не дублировать); если сервис Wails не стартовал или отказ в разрешении — уведомления только в лог.
- `shell`: `showWindow()`/`hideWindow()` трогают Dock только на Mac; `trayAvailable() bool` — Mac/Windows `true`, Linux — есть владелец имени `org.kde.StatusNotifierWatcher` на шине сессии. Закрытие окна: трей есть → спрятать; нет → свернуть.
- Иконка трея: Mac — шаблонная (`SetTemplateIcon`), иначе — цветная (`SetIcon`), видна на светлой и тёмной панели; клик по значку в трее открывает окно.
- [ ] **Step 1: Failing tests:** `TestCloseActionWithoutTray` (чистая функция: `trayAvailable=false` → «свернуть», `true` → «спрятать»); `TestTrayIconSetter` (на Mac — шаблон, иначе цветной); Linux `TestTrayAvailableReadsWatcherOwner` (фейковая шина).
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** все три проверки из Global Constraints. **Step 5: Commit** `feat(gui): window, tray and notifications on Windows and Linux`.

### Task 6: Автозапуск и обновление — Windows и Linux

**Files:** Create `internal/install/install_windows.go`, `install_linux.go` (+ тесты); `internal/update/asset.go`, `apply_darwin.go`, `apply_windows.go`, `apply_linux.go` (+ тесты); Modify `internal/update/checker.go`, `internal/gui/service.go` (`InstallUpdate`, `SettingsView.AutostartAvailable`).

**Interfaces:**
- `update.AssetName(goos string) string`: `darwin` → `sd-sync-immich`, `windows` → `SD-Sync-Immich-Setup-x64.exe`, `linux` → `SD-Sync-Immich-x86_64.AppImage`; `Client.Check` выбирает файл для `runtime.GOOS`.
- `update.Apply(ctx context.Context, c *Client, rel *Release) (quit bool, err error)` по системам: Mac — нынешнее поведение (замена исполняемого файла, переподпись, `OpenWhenExited`); Windows — скачать установщик в `%TEMP%`, проверить SHA-256, запустить отдельным процессом `"<setup>.exe" /S /RELAUNCH`, вернуть `quit=true`; Linux — путь из `$APPIMAGE`, скачать во временный файл рядом, проверить, `chmod 0755`, атомарно заменить, запустить новый AppImage отдельным процессом, `quit=true`; пустой `$APPIMAGE` → ошибка «обновите вручную с GitHub», ничего не трогать. `Service.InstallUpdate` вызывает `Apply` и выходит с кодом 0, если `quit`.
- `install` на Windows: значение `SD Sync Immich` в `HKCU\…\Run` = `"<exe>"`; `AutostartAvailable` — `true`. На Linux: `~/.config/autostart/sd-sync-immich.desktop` с `Exec=` = `$APPIMAGE` (или исполняемый файл); `AutostartAvailable` — `true`, только если `$APPIMAGE` задан.
- [ ] **Step 1: Failing tests:** `TestAssetName` (таблица); `TestCheckPicksPlatformAsset` (фейковый релиз со всеми файлами); `TestLinuxUpdateWithoutAppImage` (Review Focus 3); `TestLinuxUpdateReplacesAppImage` (временный файл, проверка суммы, права 0755, шов запуска); `TestWindowsUpdateRunsInstallerSilently` (шов запуска получил `/S /RELAUNCH`, сумма проверена); `TestWindowsAutostartRunValue`, `TestLinuxAutostartDesktopFile` (временный HOME/шов реестра).
- [ ] **Step 2:** FAIL. **Step 3:** Implement. **Step 4:** все три проверки. **Step 5: Commit** `feat(update,install): autostart and self-update on Windows and Linux`.

### Task 7: Сборка установщика, AppImage и релиз

**Files:** Create `build/windows/installer.nsi`, `scripts/build-windows.ps1`, `scripts/build-linux.sh`, `build/linux/` (desktop-файл, иконка); Modify `.github/workflows/release.yml`, `README.md`, `scripts/build-app.sh` (Mac `checksums.txt` → только свои файлы, общий собирается в релизе).

- Установщик NSIS: `RequestExecutionLevel user`, `InstallDir "$LOCALAPPDATA\Programs\SD Sync Immich"`, ярлык в «Пуск», деинсталлятор (удаляет программу, ярлык, значение автозапуска; данные пользователя не трогает), значение автозапуска при установке; перед копированием ждёт до 30 с завершения `sd-sync-immich.exe`, затем завершает его; ключ `/RELAUNCH` — запустить приложение после установки.
- AppImage: сборка на `ubuntu-22.04` с WebKitGTK 4.1 средствами Wails; имя `SD-Sync-Immich-x86_64.AppImage`.
- `release.yml`: три задания сборки + итоговое: собирает все файлы, считает один `checksums.txt` (Mac-файлы, `SD-Sync-Immich.zip`, `.exe`, `.AppImage`), публикует релиз.
- README: раздел «Windows и Linux (бета)»: как поставить, предупреждения SmartScreen и как пройти, GNOME + AppIndicator, где данные и логи, как сообщить об ошибке (issues).
- [ ] **Step 1:** `scripts/build-linux.sh` в Docker собирает AppImage локально (только сборка; не запускать). **Step 2:** `release.yml` проверить в ветке без публикации: временно `workflow_dispatch` с `publish: false` → артефакты есть. **Step 3: Commit** `build: Windows installer, Linux AppImage, three-platform release`.

### Task 8: Окно — бета, ссылки, выход, предохранитель

**Files:** Modify `internal/gui/service.go`, `internal/gui/views.go`, `frontend/src/components/Sidebar.svelte`, `frontend/src/sections/Settings.svelte`, `frontend/src/lib/mock.ts`, `frontend/locales/*.json`; Create `internal/gui/appinfo.go` (+ `_darwin/_windows/_linux` для версии системы), тесты.

**Interfaces:**
- `Service.AppInfo() AppInfoView{Version, OS, OSVersion string; Beta bool}` (`OS` — `macOS`/`Windows`/`Linux`; `OSVersion` — Mac `sw_vers -productVersion` через шов, Windows — версия и сборка, Linux — `PRETTY_NAME` из `/etc/os-release`; `Beta` — не Mac); `Service.Quit()` — выход с кодом 0.
- `issueURL(info AppInfoView) string` в Go (тестируется) или в TS: `…/issues/new?body=` с текстом «**Version:** {v}\n**OS:** {OS} {OSVersion}\n\n**What happened:**\n\n**Steps to reproduce:**\n» (URL-кодирование), без адреса сервера.
- Подвал боковой панели: версия, плашка «Beta» (если `Beta`), значок GitHub (`desktop.OpenURL` через `Service.OpenURL(url)` с белым списком двух адресов), значок жука, кнопка «Выход». Подсказки (title/aria-label) на 8 языках.
- Settings: строка подтверждения автоформатирования — новый текст из Global Constraints, кнопка «Включить» неактивна 10 с с обратным отсчётом в подписи, отсчёт заново при каждом открытии; mock повторяет.
- [ ] **Step 1: Failing tests:** `TestIssueURL` (содержит версию и систему, закодировано, нет `immich`-адреса сервера), `TestOpenURLAllowlist` (только репозиторий и issues), `TestAppInfoBeta` (Mac → false, иначе true).
- [ ] **Step 2:** FAIL. **Step 3:** Implement (Go + фронтенд + локали). **Step 4:** проверки + скриншоты на заглушке (`/tmp/cdp-shot/shot.mjs`, как раньше): подвал на «Windows» (`?os=windows` в mock), подтверждение с отсчётом. **Step 5: Commit** `feat(ui): beta badge, GitHub and issue links, quit, 10-second format confirmation`.
