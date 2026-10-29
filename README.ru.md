<p align="center">
  <img src="site/assets/icon.svg" width="96" height="96" alt="">
</p>

<h1 align="center">SD Sync Immich</h1>

<p align="center"><b>Вставил SD-карту — фото уже в <a href="https://immich.app">Immich</a>.</b></p>

<p align="center">
  <a href="https://brofrong.github.io/sd-sync-immich/"><b>Сайт</b></a> ·
  <a href="https://brofrong.github.io/sd-sync-immich/downloads/"><b>Скачать</b></a> ·
  <a href="README.md">English</a> · Русский
</p>

<p align="center">
  <a href="https://github.com/brofrong/sd-sync-immich/releases/latest"><img alt="Latest release" src="https://img.shields.io/github/v/release/brofrong/sd-sync-immich?style=flat-square&color=7b5cff"></a>
  <a href="https://github.com/brofrong/sd-sync-immich/releases"><img alt="Downloads" src="https://img.shields.io/github/downloads/brofrong/sd-sync-immich/total?style=flat-square&color=0a84ff"></a>
  <a href="https://github.com/brofrong/sd-sync-immich/actions/workflows/ci.yml"><img alt="CI" src="https://img.shields.io/github/actions/workflow/status/brofrong/sd-sync-immich/ci.yml?branch=main&style=flat-square&label=CI"></a>
  <img alt="macOS, Windows, Linux" src="https://img.shields.io/badge/macOS%20%C2%B7%20Windows%20%C2%B7%20Linux-555?style=flat-square">
  <a href="LICENSE"><img alt="MIT license" src="https://img.shields.io/github/license/brofrong/sd-sync-immich?style=flat-square&color=30d158"></a>
</p>

Маленькое приложение в menu bar (на Windows и Linux — в трее). Оно само
замечает карту фотоаппарата и загружает в Immich только новые снимки. Больше
не нужно вспоминать, какое фото с карты было загружено последним.

