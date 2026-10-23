# Жизненный цикл карты — план реализации

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** «вставил — забыл»: Мак не засыпает во время синхронизации, карта после форматирования подхватывается сама, по окончании карта извлекается, при желании — форматируется после полной проверки, уведомления сообщают о важном с кнопками, история показывает файлы с ошибками и повторяет только их.

**Architecture:** `uploader` начинает отдавать список ошибок по файлам, умеет синхронизировать подмножество файлов и проверять карту целиком через `bulk-upload-check`. `volume` получает `Erase`, новый пакет `power` — запрет сна через `caffeinate`. В `internal/app` после результата синхронизации появляется фаза «после синхронизации» (проверка → отсчёт → форматирование → извлечение) в той же горутине, с новыми кодами статуса и «событиями» (`Notice`). `internal/notify` переходит на нативные уведомления Wails с запасным `osascript`; `internal/gui` решает, какое уведомление показать, и маршрутизирует кнопки. Фронтенд получает настройки, новые состояния карточки и список ошибок в истории.

**Tech Stack:** Go 1.26, Wails `v3.0.0-beta.34` (`pkg/services/notifications`), Svelte 5 + TS, SQLite (`modernc.org/sqlite`), `diskutil`, `caffeinate`.

**Spec:** `docs/superpowers/specs/2026-10-23-card-lifecycle-design.md`

## Global Constraints

- macOS 13+, Wails ровно `v3.0.0-beta.34`, CGO-флаги сборки как в `scripts/build-app.sh` (`-O2 -g -mmacosx-version-min=13.0`).
- Go не отдаёт тексты: только `code`, `params`, `actions`; все строки в `frontend/locales/*.json` на 8 языках (`en`, `ru`, `de`, `es`, `fr`, `pt-BR`, `zh-Hans`, `ja`); тесты паритета ключей/плейсхолдеров и `internal/i18n/app_keys_test.go` (каждый `Code` → `status.<code>.title/detail`, каждый `Action` → `action.<a>`) должны проходить. Логи — английский.
- Значения по умолчанию: автоизвлечение **вкл.**, автоформатирование **выкл.**, все уведомления **вкл.**; отсутствующие в старом `config.yaml` ключи получают эти значения.
- Форматирование: только выбранная карта, только целиком, тот же формат (`exfat`→`ExFAT`, `msdos`→`MS-DOS FAT32`/`MS-DOS FAT16`) и то же имя; отсчёт **30 с**; только `done`/`nothing_new` без ошибок; файлы из корзины Immich (`isTrashed`) — не подтверждены.
- Автоизвлечение: только после `done` (загружен ≥1 файл, 0 ошибок) или после успешного форматирования; после `nothing_new`, ошибок, остановки — нет.
- Список ошибок: не больше **1000** файлов на синхронизацию.
- Сон: `caffeinate -i -w <pid>` на всё время синхронизации и фазы после неё.
- **Безопасность разработки:** на машине автора работает живая установка (LaunchAgent `com.sd-sync-immich`, `~/.config/sd-sync-immich`, `state.db`) и вставлена настоящая карта. Никогда: запускать собранное приложение/бинарник, вызывать `diskutil eraseVolume`/`eject`, `launchctl`, `caffeinate` по-настоящему. Всё — через швы; тесты подменяют их.
- Перед каждым коммитом: `export CGO_CFLAGS="-O2 -g -mmacosx-version-min=13.0" CGO_LDFLAGS="-O2 -g -mmacosx-version-min=13.0"`, `go vet ./...`, `go test -race ./...`; если менялся фронтенд или локали — `scripts/build-frontend.sh` и `cd frontend && npm run check`.

## Review Focus

