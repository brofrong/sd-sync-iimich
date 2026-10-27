// The landing page's only script: picks English or Russian, works out the
// visitor's system to offer the right download, and fills in the latest
// version and the star count from GitHub. Without it (or without GitHub)
// the pages still work: in English, with the download buttons pointing at
// the latest release.
(function () {
  "use strict";

  var REPO = "brofrong/sd-sync-iimich";
  var RELEASES = "https://github.com/" + REPO + "/releases/latest/download/";
  var FILES = {
    macos: "SD-Sync-Immich.dmg",
    windows: "SD-Sync-Immich-Setup-x64.exe",
    linux: "SD-Sync-Immich-x86_64.AppImage",
  };

  // The Russian text. The English is the pages' own (kept on first use in
  // data-en, to switch back).
  var RU = {
    "title.home": "SD Sync Immich: вставил SD-карту, фото уже в Immich",
    "title.downloads": "Скачать SD Sync Immich для macOS, Windows и Linux",
    "nav.downloads": "Загрузки",
    "nav.star": "Звезда на GitHub",
    "hero.title": "Вставил SD-карту&nbsp;— <em>фото уже в Immich</em>",
    "hero.lead": "Маленькое приложение в menu bar само замечает карту фотоаппарата и загружает только новые снимки. Больше не нужно вспоминать, какое фото было последним.",
    "hero.all": "macOS, Windows и Linux",
    "hero.shot_alt": "Окно приложения: карта загружается в Immich",
    "how.title": "Как это работает",
    "how.sub": "Настроить один раз, дальше просто вставлять карту.",
    "how.1.title": "Подключите Immich",
    "how.1.text": "Один раз укажите адрес сервера и ключ API и выберите карту для синхронизации.",
    "how.2.title": "Вставьте карту",
    "how.2.text": "Загрузка начнётся сама. То, что уже есть в Immich, пропускается.",
    "how.3.title": "Вынимайте",
    "how.3.text": "Когда всё загружено, карта извлекается сама. Можно снимать дальше.",
    "gallery.title": "Смотрите снимки, пока они грузятся",
    "gallery.text1": "Вкладка «Галерея» показывает фото с карты по дням, новые сверху, и на каждом отметку: в Immich, загружается или ждёт.",
    "gallery.text2": "По клику фото открывается крупно, листать — стрелками. Удобно, чтобы сразу показать кому-то съёмку.",
    "gallery.shot_alt": "Вкладка «Галерея»: фото с карты по дням, у каждого отметка «в Immich»",
    "features.title": "Что ещё умеет",
    "features.sub": "Сделано для большой карты после дня съёмки.",
    "f.1.title": "Помнит, что уже загружено",
    "f.1.text": "У каждой карты свой прогресс. В следующий раз уходят только новые фото.",
    "f.2.title": "Без дублей",
    "f.2.text": "Сверяет дату файла и контрольную сумму, как immich-go.",
    "f.3.title": "Переживает обрывы",
    "f.3.text": "Пропала сеть или лёг сервер? Остановится и продолжит с того же места.",
    "f.4.title": "Фото, RAW и видео",
    "f.4.text": "Форматы, которые принимает Immich, вместе с XMP-файлами, в несколько потоков.",
    "f.5.title": "Карта сразу готова",
    "f.5.text": "Извлекается после загрузки. По желанию форматируется, когда каждый файл проверен в Immich.",
    "f.6.title": "Не мешает",
    "f.6.text": "Живёт в menu bar или трее, может запускаться при входе, обновляется само. 8 языков.",
    "cta.title": "Бесплатно и с открытым кодом",
    "cta.text": "Если приложение экономит вам время, поставьте звезду на GitHub: так его находят другие пользователи Immich. Issues и pull requests приветствуются.",
    "cta.button": "Звезда на GitHub",
    "footer.issues": "Сообщить об ошибке",
    "footer.home": "Главная",
    "footer.license": "Лицензия MIT",
    "footer.unofficial": "Не связано с командой Immich.",
    "dl.title": "Загрузки",
    "dl.latest": "Последняя версия.",
    "dl.needs": "Нужен сервер Immich v3.",
    "dl.yours": "Ваша система",
    "dl.beta": "бета",
    "dl.mac.meta": "macOS 13 и новее · Apple Silicon и Intel",
    "dl.mac.button": "Скачать .dmg",
    "dl.mac.1": "Откройте .dmg и перетащите SD Sync Immich на папку «Программы».",
    "dl.mac.2": "Приложение не подписано Apple Developer ID, поэтому первый запуск macOS заблокирует: откройте <b>Системные настройки → Конфиденциальность и безопасность</b> и нажмите <b>Всё равно открыть</b> у SD Sync Immich.",
    "dl.win.meta": "Windows 10 или 11 · x64",
    "dl.win.button": "Скачать установщик",
    "dl.win.1": "Запустите установщик. Права администратора не нужны.",
    "dl.win.2": "Установщик не подписан, поэтому SmartScreen предупредит о нём: нажмите <b>Подробнее → Выполнить в любом случае</b>.",
    "dl.linux.meta": "x86_64 · glibc 2.35+ (Ubuntu 22.04, Debian 12, Fedora 36 и новее)",
    "dl.linux.button": "Скачать AppImage",
    "dl.linux.1": "Сделайте файл исполняемым и запустите: <code>chmod +x SD-Sync-Immich-x86_64.AppImage</code>",
    "dl.linux.2": "В GNOME для значка в трее нужно расширение AppIndicator (в Ubuntu оно есть).",
    "dl.more": "Контрольные суммы, бинарники для командной строки и старые версии — на <a href=\"https://github.com/brofrong/sd-sync-iimich/releases\">странице релизов GitHub</a>. Подробная инструкция — в <a href=\"https://github.com/brofrong/sd-sync-iimich#readme\">README</a>.",
  };

  // The text the script writes itself, in both languages.
  var TEXT = {
    en: {
      "title.home": "SD Sync Immich: insert the SD card, the photos go to Immich",
      "title.downloads": "Download SD Sync Immich for macOS, Windows and Linux",
      "dl.macos": "Download for macOS",
      "dl.windows": "Download for Windows",
      "dl.linux": "Download for Linux",
      "dl.all": "All downloads",
      "meta.macos": "macOS 13+ · Apple Silicon and Intel",
      "meta.windows": "Windows 10/11 · beta",
      "meta.linux": "x86_64 AppImage · beta",
      "meta.all": "macOS, Windows and Linux",
      "note.others": "Other systems: <a href=\"downloads/\">all downloads</a>. Free and open source.",
      "note.mobile": "SD Sync Immich runs on a computer: open this page there to download it.",
      "note.other": "Free and open source.",
      "release": "Version {v}, {date}.",
      "mb": "{n} MB",
    },
    ru: {
      "title.home": RU["title.home"],
      "title.downloads": RU["title.downloads"],
      "dl.macos": "Скачать для macOS",
      "dl.windows": "Скачать для Windows",
      "dl.linux": "Скачать для Linux",
      "dl.all": "Все загрузки",
      "meta.macos": "macOS 13+ · Apple Silicon и Intel",
      "meta.windows": "Windows 10/11 · бета",
      "meta.linux": "x86_64 AppImage · бета",
      "meta.all": "macOS, Windows и Linux",
      "note.others": "Другие системы — <a href=\"downloads/\">все загрузки</a>. Бесплатно, открытый код.",
      "note.mobile": "SD Sync Immich работает на компьютере: откройте эту страницу на нём, чтобы скачать.",
      "note.other": "Бесплатно, открытый код.",
      "release": "Версия {v}, {date}.",
      "mb": "{n} МБ",
    },
  };

  var query = new URLSearchParams(location.search);
  var lang = pickLanguage();
  var os = detectOS();
  var github = { stars: null, version: null, date: null, sizes: {} };

  // ---- Language -----------------------------------------------------------

  function pickLanguage() {
    var asked = query.get("lang") || stored();
    if (asked === "en" || asked === "ru") return asked;
    var langs = navigator.languages || [navigator.language || "en"];
    for (var i = 0; i < langs.length; i++) {
      var l = String(langs[i]).toLowerCase();
      if (l.indexOf("ru") === 0) return "ru";
      if (l.indexOf("en") === 0) return "en";
    }
    return "en";
  }

  function stored() {
    try { return localStorage.getItem("lang"); } catch (e) { return null; }
  }

  function t(key, vars) {
    var s = TEXT[lang][key];
    for (var k in vars || {}) s = s.replace("{" + k + "}", vars[k]);
    return s;
  }

  function translate() {
    document.documentElement.lang = lang;
    each("[data-i18n]", function (el) {
      if (el.dataset.en === undefined) el.dataset.en = el.innerHTML;
      var ru = RU[el.dataset.i18n];
      el.innerHTML = lang === "ru" && ru ? ru : el.dataset.en;
    });
    each("[data-i18n-alt]", function (el) {
      if (el.dataset.enAlt === undefined) el.dataset.enAlt = el.alt;
      el.alt = lang === "ru" ? RU[el.dataset.i18nAlt] : el.dataset.enAlt;
    });
    each("img[data-src-ru]", function (el) {
      if (el.dataset.srcEn === undefined) el.dataset.srcEn = el.getAttribute("src");
      el.setAttribute("src", lang === "ru" ? el.dataset.srcRu : el.dataset.srcEn);
    });
    each(".lang button", function (b) {
      b.setAttribute("aria-pressed", String(b.dataset.lang === lang));
    });
  }

  // ---- System -------------------------------------------------------------

  // detectOS answers macos, windows, linux (x86_64), mobile or other: the
  // systems with a download, and the rest.
  // ?os=<one of these> overrides it, to check the pages.
  function detectOS() {
    var asked = query.get("os");
    if (asked) return asked;
    var uad = navigator.userAgentData;
    var ua = navigator.userAgent || "";
    var platform = (uad && uad.platform) || navigator.platform || "";
    if ((uad && uad.mobile) || /Android|iPhone|iPad|iPod/i.test(ua)) return "mobile";
    // iPadOS asks for the desktop site and calls itself a Mac.
    if (/Mac/i.test(platform) && navigator.maxTouchPoints > 1) return "mobile";
    if (/Win/i.test(platform) || /Windows/i.test(ua)) return "windows";
    if (/Mac/i.test(platform) || /Mac OS X/i.test(ua)) return "macos";
    if (/CrOS/i.test(ua)) return "other";
    if (/Linux|X11/i.test(platform + " " + ua)) {
      return /aarch64|arm/i.test(platform + " " + ua) ? "other" : "linux";
    }
    return "other";
  }

  // ---- Rendering ----------------------------------------------------------

  function render() {
    translate();
    var home = !!document.getElementById("download");
    document.title = t(home ? "title.home" : "title.downloads");
    if (home) renderHero();
    else renderDownloads();
    renderGitHub();
  }

  function renderHero() {
    var button = document.getElementById("download");
    var title = document.getElementById("download-title");
    var meta = document.getElementById("download-meta");
    var note = document.getElementById("download-note");
    var file = FILES[os];
    var version = github.version ? github.version + " · " : "";
    if (file) {
      button.href = RELEASES + file;
      title.textContent = t("dl." + os);
      meta.textContent = version + t("meta." + os);
      note.innerHTML = t("note.others");
      return;
    }
    button.href = "downloads/";
    title.textContent = t("dl.all");
    meta.textContent = version + t("meta.all");
    note.innerHTML = t(os === "mobile" ? "note.mobile" : "note.other");
  }

  function renderDownloads() {
    var list = document.querySelector(".systems");
    each(".system", function (el) {
      var isMine = el.dataset.os === os;
      el.classList.toggle("mine", isMine);
      el.querySelector(".mine-tag").hidden = !isMine;
      if (isMine && list.firstElementChild !== el) list.insertBefore(el, list.firstElementChild);
    });
    if (github.version) {
      var date = github.date ? github.date.toLocaleDateString(lang, { day: "numeric", month: "long", year: "numeric" }) : "";
      document.getElementById("release-line").textContent = t("release", { v: github.version, date: date });
    }
    each("[data-file]", function (el) {
      var size = github.sizes[el.dataset.file];
      el.textContent = el.dataset.file + (size ? " · " + t("mb", { n: Math.round(size / 1e6) }) : "");
    });
  }

  function renderGitHub() {
    if (github.stars === null) return;
    var n = new Intl.NumberFormat(lang).format(github.stars);
    each("[data-stars]", function (el) {
      el.textContent = n;
      el.hidden = false;
    });
    each("[data-stars-inline]", function (el) {
      el.textContent = "· ★ " + n;
    });
  }

  // ---- GitHub -------------------------------------------------------------

  // The answers are kept for 10 minutes: GitHub allows 60 requests an hour
  // from one address without signing in.
  function fetchJSON(path) {
    var key = "gh:" + path;
    try {
      var hit = JSON.parse(sessionStorage.getItem(key) || "null");
      if (hit && Date.now() - hit.at < 10 * 60 * 1000) return Promise.resolve(hit.data);
    } catch (e) { /* no storage: ask GitHub */ }
    return fetch("https://api.github.com/repos/" + REPO + path, { headers: { Accept: "application/vnd.github+json" } })
      .then(function (r) {
        if (!r.ok) throw new Error("GitHub answered " + r.status);
        return r.json();
      })
      .then(function (data) {
        try { sessionStorage.setItem(key, JSON.stringify({ at: Date.now(), data: data })); } catch (e) { /* fine */ }
        return data;
      });
  }

  function loadGitHub() {
    fetchJSON("").then(function (repo) {
      github.stars = repo.stargazers_count;
      renderGitHub();
    }, noop);
    fetchJSON("/releases/latest").then(function (rel) {
      github.version = rel.tag_name;
      github.date = rel.published_at ? new Date(rel.published_at) : null;
      (rel.assets || []).forEach(function (a) { github.sizes[a.name] = a.size; });
      render();
    }, noop);
  }

  // ---- Start --------------------------------------------------------------

  function each(selector, fn) {
    Array.prototype.forEach.call(document.querySelectorAll(selector), fn);
  }

  function noop() {}

  each(".lang button", function (b) {
    b.addEventListener("click", function () {
      lang = b.dataset.lang;
      try { localStorage.setItem("lang", lang); } catch (e) { /* fine */ }
      render();
    });
  });

  render();
  loadGitHub();
})();