**[Скачать для своей системы →](https://brofrong.github.io/sd-sync-immich/)**
macOS 13+, Windows 10/11 и Linux x86_64 (Windows и Linux — бета). Нужен
сервер Immich v3. Бесплатно, открытый код.

![Окно приложения во время загрузки карты](site/assets/status-ru.jpg)

## Что умеет

- **Загружает само.** Вставили карту — загрузка началась. Ничего не нужно
  нажимать.
- **Помнит, что уже загружено.** У каждой карты свой прогресс: в следующий раз
  уходят только новые снимки.
- **Галерея прямо с карты.** Фото по дням, новые сверху, у каждого отметка: в
  Immich, загружается, ждёт или ошибка. По клику фото открывается крупно,
  листать — стрелками. Удобно, чтобы показать съёмку, пока она ещё грузится.
- **Без дублей.** Сверяет дату файла и контрольную сумму, как
  [immich-go](https://github.com/simulot/immich-go).
- **Переживает обрывы.** Пропала сеть или сервер недоступен — приложение
  остановится и продолжит с того же места, когда Immich снова на связи.
- **Фото, RAW и видео.** Всё, что принимает Immich, вместе с XMP-файлами, в
  несколько потоков. Само находит снимки на картах Canon, Nikon, Sony,
  Fujifilm, Panasonic, Olympus, GoPro, DJI и других камер.
- **RAW+JPEG — одно фото.** RAW и снятый вместе с ним JPEG или HEIF
  собираются в Immich в стек, сверху JPEG. Можно выключить в настройках.
- **Карта сразу готова.** После загрузки карта извлекается — её можно
  вынимать. По желанию приложение её ещё и отформатирует, но только когда
  проверит, что каждый файл есть в Immich.
- **История.** Видно, когда и сколько загружено и что не получилось; файлы с
  ошибками можно загрузить ещё раз одной кнопкой.
- **Уведомления** о том, что загрузка закончилась, о проблемах с сервером или о
  том, что вставлена другая карта. Каждый вид включается отдельно.
- **Не мешает.** Может запускаться при входе в систему и обновляется само.
- **8 языков:** русский, английский, немецкий, испанский, французский,
  португальский, китайский и японский.

![Галерея: фото с карты по дням](site/assets/gallery-ru.jpg)

## Чем отличается от других способов загрузки

- **Мобильное приложение Immich** сохраняет фото с телефона. SD Sync Immich —
  для карты фотоаппарата: не нужно сначала копировать снимки на телефон или
  компьютер.
- **[immich-go](https://github.com/simulot/immich-go) и
  [Immich CLI](https://docs.immich.app/features/command-line-interface)** —
  программы для командной строки: их запускают вручную и указывают папку.
  Они отлично подходят для больших импортов, например архива из Google
  Фото. SD Sync Immich — для обычной карты после съёмки: сам замечает карту,
  загружает с неё только новое и извлекает, когда всё готово.

## Приватность

Приложение общается с вашим сервером Immich и с GitHub — чтобы проверить и
скачать новую версию. Никаких аккаунтов. Ключ API хранится на вашем
компьютере и отправляется только на ваш сервер.

Анонимная статистика выключена, пока вы её не разрешите: приложение один раз
спрашивает, а включить или выключить её можно в **Настройки → Статистика**.
Когда она включена, приложение отправляет версию и систему, сколько файлов
было в каждой загрузке и какие возникали ошибки, со случайным id, который
меняется при каждом включении. Никогда — имена файлов, пути, адрес сервера и
ключ API. Подробнее — в [SECURITY.md](SECURITY.md).

## Как сделано

Большая часть кода, тестов и текстов написана с помощью ИИ (Claude, через
Claude Code) — в коммитах, которые он писал, это указано. Я решаю, что и как
делает приложение, и пользуюсь им сам: на macOS с картами своей камеры с
июля 2026 года. Каждое изменение проходит автоматические тесты на macOS,
Windows и Linux; тесты работают с поддельным сервером и временными папками и
никогда не трогают настоящие карты и диски. Сборки для Windows и Linux
вручную проверены гораздо меньше, поэтому они — бета. Если на вашей системе
что-то не работает, [напишите](https://github.com/brofrong/sd-sync-immich/issues/new/choose).

## Установка

Самый простой путь — [страница загрузки](https://brofrong.github.io/sd-sync-immich/):
она сама предложит файл для вашей системы. Все файлы — на странице
[Releases](https://github.com/brofrong/sd-sync-immich/releases/latest).

### macOS

1. Скачайте **`SD-Sync-Immich.dmg`**, откройте его и перетащите
   **SD Sync Immich** на папку **Applications**.
2. Приложение не подписано Apple Developer ID, поэтому первый запуск macOS
   заблокирует. Откройте **Системные настройки → Конфиденциальность и
   безопасность** и нажмите **Всё равно открыть** у SD Sync Immich. Или в
   Терминале:

   ```bash
   xattr -dr com.apple.quarantine /Applications/"SD Sync Immich.app"
   ```

Или через [Homebrew](https://brew.sh) вместо шага 1 (шаг 2 тот же):

```bash
brew tap brofrong/tap
brew trust --cask brofrong/tap/sd-sync-immich
brew install --cask sd-sync-immich
```

### Windows (бета)

1. Скачайте и запустите **`SD-Sync-Immich-Setup-x64.exe`**. Права
   администратора не нужны.
2. Установщик не подписан, поэтому SmartScreen предупредит о нём: нажмите
   **Подробнее → Выполнить в любом случае**. Edge может ещё при скачивании
   сказать, что файл «скачивают нечасто»: **…** → **Сохранить**.

Приложение живёт в трее (значок может быть спрятан под стрелкой «^» на панели
задач).

### Linux (бета)

1. Скачайте **`SD-Sync-Immich-x86_64.AppImage`**, положите туда, где он будет
   жить (например, в `~/Applications`), и запустите:

   ```bash
   chmod +x SD-Sync-Immich-x86_64.AppImage
   ./SD-Sync-Immich-x86_64.AppImage
   ```

2. Если AppImage ругается на FUSE, установите пакет `fuse3` (или `fuse`).

Или Flatpak вместо AppImage: скачайте **`SD-Sync-Immich-x86_64.flatpak`** и
установите:

```bash
flatpak install --user SD-Sync-Immich-x86_64.flatpak
```

Приложение появится в меню приложений. Flatpak-версия не обновляется сама:
новую версию скачайте и установите так же.

Подходят Ubuntu 22.04, Debian 12, Fedora 36 и новее. В GNOME для значка в трее
нужно расширение
[AppIndicator](https://extensions.gnome.org/extension/615/appindicator-support/)
(в Ubuntu оно уже есть); KDE и другие рабочие столы показывают трей сами.

## Первый запуск

1. Откройте окно приложения: в menu bar → **Открыть SD Sync Immich** (на
   Windows и Linux — кликом по значку в трее).
2. **Настройки → Сервер:** адрес вашего Immich (например,
   `https://immich.example.com`) и ключ API. Ключ создаётся в Immich:
   **Account Settings → API Keys → New API Key**, с правами `asset.upload` и
   `stack.create` (второе — чтобы собирать пары RAW+JPEG в стек). Нажмите
   **Проверить подключение** и **Сохранить**.
3. Вставьте карту → раздел **Карты** → **Выбрать**.
4. Всё. Дальше загрузка начинается сама, как только карта вставлена.

Если хотите, чтобы приложение работало всегда, включите **Настройки →
Автозапуск**.

## Карта после загрузки

В **Настройках → Карта** два переключателя:

- **Извлекать карту после загрузки** (включено). Когда всё загрузилось без
  ошибок, карта извлекается — можно вынимать.
- **Форматировать карту после загрузки** (выключено). Приложение проверяет,
  что каждое фото и видео с карты есть в Immich, ждёт 30 секунд (можно
  отменить) и форматирует карту. Если хоть один файл не найден, карта не
  трогается, а приложение пишет почему.

> **Внимание:** форматирование стирает с карты **всё**, не только фото и
> видео. Фото, лежащие в корзине Immich, загруженными не считаются.

На Windows форматировать карту без прав администратора система не даёт, а на
Linux — если просит пароль; тогда приложение просто сообщит об этом, и карту
можно отформатировать в камере.

## Если что-то не так

Журнал работы открывается в окне: **Настройки → Открыть журнал**. Файлы
настроек и журнала:

| | Настройки и история | Журнал (`app.log`) |
|---|---|---|
| macOS | `~/.config/sd-sync-immich` | `~/Library/Logs/sd-sync-immich` |
| Windows | `%APPDATA%\sd-sync-immich` | `%LOCALAPPDATA%\sd-sync-immich\logs` |
| Linux | `~/.config/sd-sync-immich` | `~/.local/state/sd-sync-immich` |
| Linux, Flatpak | `~/.var/app/io.github.brofrong.sd-sync-immich/config/sd-sync-immich` | `~/.var/app/io.github.brofrong.sd-sync-immich/.local/state/sd-sync-immich` |

Удаление приложения эти папки не трогает. Удалить приложение: на macOS —
перетащить в Корзину, на Windows — **Параметры → Приложения**, на Linux —
удалить AppImage или выполнить
`flatpak uninstall io.github.brofrong.sd-sync-immich`.

Нашли ошибку? Создайте [issue](https://github.com/brofrong/sd-sync-immich/issues/new/choose):
напишите систему, что делали и что произошло, и приложите `app.log`.
Вопросы и идеи — в [Discussions](https://github.com/brofrong/sd-sync-immich/discussions).

## Участие

Issues и pull requests приветствуются, можно по-русски. С чего начать — в
[CONTRIBUTING.md](CONTRIBUTING.md) (на английском); как собрать приложение,
команды для командной строки и как устроены релизы — в
[docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).

## Лицензия

[MIT](LICENSE): можно свободно использовать, менять и распространять, в том
числе в коммерческих целях, сохраняя уведомление об авторских правах.

Проект не связан с командой Immich.