1. Во время отсчёта карту заменили другой с тем же именем в той же точке монтирования → форматирования нет (сверка UUID непосредственно перед `Erase`). Тест: Task 5 `TestFormatSkippedWhenCardChanged`.
2. Файл есть в Immich, но в корзине → не подтверждён, форматирования нет. Тест: Task 3 `TestVerifyCardTrashedIsMissing`.
3. Два одинаковых по содержимому файла на карте → оба подтверждаются (проверка по пути, а не по контрольной сумме как ключу). Тест: Task 3 `TestVerifyCardSameContentTwice`.
4. Карта открыта в Finder → извлечение не удалось → одно событие `eject_failed`, статус остаётся `done`, без повторов. Тест: Task 4 `TestAutoEjectFailureKeepsStatus`.
5. Старый `config.yaml` без новых ключей → автоизвлечение вкл., автоформатирование выкл., уведомления вкл. Тест: Task 1 `TestLoadOldConfigGetsCardDefaults`.

---

### Task 1: Конфиг и хранилище ошибок

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `internal/state/sqlite.go`, `internal/state/runs.go`
- Create: `internal/state/failures.go`, `internal/state/failures_test.go`

**Interfaces:**
- Produces:
  - `config.CardConfig{AutoEject bool \`yaml:"auto_eject"\`; AutoFormat bool \`yaml:"auto_format"\`}`, поле `Config.Card CardConfig \`yaml:"card"\``.
  - `config.NotifyConfig{Enabled, Done, Errors, OtherCard, Server, Card bool}` (yaml: `enabled`, `done`, `errors`, `other_card`, `server`, `card`), поле `Config.Notifications NotifyConfig \`yaml:"notifications"\``.
  - `config.KnownMediaExtensions() map[string]struct{}` — нижний регистр, без точки; это существующий `knownCameraExtensions`.
  - `state.FileFailure{RelativePath, Kind, Detail string}` (json: `relativePath`, `kind`, `detail`).
  - `(*state.DB).RecordFailures(runID int64, failures []FileFailure) error` — пишет строки в `sync_run_failures(run_id, relative_path, kind, detail)` и ставит `sync_runs.failures_recorded = 1` (даже для пустого списка), в одной транзакции.
  - `(*state.DB).RunFailures(runID int64) ([]FileFailure, error)` — по порядку вставки.
  - `state.SyncRun.FailuresRecorded bool \`json:"failuresRecorded"\``.

- [ ] **Step 1: Failing tests.** `config_test.go`: `TestLoadOldConfigGetsCardDefaults` — HOME во временную папку, записать YAML без `card`/`notifications` (только `immich`, `watch`), `Load()` → `Card.AutoEject == true`, `Card.AutoFormat == false`, все поля `Notifications` == true; `TestKnownMediaExtensions` — содержит `arw`, `hif`, `mxf`, не содержит `xml`, `xmp`. `failures_test.go`: `TestRecordAndReadFailures` (2 записи → `RunFailures` возвращает их по порядку, `RecentRuns()[0].FailuresRecorded == true`); `TestRunWithoutRecordedFailures` (у нового запуска без `RecordFailures` — `FailuresRecorded == false`, `RunFailures` → пусто); `TestOpenAddsFailuresRecordedColumn` — по образцу `TestOpenAddsDuplicatesColumn` в `runs_test.go`.
- [ ] **Step 2:** `go test ./internal/config/ ./internal/state/` — FAIL (нет типов).
- [ ] **Step 3: Implement.** `Default()` выставляет значения из Global Constraints. В `migrate()` — `CREATE TABLE IF NOT EXISTS sync_run_failures (...)` с индексом по `run_id` и `addColumnIfMissing("sync_runs", "failures_recorded", "INTEGER NOT NULL DEFAULT 0")`; добавить колонку в запросы `RecentRuns`/`LastFinishedRun`/`scanSyncRun`.
- [ ] **Step 4:** тесты PASS, `go vet ./...`.
- [ ] **Step 5: Commit** `feat(state,config): card and notification settings; per-file failure records`.

### Task 2: Загрузчик: ошибки по файлам и повтор подмножества

**Files:**
- Modify: `internal/uploader/uploader.go`, `internal/uploader/counts_test.go`, `internal/uploader/retry_test.go`, `internal/app/app.go` (вызов `Sync`)
- Create: `internal/uploader/failures_test.go`

**Interfaces:**
- Consumes: `state.FileFailure` (Task 1).
- Produces:
  - `uploader.SyncOptions{DryRun bool; Only []string}`; сигнатура меняется на `(*Service).Sync(ctx context.Context, vol volume.Info, opts SyncOptions, onProgress ProgressFunc) (Result, error)`. `Only` — относительные пути: если не пусто, кандидаты — отсканированные файлы с этими путями, без фильтра по курсору (курсор при этом не откатывается), `Skipped` = остальные отсканированные.
  - `Result.Failures []state.FileFailure` — до `maxRecordedFailures = 1000` штук; `Detail` — `immich.Detail(err)` (для ошибок чтения — текст ошибки).

- [ ] **Step 1: Failing tests** в `failures_test.go`: `TestUploadAllRecordsFailures` — сервер отклоняет `DSC00001` (400): `res.Failures == [{DCIM/DSC00001.JPG, "rejected", <непустой detail>}]`; `TestFailuresAreCapped` — подменить `maxRecordedFailures` на 2 (сделать `var`), 3 отклонённых файла → `len(res.Failures) == 2`, `res.Errors == 3`; `TestSyncOnlyUploadsListedFiles` — `Service.Sync` по временной «карте» (`DCIM/` с 3 файлами, `config.Store` во временном HOME, курсор уже за всеми файлами), `Only: ["DCIM/B.JPG"]` → на сервер пришёл ровно один запрос с `B.JPG`, `res.Uploaded == 1`, `res.Skipped == 2`.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3: Implement.** Сбор ошибок — в `syncOne` (там уже есть `err` и `kind`), потокобезопасно, рядом с `errorTally`. Обновить вызов в `internal/app/app.go` на `SyncOptions{DryRun: a.dryRun}` и существующие тесты под новую сигнатуру.
- [ ] **Step 4:** `go test -race ./internal/uploader/ ./internal/app/` PASS.
- [ ] **Step 5: Commit** `feat(uploader): per-file failures and syncing a chosen subset`.

### Task 3: Проверка карты перед форматированием

**Files:**
- Modify: `internal/immich/client.go`, `internal/immich/client_test.go`
- Create: `internal/uploader/verify.go`, `internal/uploader/verify_test.go`

**Interfaces:**
- Consumes: `config.KnownMediaExtensions()` (Task 1); существующие `config.Config.ScanPaths()`, `ExtensionSet()`, `Scan.ExcludePatterns`, `scanner.ComputeSHA1`, `state.DB.Get`.
- Produces:
  - `immich.BulkCheckAsset{ID, Checksum string}`; `immich.BulkCheckResult{ID, Action, Reason, AssetID string; IsTrashed bool}` (json `id`, `action`, `reason`, `assetId`, `isTrashed`); `(*immich.Client).BulkUploadCheck(ctx, assets []BulkCheckAsset) ([]BulkCheckResult, error)` — `POST /assets/bulk-upload-check`, ответ ограничен `io.LimitReader` (как `Check`), не-2xx → `*StatusError`.
  - `uploader.VerifyResult{Media int; MissingCount int; Missing []string /* первые 20 */; Unsupported map[string]int /* расширение → число */}`, `(VerifyResult).Safe() bool` = `MissingCount == 0 && len(Unsupported) == 0`.
  - `(*Service).VerifyCard(ctx context.Context, vol volume.Info, onProgress func(done, total int)) (VerifyResult, error)`.

Правила `VerifyCard`: обходит **весь** `vol.MountPoint`; файлы по `ExcludePatterns` и не-медиа (расширение не в `KnownMediaExtensions() ∪ ExtensionSet()`) пропускает. Медиа-файл вне `ScanPaths()` или с расширением не из `ExtensionSet()` → `Unsupported[ext]++`. Остальные: SHA-1 из `state.Get(vol.UUID, rel)`, если размер и mtime совпадают и `SHA1 != ""`, иначе `scanner.ComputeSHA1`; проверка пачками по 200, `ID` = относительный путь. Подтверждён ⇔ `Action == "reject" && Reason == "duplicate" && !IsTrashed`.

- [ ] **Step 1: Failing tests.** `client_test.go`: `TestBulkUploadCheckParsesResults` (тело запроса содержит оба актива, разбирается `isTrashed`). `verify_test.go` (фейковый сервер отвечает по контрольной сумме): `TestVerifyCardAllConfirmed` (`Safe()`, `Media == 3`, `onProgress` дошёл до `3, 3`); `TestVerifyCardMissing` (один `accept` → `MissingCount == 1`, `Missing` содержит путь); `TestVerifyCardTrashedIsMissing`; `TestVerifyCardSameContentTwice` (два файла с одинаковым содержимым, оба `duplicate` → `Safe()`); `TestVerifyCardUnsupported` (`PRIVATE/OTHER/A.HIF` вне путей сканирования → `Unsupported["hif"] == 1`, не `Safe()`); `TestVerifyCardIgnoresCameraJunk` (`PRIVATE/M4ROOT/CLIP/C0001M01.XML`, `._DSC.JPG`, `AVF_INFO/AVIN0001.BNP` не влияют); `TestVerifyCardBatches` (450 файлов → 3 запроса).
- [ ] **Step 2:** FAIL.
- [ ] **Step 3: Implement** в `verify.go`.
- [ ] **Step 4:** PASS.
- [ ] **Step 5: Commit** `feat(uploader): verify a whole card against Immich`.

### Task 4: Форматирование тома и запрет сна

**Files:**
- Create: `internal/volume/erase_darwin.go`, `internal/volume/erase_darwin_test.go`
- Create: `internal/power/power_darwin.go`, `internal/power/power_darwin_test.go`

**Interfaces:**
- Produces:
  - `volume.Erase(mountPoint string) (Info, error)`: через шов `runDiskutil` читает `diskutil info -plist <mountPoint>` (`DeviceIdentifier`, `FilesystemType`, `FilesystemName`, `VolumeName`, `Removable`/`Ejectable`/`External`), отказывает (ошибка, без вызова erase), если том не съёмный или тип не `exfat`/`msdos`; вызывает `diskutil eraseVolume <ExFAT|MS-DOS FAT32|MS-DOS FAT16> <VolumeName> <DeviceIdentifier>` (FAT16 — если `FilesystemName` содержит `FAT16`); затем ждёт монтирования `/Volumes/<VolumeName>` (шов `waitForMount`, по умолчанию `WaitForMount`, таймаут 60 с) и возвращает `Info` нового тома (шов `getInfo`, по умолчанию `GetInfo`).
  - `power.KeepAwake() (release func())`: запускает `caffeinate -i -w <os.Getpid()>` (шов `var startCaffeinate func(pid int) (stop func(), err error)`); ошибка запуска только логируется, `release` всё равно безопасна; `release` идемпотентна.

- [ ] **Step 1: Failing tests.** `erase_darwin_test.go` (фейковый `runDiskutil` с plist): `TestEraseExFATKeepsNameAndFormat` (аргументы `eraseVolume ExFAT BROFRONG disk4s1`, возвращает новый UUID из фейкового `getInfo`); `TestEraseFAT32` и `TestEraseFAT16`; `TestEraseRefusesNonRemovable` и `TestEraseRefusesAPFS` (erase не вызывался); `TestEraseReportsDiskutilOutput` (ошибка содержит вывод). `power_darwin_test.go`: `TestKeepAwakeStartsAndStops` (фейк получил `os.Getpid()`, `release` вызывает stop ровно один раз даже при двойном вызове); `TestKeepAwakeStartFailure` (ошибка запуска → `release` не паникует).
- [ ] **Step 2:** FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** PASS.
- [ ] **Step 5: Commit** `feat(volume,power): erase a card in place; keep the Mac awake`.

### Task 5: App — сон, автоизвлечение, подхват карты, ошибки и повтор

**Files:**
- Modify: `internal/app/app.go`, `internal/app/status.go`, `internal/app/status_test.go`
- Create: `internal/app/lifecycle.go`, `internal/app/lifecycle_test.go`
- Modify: `frontend/locales/*.json` (8 файлов)

**Interfaces:**
- Consumes: Task 1 (`Card`, `RecordFailures`, `RunFailures`), Task 2 (`SyncOptions`, `Result.Failures`), Task 4 (`power.KeepAwake`), существующие `volume.Eject`, `ForgetCard`.
- Produces:
  - Коды: `CodeEjected Code = "ejected"` (params `uploaded`; actions `[open_immich]`).
  - Действия: `ActionOpenHistory Action = "open_history"`; `actionsFor(CodeDoneWithErrors)` → `[open_history]`.
  - `app.Notice{Kind NoticeKind; Params map[string]string}`; `NoticeCardFollowed = "card_followed"` (params `name`), `NoticeEjectFailed = "eject_failed"` (params `name`, `detail`); `(*App).OnNotice(fn func(Notice))` — регистрировать до `Run`.
  - `(*App).RetryFailures(runID int64) error` — ошибки: `ErrRetryUnavailable` (карта запуска не выбрана/не вставлена, синхронизация идёт, список не записан или пуст).
  - Швы на `App` (ставятся в `New`, тесты подменяют): `keepAwake func() func()`, `eject func(mountPoint string) error`, `isCameraCard func(mountPoint string) bool` (есть `DCIM` или `PRIVATE/M4ROOT`).
  - `followCandidate(cfg config.Config, mounted []volume.Info, isCameraCard func(string) bool) (volume.Info, bool)` — чистая функция.

Поведение:
- Горутина синхронизации берёт `release := a.keepAwake()` в начале и отпускает в самом конце (после фазы «после синхронизации»).
- После `FinishRun` — `RecordFailures(runID, result.Failures)`.
- `startSync(only []string)`; `SyncNow` вызывает с `nil`.
- Автоизвлечение (если `Card.AutoEject` и код `done`): запомнить точку монтирования как «извлекаю сам», `eject`; успех → статус `ejected{uploaded}`; ошибка → `Notice{eject_failed}`, статус остаётся `done`, повторов нет.
- В `handleVolumeEvent`: размонтирование тома, который приложение извлекло само → `mountedTarget = nil`, без `cancelSync`, без `updateIdleStatus` (статус `ejected` остаётся до следующего монтирования или действия пользователя).
- Подхват: при монтировании не-выбранного тома и в `Run` при старте — если выбранный не вставлен и `followCandidate` даёт кандидата: `ForgetCard(старый UUID)` (это снимает выбор), затем выбрать кандидата (как `SelectCard`, без запуска синхронизации внутри), `Notice{card_followed}`, синхронизация.
- `ResultStatus`/`IdleStatus` и автопродолжение не меняются.
- Локали: `status.ejected.title/detail` («Можно вынимать карту» / «Загружено файлов: {uploaded}. Карта извлечена.»), `action.open_history` («Подробнее»), на 8 языках.

- [ ] **Step 1: Failing tests** (`lifecycle_test.go`, на `testApp` из `app_test.go` с подменёнными швами): `TestSyncKeepsMacAwake` (keepAwake вызван, release вызван после финального статуса); `TestAutoEjectAfterDone` (eject вызван с точкой монтирования, статус `ejected`, `uploaded` из результата); `TestNoAutoEjectAfterNothingNew`; `TestNoAutoEjectWhenDisabled`; `TestAutoEjectFailureKeepsStatus` (Review Focus 4: одно `eject_failed`, статус `done`); `TestOwnEjectUnmountKeepsStatus`; `TestFollowCandidate` (табличный: одно совпадение имени без учёта регистра + камера → да; два совпадения → нет; выбранный вставлен → нет; не камера → нет; другое имя → нет); `TestFollowFormattedCardOnMount` (старое состояние забыто, выбран новый UUID, `card_followed`, синхронизация запущена); `TestFailuresRecordedWithRun`; `TestRetryFailuresSyncsOnlyThem` (фейковый `Uploader` нельзя — проверять через сервер как в `TestSyncNowRecordsRun`: в `Only` ушли пути из `RunFailures`); `TestRetryFailuresUnavailable` (карта не вставлена → `ErrRetryUnavailable`). `status_test.go`: строка `done_with_errors` → `[open_history]`.
- [ ] **Step 2:** FAIL.
- [ ] **Step 3: Implement**; локали.
- [ ] **Step 4:** `go test -race ./internal/app/ ./internal/i18n/` PASS.
- [ ] **Step 5: Commit** `feat(app): keep awake, auto-eject, follow a formatted card, retry failed files`.

### Task 6: App — автоформатирование

**Files:**
- Modify: `internal/app/lifecycle.go`, `internal/app/status.go`, `internal/app/app.go`
- Create: `internal/app/format_test.go`
- Modify: `frontend/locales/*.json`

**Interfaces:**
- Consumes: Task 3 (`VerifyCard`, `VerifyResult`), Task 4 (`volume.Erase`), Task 5 (фаза после синхронизации, `eject`, «извлекаю сам», `ForgetCard`).
- Produces:
  - Коды и params: `verifying` (Progress: `Current`, `Total`; actions `[stop]`), `format_pending` (`seconds`, `name`; actions `[cancel_format]`), `formatting` (`name`), `formatted` (`name`), `format_skipped_missing` (`count`), `format_skipped_unsupported` (`files` — например `12 × .HIF, 3 × .MXF`, собирается в Go из `Unsupported` без слов), `format_failed` (`detail`; actions `[open_logs]`).
  - `ActionCancelFormat Action = "cancel_format"`; `(*App).CancelFormat()` — отменяет текущий отсчёт, иначе ничего.
  - Швы: `erase func(mountPoint string) (volume.Info, error)` (по умолчанию `volume.Erase`), `verify func(ctx, volume.Info, func(int, int)) (uploader.VerifyResult, error)` (по умолчанию `a.Uploader.VerifyCard`), `formatCountdown time.Duration` (30 с; тесты — миллисекунды), `getInfo func(mountPoint string) (*volume.Info, error)`.

Поведение (до автоизвлечения из Task 5): если `Card.AutoFormat` и код `done`/`nothing_new` и `result.Errors == 0` → `verifying` → не `Safe()` → соответствующий `format_skipped_*`, дальше обычное правило извлечения → `Safe()` → `format_pending` раз в секунду (`seconds` убывает) до конца отсчёта; `CancelFormat` → вернуть статус результата, дальше обычное правило извлечения; `StopSync`/вынули карту → тихо выйти (статус как при остановке/извлечении). Перед `erase`: `getInfo(mountPoint)` и сверка UUID с выбранным — не совпало → тихо выйти. Пока идёт `erase`, `handleVolumeEvent` игнорирует события этой карты. Успех → `ForgetCard(старый)`, выбрать новый `Info`, статус `formatted`, затем извлечение, если `AutoEject` (результат `ejected`). Ошибка → `format_failed`, без извлечения. Отсчёт не сохраняется (перезапуск = нет форматирования).

- [ ] **Step 1: Failing tests** (`format_test.go`): `TestFormatAfterDone` (порядок статусов `done → verifying → format_pending(30..1) → formatting → formatted → ejected`, `erase` вызван один раз, выбран новый UUID, старое состояние забыто); `TestFormatAfterNothingNew`; `TestNoFormatWhenDisabled`; `TestNoFormatAfterErrors`; `TestFormatSkippedMissing` (статус `format_skipped_missing{count}`, `erase` не вызван, карта извлечена по обычному правилу при `done`); `TestFormatSkippedUnsupported` (`files` = `1 × .HIF`); `TestCancelFormat` (во время отсчёта → `erase` не вызван); `TestFormatSkippedWhenCardChanged` (Review Focus 1: `getInfo` вернул другой UUID); `TestStopDuringCountdown`; `TestFormatFailure` (`format_failed{detail}`, `eject` не вызван); `TestVolumeEventsIgnoredWhileErasing` (событие размонтирования во время `erase` не отменяет фазу).
- [ ] **Step 2:** FAIL.
- [ ] **Step 3: Implement**; локали `status.<code>.title/detail` для 7 новых кодов и `action.cancel_format` («Отменить») на 8 языках. Тексты: `verifying` — «Проверяю, что всё есть в Immich», `format_pending` — «Карта «{name}» будет отформатирована через {seconds} с», `formatting` — «Форматирую карту…», `formatted` — «Карта отформатирована», `format_skipped_missing` — «Не форматирую: {count} файлов нет в Immich», `format_skipped_unsupported` — «Не форматирую: на карте есть файлы, которые не загружаются: {files}», `format_failed` — «Не удалось отформатировать карту: {detail}».
- [ ] **Step 4:** PASS (`./internal/app/`, `./internal/i18n/`).
- [ ] **Step 5: Commit** `feat(app): auto-format a fully verified card after a sync`.

### Task 7: Уведомления

**Files:**
- Modify: `internal/notify/notify_darwin.go`
- Create: `internal/notify/native_darwin.go`, `internal/gui/notifications.go`, `internal/gui/notifications_test.go`
- Modify: `internal/gui/app.go`, `internal/gui/service.go`, `frontend/locales/*.json`

**Interfaces:**
- Consumes: Task 1 (`NotifyConfig`, `Card`), Task 5/6 (коды, `Notice`, `ActionOpenHistory`, `ActionCancelFormat`, `CancelFormat`).
- Produces:
  - `notify.Message{ID, Title, Body string; Actions []notify.Action; Data map[string]string}`, `notify.Action{ID, Title string}`.
  - `notify.Center` с `Send(Message)`; `notify.NewCenter(svc *notifications.NotificationService, onResponse func(actionID string, data map[string]string)) *Center`: при старте запрашивает разрешение; если нативные недоступны (ошибка, отказ, нет bundle id) — шлёт через `osascript` (без кнопок); сервис регистрируется в `application.Options.Services` в `gui/app.go`.
  - В `gui`: `notificationKind(code app.Code, cfg config.Config) (kind string, ok bool)` и `noticeKind(k app.NoticeKind) string`; виды и коды:
    - `done` ← `ejected`; ← `done` только если `!Card.AutoEject && !Card.AutoFormat`;
    - `errors` ← `done_with_errors` (кнопка `open_history`);
    - `other_card` ← `other_card_mounted` (кнопка `select_card`, `Data["uuid"] = mounted_uuid`);
    - `server` ← `server_unreachable`, `auth_failed`, `server_error` (кнопка `retry`);
    - `card` ← `format_pending` (кнопка `cancel_format`), `formatted`, `format_skipped_missing`, `format_skipped_unsupported`, `format_failed`, `Notice card_followed`, `Notice eject_failed`.
  - `notifyGate` — в памяти: одно уведомление на код до сброса; сброс на `waiting_for_card`, `done`, `nothing_new`, `ejected`.
  - Включено ⇔ `Notifications.Enabled && Notifications.<вид>`.
  - Ответ: `select_card` → `SelectCard(Data["uuid"])`, `retry` → `Retry()`, `cancel_format` → `CancelFormat()`, `open_history` → показать окно и открыть «Историю»; клик по самому уведомлению → показать окно (раздел «История» для `done_with_errors`, иначе «Статус»).
  - Тексты: `notify.<code>.title/body` и `notify.<notice>.title/body` с теми же params, что у статуса; существующие `notify.done.*` сохраняются.

- [ ] **Step 1: Failing tests** (`notifications_test.go`): `TestNotificationKind` (табличный, включая правило `done` при вкл./выкл. автоизвлечении и автоформатировании); `TestNotificationsDisabled` (общий выкл. → ничего; вид выкл. → ничего); `TestNotifyGateOncePerCard` (`server_unreachable` дважды → одно; после `done` — снова можно); `TestFormatPendingNotifiedOnce` (смена только params не шлёт повтор); `TestNotificationActions` (у `other_card_mounted` действие `select_card` и `Data["uuid"]`).
- [ ] **Step 2:** FAIL.
- [ ] **Step 3: Implement**; заменить отправку в `Service.onStatus`; подписаться на `OnNotice`; локали на 8 языках.
- [ ] **Step 4:** PASS (`./internal/gui/`, `./internal/i18n/`).
- [ ] **Step 5: Commit** `feat(notify): native notifications with actions, per-kind settings`.

### Task 8: Окно — настройки, состояния, история

**Files:**
- Modify: `internal/gui/service.go`, `internal/gui/views.go`, `internal/gui/concurrency_test.go` (или новый `settings_test.go`)
- Modify: `frontend/src/sections/Settings.svelte`, `frontend/src/components/StatusCard.svelte`, `frontend/src/sections/History.svelte`, `frontend/src/lib/mock.ts`, `frontend/locales/*.json`, `README.md`

**Interfaces:**
- Consumes: всё выше.
- Produces:
  - `SettingsView` += `AutoEject bool \`json:"autoEject"\``, `AutoFormat bool \`json:"autoFormat"\``, `Notifications NotificationsView \`json:"notifications"\`` (`enabled`, `done`, `errors`, `otherCard`, `server`, `card`); `SaveSettings` сохраняет их.
  - `Service.CancelFormat()`, `Service.RetryFailures(runID int64) error`, `Service.RunFailures(runID int64) ([]state.FileFailure, error)`.

UI:
- «Настройки»: «Извлекать карту после загрузки» (переключатель); «Форматировать карту после загрузки» — при включении строка подтверждения «Все файлы с карты будут удалены после проверки. Включить?» [Отмена] [Включить], подсказка из спецификации; блок «Уведомления»: общий выключатель и пять по видам (неактивны, когда общий выключен).
- Карточка статуса: `verifying` — полоса прогресса `Current/Total`; `format_pending` — обратный отсчёт и кнопка «Отменить» (`cancel_format` → `backend.CancelFormat()`); `open_history` → раздел «История».
- «История»: у синхронизации с ошибками строка раскрывается списком «путь — вид ошибки (переведённый `error_kind.*`) — подробности»; для `failuresRecorded == false` — «Список файлов недоступен для старых синхронизаций»; кнопка «Повторить ошибки» вызывает `RetryFailures`, при ошибке показывает «Вставьте карту «{name}», чтобы повторить».
- `mock.ts` повторяет поведение Go (новые поля, методы, состояния `?state=format_pending` и т.п.).
- README: раздел о новых настройках и предупреждение про форматирование.

- [ ] **Step 1: Failing Go test** `TestSaveSettingsCardAndNotifications` — через чистую функцию `applySettings(c *config.Config, v SettingsView)`, которую использует `SaveSettings`: поля переносятся в конфиг.
- [ ] **Step 2:** FAIL. **Step 3:** реализовать Go, перегенерировать привязки (`scripts/build-frontend.sh`), фронтенд, локали, mock.
- [ ] **Step 4:** `npm run check` (0 ошибок), `go test -race ./...`; скриншоты в браузере на заглушке (Chrome headless по образцу `/tmp/cdp-shot/shot.mjs`): настройки, `format_pending`, раскрытая история.
- [ ] **Step 5: Commit** `feat(ui): card settings, notification settings, format states, failed files in history`.
