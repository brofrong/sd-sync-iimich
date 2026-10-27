// A stand-in backend for developing the UI in a plain browser (no Wails
// runtime, development builds only) or with VITE_MOCK=1. It replays the
// mockup's scenario in a loop, syncing → server_unreachable → done, and
// answers every Service method with plausible data.
//
// URL parameters (handy for screenshots):
//   ?state=<code>   pin the status to one code (e.g. done, not_configured,
//                   verifying, format_pending, formatted); waiting_for_card
//                   and other_card_mounted also unplug the selected card, so
//                   Cards shows it as not connected
//   ?format=<outcome>  with ?state=ejected: what became of the card's format
//                   (formatted, skipped_missing, skipped_unsupported)
//   ?lang=<tag>     UI language (default: the browser's language)
//   ?section=<id>   open a section
//   ?update=none    "Check for Updates" finds nothing (default: v0.5.0)
//   ?update=manual  installing it fails with update_manual (Linux outside
//                   an AppImage)
//   ?history=none   no sync runs yet
//   ?gallery=none   Gallery finds no card (as when it is not plugged in);
//                   ?gallery=empty a card without photos, ?gallery=error
//                   a failing Gallery call; ?numbers=off pictures without
//                   their numbers
//   ?expand=<id>    expand the failed files of run <id> in History
//   ?os=<system>    the system AppInfo reports: macos (default), windows or
//                   linux; Windows and Linux are betas (the sidebar badge)
//
// "Retry failed files" works for a run of the selected, mounted card (the
// scenario syncs again) and answers retry_unavailable otherwise, as Go.
// In the server settings a URL containing "bad" is unreachable, the API key
// "bad" is rejected, the key "readonly" lacks asset.upload, and a URL
// containing "readonly" fails to save.
import {
  AppInfoView,
  CardView,
  NotificationsView,
  ServerCheckView,
  ServerView,
  SettingsView,
  StateView,
  UpdateView,
} from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/gui";
import { Progress, Status } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/app";
import type { Action, Code } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/app";
import { FileFailure, SyncRun } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/state";
import { List, Photo } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/gallery";
import type { Backend, EventMap, Transport } from "./backend";
import { resolveLanguage } from "./i18n";

const SERVER_URL = "https://immich.brofrong.ru";
const HOST = "immich.brofrong.ru";
// As Go's repoURL and newIssueURL.
const REPO_URL = "https://github.com/brofrong/sd-sync-iimich";
const NEW_ISSUE_URL = REPO_URL + "/issues/new";

const systems: Record<string, { os: string; osVersion: string }> = {
  macos: { os: "macOS", osVersion: "15.1.1" },
  windows: { os: "Windows", osVersion: "11 (10.0.22631)" },
  linux: { os: "Linux", osVersion: "Ubuntu 24.04.1 LTS" },
};

/** As Go's appInfo and issueURL. */
function appInfo(system: string, version: string): AppInfoView {
  const { os, osVersion } = systems[system] ?? systems.macos;
  const name = `${os} ${osVersion}`.trim();
  const body = `**Version:** ${version}\n**OS:** ${name}\n\n` + "**What happened:**\n\n**Steps to reproduce:**\n";
  return new AppInfoView({
    version,
    os,
    osVersion,
    beta: os !== "macOS",
    repoURL: REPO_URL,
    issueURL: `${NEW_ISSUE_URL}?${new URLSearchParams({ body })}`,
  });
}

const mainCard = new CardView({
  uuid: "5F2C81AB-1D3E-4C2A-9B7F-0E6D2A1C9F11",
  name: "Brofrong",
  mountPoint: "/Volumes/Brofrong",
  mounted: true,
  selected: true,
  sizeBytes: 256e9,
});

const otherCard = new CardView({
  uuid: "7E1A22C4-6B0D-4F8E-A3C5-91D2B4E6F702",
  name: "EOS_DIGITAL",
  mountPoint: "/Volumes/EOS_DIGITAL",
  mounted: true,
  selected: false,
  sizeBytes: 64e9,
});

const spareCard = new CardView({
  uuid: "C3D9F0E2-8A41-4B67-9E15-3F7A2C6D8B40",
  name: "NO NAME",
  mountPoint: "/Volumes/NO NAME",
  mounted: true,
  selected: false,
  sizeBytes: 32e9,
});

// The same action sets as Go's actionsFor.
const actions: Record<string, string[]> = {
  not_configured: ["open_server"],
  no_card_selected: ["open_cards"],
  other_card_mounted: ["select_card"],
  done: ["eject", "open_immich"],
  done_with_errors: ["open_history"],
  ejected: ["open_immich"],
  server_unreachable: ["retry"],
  server_error: ["retry"],
  auth_failed: ["open_server"],
  failed: ["open_logs"],
  format_failed: ["open_logs"],
  format_needs_admin: ["open_logs"],
  syncing: ["stop"],
  verifying: ["stop"],
  format_pending: ["cancel_format"],
};

const params: Record<string, Record<string, string>> = {
  waiting_for_card: { name: mainCard.name },
  other_card_mounted: {
    selected_name: mainCard.name,
    mounted_name: otherCard.name,
    mounted_uuid: otherCard.uuid,
  },
  done: { uploaded: "2114" },
  done_with_errors: { uploaded: "2101", errors: "13" },
  server_unreachable: { host: HOST, detail: "no such host" },
  server_error: { status: "502", detail: "Bad Gateway" },
  card_removed: { uploaded: "412" },
  cancelled: { uploaded: "412" },
  failed: { detail: "open /Volumes/Brofrong/DCIM: permission denied." },
  ejected: { uploaded: "2114" },
  format_pending: { seconds: "27", name: mainCard.name },
  formatting: { name: mainCard.name },
  formatted: { name: mainCard.name },
  format_skipped_missing: { count: "3" },
  format_skipped_unsupported: { files: "12 × .HIF" },
  format_failed: { detail: "diskutil: Could not unmount disk (-69888)" },
  format_needs_admin: { name: mainCard.name },
};

// As Go's ejectedParams: the format outcome and its param.
const formatOutcomes: Record<string, Record<string, string>> = {
  formatted: { format: "formatted", name: mainCard.name },
  skipped_missing: { format: "skipped_missing", count: "3" },
  skipped_unsupported: { format: "skipped_unsupported", files: "12 × .HIF" },
  needs_admin: { format: "needs_admin", name: mainCard.name },
};

function makeStatus(code: string, progress: Progress | null = null, p?: Record<string, string>): Status {
  return new Status({
    code: code as Code,
    params: p ?? params[code] ?? {},
    progress,
    actions: (actions[code] ?? []) as Action[],
    at: new Date().toISOString(),
  });
}

// ---- Scenario --------------------------------------------------------------

const TICK = 500; // ms
const FILES = 2114;
const BYTES = 31e9;

interface Phase {
  code: string;
  ms: number;
}

const cycle: Phase[] = [
  { code: "syncing", ms: 6500 },
  { code: "server_unreachable", ms: 4200 },
  { code: "done", ms: 4200 },
];

// ---- Gallery ----------------------------------------------------------------
// The card's JPEGs, numbered k from the oldest: PHOTOS of them taken over
// five days (the rest of the FILES are RAW and video). The sync uploads
// them oldest first, so a photo is in Immich once the sync has passed it.

const PHOTOS = 2000;
const IN_FLIGHT = 4; // files uploading at once
// [days ago, photos], oldest day first.
const shootingDays: [number, number][] = [
  [9, 514],
  [5, 640],
  [3, 586],
  [2, 212],
  [0, 48],
];

const photoName = (k: number) => `DSC${String(5000 + k).padStart(5, "0")}.JPG`;
const photoPath = (k: number) => `DCIM/100MSDCF/${photoName(k)}`;

/** The photos the sync is uploading once it is fraction done. */
function inFlightAt(fraction: number): string[] {
  const first = Math.round(PHOTOS * fraction);
  const out: string[] = [];
  for (let k = first; k < Math.min(PHOTOS, first + IN_FLIGHT); k++) out.push(photoPath(k));
  return out;
}

// ?numbers=off drops the numbers from the pictures, for screenshots.
const numbered = new URLSearchParams(location.search).get("numbers") !== "off";

/** A stand-in picture: a gradient landscape with the photo's number. */
function picture(k: number, width: number): string {
  const hue = (k * 47) % 360;
  const svg =
    `<svg xmlns='http://www.w3.org/2000/svg' width='${width}' height='${(width * 2) / 3}' viewBox='0 0 300 200'>` +
    `<defs><linearGradient id='g' x1='0' y1='0' x2='1' y2='1'>` +
    `<stop offset='0' stop-color='hsl(${hue},70%,58%)'/><stop offset='1' stop-color='hsl(${(hue + 70) % 360},60%,22%)'/>` +
    `</linearGradient></defs><rect width='300' height='200' fill='url(#g)'/>` +
    `<circle cx='228' cy='58' r='22' fill='rgba(255,255,255,.3)'/>` +
    `<path d='M0 200L78 122l58 44 72-68 92 78v24z' fill='rgba(0,0,0,.28)'/>` +
    (numbered
      ? `<text x='150' y='116' font-family='-apple-system,Helvetica,sans-serif' font-size='44' font-weight='700' ` +
        `fill='rgba(255,255,255,.9)' text-anchor='middle'>${k + 1}</text>`
      : "") +
    `</svg>`;
  return "data:image/svg+xml;utf8," + encodeURIComponent(svg);
}

/** samplePhotos are the card's photos, oldest first, without statuses. */
function samplePhotos(): Photo[] {
  const now = Date.now();
  const out: Photo[] = [];
  for (const [daysAgo, count] of shootingDays) {
    const day = new Date(now - daysAgo * 86400e3);
    // A day's shooting from 9:00, spread over ten hours; today's ends
    // a few minutes ago.
    let start = new Date(day.getFullYear(), day.getMonth(), day.getDate(), 9).getTime();
    let step = (10 * 3600e3) / count;
    if (daysAgo === 0) {
      step = 20e3;
      start = now - 5 * 60e3 - count * step;
    }
    for (let i = 0; i < count; i++) {
      const k = out.length;
      out.push(
        new Photo({
          path: photoPath(k),
          name: photoName(k),
          takenAt: new Date(start + i * step).toISOString(),
          size: 4.2e6 + ((k * 7919) % 8000) * 1000,
          status: "waiting",
          thumb: picture(k, 300),
          src: picture(k, 1500),
        }),
      );
    }
  }
  return out;
}

function syncProgress(fraction: number, tick: number): Progress {
  const current = Math.round(FILES * fraction);
  const bytesDone = BYTES * fraction;
  const bytesPerSec = 48e6 + Math.sin(tick / 2) * 6e6;
  return new Progress({
    current,
    total: FILES,
    bytesDone,
    bytesTotal: BYTES,
    bytesPerSec,
    etaSeconds: Math.round((BYTES - bytesDone) / bytesPerSec),
    file: `DSC0${7133 + current - 412}.ARW`,
    // Most files are new, about one in five Immich already has, and one
    // failed once the sync was a third of the way through.
    duplicates: Math.round(current * 0.2),
    errors: fraction > 0.33 ? 1 : 0,
    uploaded: current - Math.round(current * 0.2) - (fraction > 0.33 ? 1 : 0),
    inFlight: inFlightAt(fraction),
  });
}

function sampleHistory(): SyncRun[] {
  const now = Date.now();
  let id = 100;
  const run = (
    hoursAgo: number,
    minutes: number | null,
    card: CardView,
    code: string,
    p: Record<string, string>,
    counts: {
      uploaded: number;
      duplicates?: number;
      skipped?: number;
      errors?: number;
      summary?: Record<string, number>;
      recorded?: boolean;
    },
  ) => {
    const started = new Date(now - hoursAgo * 3600e3);
    return new SyncRun({
      id: id--,
      startedAt: started.toISOString(),
      finishedAt: minutes === null ? null : new Date(started.getTime() + minutes * 60e3).toISOString(),
      volumeUUID: card.uuid,
      volumeName: card.name,
      resultCode: minutes === null ? "" : code,
      resultParams: p,
      uploaded: counts.uploaded,
      skipped: counts.skipped ?? 0,
      errors: counts.errors ?? 0,
      duplicates: counts.duplicates ?? 0,
      bytes: counts.uploaded * 14.6e6,
      errorSummary: counts.summary ?? {},
      // As Go: runs since the failure list exists have it, even when empty.
      failuresRecorded: counts.recorded ?? minutes !== null,
    });
  };
  return [
    run(3, 108, mainCard, "done_with_errors", { uploaded: "2101", errors: "3" }, {
      uploaded: 2101,
      duplicates: 37,
      skipped: 3,
      errors: 3,
      summary: { network: 2, rejected: 1 },
    }),
    run(5, 2, mainCard, "nothing_new", {}, { uploaded: 0, skipped: 2117 }),
    run(27, 12, mainCard, "server_unreachable", params.server_unreachable, { uploaded: 412 }),
    run(30, 41, otherCard, "done_with_errors", { uploaded: "806", errors: "6" }, {
      uploaded: 806,
      skipped: 14,
      errors: 6,
      summary: { network: 4, rejected: 2 },
    }),
    run(40, 33, mainCard, "done_with_errors", { uploaded: "512", errors: "2" }, {
      uploaded: 512,
      errors: 2,
      summary: { read_failed: 2 },
      recorded: false, // synced before the app kept the list
    }),
    run(52, 7, mainCard, "cancelled", { uploaded: "96" }, { uploaded: 96 }),
    run(98, null, mainCard, "", {}, { uploaded: 0 }),
    run(170, 18, mainCard, "card_removed", { uploaded: "300" }, { uploaded: 300, skipped: 1 }),
    run(7400, 64, otherCard, "done", { uploaded: "1290" }, { uploaded: 1290 }),
  ];
}

/** sampleFailures are the failed files of the sample runs, by run id. */
function sampleFailures(runs: SyncRun[]): Map<number, FileFailure[]> {
  const out = new Map<number, FileFailure[]>();
  const f = (relativePath: string, kind: string, detail: string) => new FileFailure({ relativePath, kind, detail });
  for (const r of runs) {
    if (!r.failuresRecorded || r.errors === 0) continue;
    if (r.volumeUUID === mainCard.uuid) {
      out.set(r.id, [
        f("DCIM/100MSDCF/DSC07311.ARW", "network", "Post \"https://immich.brofrong.ru/api/assets\": read tcp: connection reset by peer"),
        f("DCIM/100MSDCF/DSC07342.ARW", "network", "Post \"https://immich.brofrong.ru/api/assets\": context deadline exceeded"),
        f("PRIVATE/M4ROOT/CLIP/C0042.MP4", "rejected", "server returned 400: Unsupported file type"),
      ]);
    } else {
      out.set(r.id, [
        ...[4012, 4013, 4030, 4031].map((n) => f(`DCIM/100CANON/IMG_${n}.CR3`, "network", "dial tcp: lookup immich.brofrong.ru: no such host")),
        f("DCIM/100CANON/IMG_4100.CR3", "rejected", "server returned 400: Unsupported file type"),
        f("DCIM/100CANON/MVI_4101.MP4", "rejected", "server returned 413: Request Entity Too Large"),
      ]);
    }
  }
  return out;
}

/** sampleLog imitates app.log (slog's text format) with a few warnings. */
function sampleLog(lines: number): string {
  const out: string[] = [];
  let at = Date.now() - lines * 1900;
  const src = "source=/Users/runner/work/sd-sync-immich/internal/app/app.go:";
  const line = (level: string, msg: string, attrs = "") => {
    at += 1200 + Math.round(Math.random() * 1400);
    const ts = new Date(at).toISOString().replace("Z", "+00:00");
    out.push(`time=${ts} level=${level} ${src}${200 + (out.length % 180)} msg="${msg}"${attrs ? " " + attrs : ""}`);
  };
  line("INFO", "app run loop started", `version=0.4.0`);
  line("INFO", "volume mounted", `name=${mainCard.name} uuid=${mainCard.uuid} mount=${mainCard.mountPoint}`);
  line("INFO", "scan started", `paths="[DCIM PRIVATE/M4ROOT/CLIP]"`);
  line("DEBUG", "scan finished", `files=${FILES} new=${FILES - 3} skipped=3 took=1.84s`);
  line("INFO", "sync started", `files=${FILES - 3} bytes=${BYTES} concurrency=4`);
  for (let i = 0; out.length < lines - 2; i++) {
    const file = `DSC0${7100 + i}.ARW`;
    if (i % 23 === 17) {
      line("WARN", "upload failed, retrying", `file=${file} attempt=1 err="Post \"${SERVER_URL}/api/assets\": dial tcp: lookup ${HOST}: no such host"`);
    } else if (i % 61 === 40) {
      line("ERROR", "upload failed", `file=${file} kind=rejected err="server returned 400: Unsupported file type"`);
    } else {
      line("INFO", "uploaded", `file=${file} bytes=${14_600_000 + i * 1013} duplicate=false`);
    }
  }
  line("INFO", "sync finished", `uploaded=${FILES} skipped=3 errors=0 took=1h48m12s`);
  line("INFO", "status changed", "code=done");
  return out.slice(-lines).join("\n");
}

const sampleNotes = `What's new
• A real window: status, history, cards, server, logs and settings
• Sync resumes by itself when Immich is back online
• Eject the card right after a sync

Fixes
• The API key no longer keeps a trailing newline when pasted`;

/** createMockTransport builds the mock backend and starts its scenario. */
export function createMockTransport(): Transport {
  const query = new URLSearchParams(location.search);
  const pinned = query.get("state");

  const listeners: { [E in keyof EventMap]: Set<(d: EventMap[E]) => void> } = {
    status: new Set(),
    state: new Set(),
    navigate: new Set(),
  };
  function emit<E extends keyof EventMap>(name: E, data: EventMap[E]): void {
    listeners[name].forEach((cb) => cb(data));
  }

  // ?bundle=none mimics a development build, where autostart is
  // unavailable; ?bundle=translocated an app opened from a quarantined
  // download in place. ?format_admin=needed mimics Windows without
  // administrator rights, where formatting never happens.
  const translocated = query.get("bundle") === "translocated";
  const inBundle = query.get("bundle") !== "none" && !translocated;
  const settings = new SettingsView({
    language: query.get("lang") ?? "",
    concurrency: 0,
    autoPaths: true,
    autostart: inBundle,
    autostartAvailable: inBundle,
    autostartTranslocated: translocated,
    autoEject: true,
    autoFormat: query.get("format_setting") === "on",
    autoFormatNeedsAdmin: query.get("format_admin") === "needed",
    notifications: new NotificationsView({ enabled: true, done: true, errors: true, otherCard: true, server: true, card: true }),
  });
  const state = new StateView({
    status: makeStatus(pinned ?? "syncing"),
    language: resolveLanguage(settings.language, navigator.languages),
    server: new ServerView({ url: SERVER_URL, hasKey: true }),
    card: new CardView(mainCard),
    settings,
    version: "0.4.0",
    update: null,
    updateCheckedAt: null,
  });
  // Pinned onboarding and card codes need a matching configuration.
  // The mounted removable volumes; the selected card is state.card.
  let mounted = [new CardView(mainCard), new CardView(otherCard), new CardView(spareCard)];
  const unplugged = (c: CardView) => new CardView({ ...c, mounted: false, mountPoint: "", sizeBytes: 0 });
  // The cards with sync state (as Go's state.RememberedCards): the main card,
  // an old copy of it from before it was formatted, and one never named.
  let remembered = [
    { uuid: mainCard.uuid, name: mainCard.name, uploaded: 13821 },
    { uuid: "3A8C3713-C262-3DB7-8BEC-604865B64393", name: "Brofrong", uploaded: 18900 },
    { uuid: "9B04E7A1-52D3-4E6F-8C21-7A0F3D5B6E19", name: "", uploaded: 412 },
  ];

  if (pinned === "not_configured") {
    state.server = new ServerView();
    state.card = new CardView();
  } else if (pinned === "no_card_selected") {
    state.card = new CardView();
  } else if (pinned === "waiting_for_card" || pinned === "other_card_mounted") {
    mounted = mounted.filter((c) => c.uuid !== mainCard.uuid);
    state.card = unplugged(mainCard);
  }
  if (pinned === "syncing") state.status = makeStatus("syncing", syncProgress(0.38, 0));
  // The pinned sync stands at 38 %; otherwise the scenario moves it.
  let fraction = pinned === "syncing" ? 0.38 : 0.2;
  if (pinned === "scanning") state.status = makeStatus("scanning");
  if (pinned === "verifying") state.status = makeStatus("verifying", new Progress({ current: 1388, total: 2114 }));
  if (pinned === "ejected") {
    const outcome = formatOutcomes[query.get("format") ?? ""];
    state.status = makeStatus("ejected", null, { ...params.ejected, ...outcome });
  }
  if (pinned === "ejected" || pinned === "formatted") {
    // The ejected card is no longer mounted.
    mounted = mounted.filter((c) => c.uuid !== mainCard.uuid);
    state.card = unplugged(mainCard);
  }

  let history = query.get("history") === "none" ? [] : sampleHistory();
  const failures = sampleFailures(history);

  function setStatus(s: Status): void {
    state.status = s;
    emit("status", s);
  }
  function pushState(): void {
    emit("state", new StateView({ ...state, status: state.status }));
  }
  // As Go: a run is recorded when a sync starts and finished when it ends.
  let current: SyncRun | null = null;
  function startRun(): void {
    current = new SyncRun({
      id: Math.max(0, ...history.map((r) => r.id)) + 1,
      startedAt: new Date().toISOString(),
      volumeUUID: state.card.uuid,
      volumeName: state.card.name,
    });
    history = [current, ...history].slice(0, 50);
  }
  function record(code: string, p: Record<string, string>, uploaded: number): void {
    if (!current) return;
    Object.assign(current, {
      // The scenario is compressed; pretend the sync took a while.
      startedAt: new Date(Date.now() - 14 * 60e3).toISOString(),
      finishedAt: new Date().toISOString(),
      resultCode: code,
      resultParams: p,
      uploaded,
      skipped: 3,
      bytes: uploaded * 14.6e6,
    });
    current = null;
  }

  // The scenario advances only while the page is visible: no timers run
  // while the window is hidden.
  let phase = 0;
  let elapsed = 0;
  let tick = 0;
  let hold = 0; // ms left on an interrupting status (stop, eject)
  let timer: ReturnType<typeof setInterval> | undefined;

  function enter(i: number): void {
    phase = i;
    elapsed = 0;
    const { code } = cycle[i];
    if (code === "syncing") {
      fraction = 0.2;
      startRun();
      setStatus(makeStatus("syncing", syncProgress(fraction, tick)));
    } else if (code === "server_unreachable") {
      record(code, params[code], Math.round(FILES * fraction) - 412);
      setStatus(makeStatus(code, null, { ...params[code] }));
    } else {
      // The automatic resume after the outage: a second run that completes.
      if (!current) startRun();
      record(code, params.done, FILES);
      setStatus(makeStatus(code));
    }
  }

  function step(): void {
    tick++;
    if (hold > 0) {
      hold -= TICK;
      if (hold <= 0) enter(0);
      return;
    }
    elapsed += TICK;
    if (elapsed >= cycle[phase].ms) {
      enter((phase + 1) % cycle.length);
      return;
    }
    if (cycle[phase].code === "syncing") {
      fraction = Math.min(0.94, fraction + 0.035 + Math.random() * 0.02);
      setStatus(makeStatus("syncing", syncProgress(fraction, tick)));
    }
  }

  function run(): void {
    clearInterval(timer);
    timer = undefined;
    if (!pinned && !document.hidden && state.card.uuid) timer = setInterval(step, TICK);
  }
  document.addEventListener("visibilitychange", run);
  if (!pinned) {
    enter(0);
    run();
  }

  function interrupt(s: Status, ms = 4000): void {
    if (current) record(s.code, s.params as Record<string, string>, Number(s.params?.uploaded ?? 0));
    setStatus(s);
    hold = pinned ? 0 : ms;
  }

  const delay = <T>(ms: number, v: T) => new Promise<T>((r) => setTimeout(() => r(v), ms));

  // As Go's Gallery: the selected card's photos while it is mounted, newest
  // first, with the statuses of the sync so far.
  const photos = samplePhotos();
  const galleryMode = query.get("gallery");
  const finished = ["done", "done_with_errors", "nothing_new", "format_pending", "formatting", "formatted"];
  function gallery(): List | null {
    if (galleryMode === "none" || !mounted.some((c) => c.uuid === state.card.uuid)) return null;
    if (galleryMode === "empty") return new List({ cardUuid: state.card.uuid, cardName: state.card.name, photos: [], other: 114 });
    const uploaded = finished.includes(state.status.code) ? PHOTOS : Math.round(PHOTOS * fraction);
    const list = photos.map((p, k) => {
      // A few files failed in the last sync.
      const status = k >= uploaded ? "waiting" : k % 331 === 170 ? "failed" : "in_immich";
      return new Photo({ ...p, status });
    });
    return new List({ cardUuid: state.card.uuid, cardName: state.card.name, photos: list.reverse(), other: FILES - PHOTOS });
  }

  let fullscreen = false;

  // As Go's normalizeServer.
  const normalizeUrl = (u: string) => u.trim().replace(/\/+$/, "").replace(/\/api$/, "").replace(/\/+$/, "");
  const validUrl = (u: string) => {
    try {
      const parsed = new URL(normalizeUrl(u));
      return (parsed.protocol === "http:" || parsed.protocol === "https:") && parsed.host !== "";
    } catch {
      return false;
    }
  };
  const hostOf = (u: string) => {
    try {
      return new URL(normalizeUrl(u)).host.toLowerCase();
    } catch {
      return u;
    }
  };

  const backend: Backend = {
    State: async () => new StateView({ ...state }),
    // As Go's listCards: the selected card, the mounted ones, then the
    // remembered ones that are not mounted.
    Cards: async () => {
      const list: CardView[] = [];
      const listed = (uuid: string) => list.some((c) => c.uuid === uuid);
      const remember = (c: CardView) => {
        const r = remembered.find((r) => r.uuid === c.uuid);
        return r ? new CardView({ ...c, name: c.name || r.name, remembered: true, uploaded: r.uploaded }) : c;
      };
      if (state.card.uuid) {
        const m = mounted.find((c) => c.uuid === state.card.uuid);
        list.push(remember(new CardView({ ...(m ?? state.card), selected: true })));
      }
      for (const c of mounted) if (!listed(c.uuid)) list.push(remember(new CardView({ ...c, selected: false })));
      for (const r of remembered) if (!listed(r.uuid)) list.push(new CardView({ uuid: r.uuid, name: r.name, remembered: true, uploaded: r.uploaded }));
      return delay(120, list);
    },
    ForgetCard: async (uuid) => {
      const selected = uuid === state.card.uuid;
      if (selected && (state.status.code === "syncing" || state.status.code === "scanning")) {
        throw new Error("the card is syncing");
      }
      await delay(300, null);
      remembered = remembered.filter((r) => r.uuid !== uuid);
      if (selected) {
        state.card = new CardView();
        run(); // stops the scenario: there is no card to sync
        hold = 0;
        setStatus(makeStatus("no_card_selected"));
      }
      pushState();
    },
    SelectCard: async (uuid) => {
      const card = mounted.find((c) => c.uuid === uuid);
      if (!card) throw new Error(`card ${uuid} is not mounted`);
      state.card = new CardView({ ...card, selected: true });
      pushState();
      if (!pinned) {
        enter(0);
        run();
      }
    },
    EjectCard: async () => {
      const card = mounted.find((c) => c.uuid === state.card.uuid);
      if (!card) throw new Error("the selected card is not mounted");
      await delay(400, null);
      mounted = mounted.filter((c) => c !== card);
      state.card = unplugged(card);
      pushState();
      interrupt(makeStatus("waiting_for_card", null, { name: state.card.name }), 5000);
      // Plug it back in a moment later.
      setTimeout(() => {
        mounted = [card, ...mounted];
        if (state.card.uuid === card.uuid) state.card = new CardView(card);
        pushState();
      }, 5000);
    },
    TestServer: async (url, apiKey) => {
      if (!validUrl(url)) {
        return delay(80, new ServerCheckView({ ok: false, code: "invalid_url", params: {} }));
      }
      // As in Go, the saved key is only sent to the saved server's host.
      if (!apiKey.trim() && state.server.hasKey && hostOf(url) !== hostOf(state.server.url)) {
        return delay(80, new ServerCheckView({ ok: false, code: "key_required", params: {} }));
      }
      if (!apiKey.trim() && !state.server.hasKey) {
        return delay(80, new ServerCheckView({ ok: false, code: "key_missing", params: {} }));
      }
      if (url.includes("bad")) {
        return delay(600, new ServerCheckView({ ok: false, code: "server_unreachable", params: { host: url, detail: "no such host" } }));
      }
      if (apiKey === "bad") {
        return delay(300, new ServerCheckView({ ok: false, code: "auth_failed", params: { detail: "401 Unauthorized: Invalid API key" } }));
      }
      if (apiKey === "readonly") {
        return delay(300, new ServerCheckView({
          ok: false, code: "key_forbidden", params: { detail: "403 Forbidden: Missing required permission: asset.upload" },
        }));
      }
      return delay(220, new ServerCheckView({ ok: true, version: "v1.122.3", code: "", params: {} }));
    },
    SaveServer: async (url, apiKey) => {
      if (!validUrl(url)) throw new Error("server URL must start with http:// or https://");
      await delay(200, null);
      if (!apiKey.trim() && state.server.hasKey && hostOf(url) !== hostOf(state.server.url)) {
        throw new Error("key_required");
      }
      if (!apiKey.trim() && !state.server.hasKey) throw new Error("key_missing");
      if (url.includes("readonly")) {
        throw new Error("save server settings: open ~/.config/sd-sync-immich/config.yaml: permission denied");
      }
      state.server = new ServerView({ url: normalizeUrl(url), hasKey: state.server.hasKey || apiKey.trim() !== "" });
      pushState();
    },
    SaveSettings: async (v) => {
      if (v.concurrency < 0 || v.concurrency > 16) throw new Error("concurrency must be 0 (auto) or between 1 and 16");
      // As in Go, the autostart fields are not saved here.
      state.settings = new SettingsView({
        ...v,
        autostart: state.settings.autostart,
        autostartAvailable: state.settings.autostartAvailable,
        autostartTranslocated: state.settings.autostartTranslocated,
        autoFormatNeedsAdmin: state.settings.autoFormatNeedsAdmin,
      });
      state.language = resolveLanguage(v.language, navigator.languages);
      pushState();
    },
    SetAutostart: async (enabled) => {
      if (enabled && state.settings.autostartTranslocated) throw new Error("autostart_translocated");
      if (!state.settings.autostartAvailable) {
        throw new Error("starting at login is not available for this copy of the app");
      }
      await delay(150, null);
      state.settings = new SettingsView({ ...state.settings, autostart: enabled });
      pushState();
    },
    SyncNow: async () => {
      hold = 0;
      enter(0);
    },
    StopSync: async () => {
      interrupt(makeStatus("cancelled", null, { uploaded: String(Math.round(FILES * fraction) - 412) }));
    },
    Retry: async () => {
      hold = 0;
      enter(0);
    },
    History: async (limit) => history.slice(0, limit),
    RunFailures: async (runID) => delay(150, failures.get(runID) ?? []),
    // As Go's RetryFailures: only the selected, mounted card's run, with a
    // failure list, while no sync runs.
    RetryFailures: async (runID) => {
      const r = history.find((h) => h.id === runID);
      const busy = ["scanning", "syncing", "verifying", "format_pending", "formatting"].includes(state.status.code);
      const here = mounted.some((c) => c.uuid === state.card.uuid);
      if (!r || !r.failuresRecorded || !failures.get(runID)?.length || r.volumeUUID !== state.card.uuid || !here || busy) {
        await delay(200, null);
        throw new Error("retry_unavailable");
      }
      await delay(200, null);
      hold = 0;
      if (pinned) setStatus(makeStatus("syncing", syncProgress(0.1, 0)));
      else enter(0);
    },
    // As Go's FormatCard: refused while busy; verifies, then erases.
    FormatCard: async () => {
      const busy = ["scanning", "syncing", "verifying", "format_pending", "formatting"].includes(state.status.code);
      if (busy) throw new Error("the card is syncing");
      if (!mounted.some((c) => c.uuid === state.card.uuid)) throw new Error("selected card is not mounted");
      await delay(150, null);
      const name = state.card.name;
      interrupt(makeStatus("verifying", new Progress({ current: 0, total: 2114 })), 8000);
      setTimeout(() => setStatus(makeStatus("formatting", null, { name })), 2500);
      setTimeout(() => setStatus(makeStatus("formatted", null, { name })), 4500);
    },
    CancelFormat: async () => {
      if (state.status.code !== "format_pending") return;
      // The sync's result status is shown again.
      setStatus(makeStatus("done"));
    },
    LogTail: async (lines) => delay(150, sampleLog(lines)),
    RevealLog: async () => console.info("[mock] RevealLog"),
    OpenImmich: async () => console.info("[mock] OpenImmich", SERVER_URL),
    AppInfo: async () => appInfo((query.get("os") ?? "macos").toLowerCase(), state.version),
    // Only the repository and a new issue, like Go (whose check is
    // stricter: a new issue may only carry a canonically encoded body).
    OpenURL: async (u) => {
      const q = u.indexOf("?");
      const page = q < 0 ? u : u.slice(0, q);
      const query = q < 0 ? "" : u.slice(q + 1);
      const ok = (page === REPO_URL && q < 0) || (page === NEW_ISSUE_URL && (q < 0 || query.startsWith("body=")));
      if (!ok || u.includes("#")) throw new Error("only the project's GitHub pages can be opened");
      console.info("[mock] OpenURL", u);
    },
    Quit: async () => console.info("[mock] Quit"),
    CheckForUpdates: async () => {
      await delay(900, null);
      state.updateCheckedAt = new Date().toISOString();
      if (query.get("update") === "none") {
        pushState();
        return null;
      }
      const u = new UpdateView({
        version: "v0.5.0",
        notes: sampleNotes,
        url: "https://github.com/dmitiigorshkov/sd-sync-immich/releases/tag/v0.5.0",
      });
      state.update = u;
      pushState();
      return u;
    },
    InstallUpdate: async () => {
      await delay(1500, null);
      if (query.get("update") === "manual") throw new Error("update_manual");
    },
    Gallery: async () => {
      if (galleryMode === "error") {
        await delay(300, null);
        throw new Error("read /Volumes/Brofrong/DCIM: input/output error");
      }
      return delay(250, gallery());
    },
    SetFullscreen: async (on) => {
      fullscreen = on;
    },
    IsFullscreen: async () => fullscreen,
  };

  const section = query.get("section");
  if (section) setTimeout(() => emit("navigate", section), 0);
  const expand = query.get("expand");
  if (expand) {
    // Click the disclosure of that run once History has rendered it.
    const click = (tries: number) => {
      const b = document.querySelector<HTMLButtonElement>(`button[aria-controls="failures-${expand}"]`);
      if (b) b.click();
      else if (tries > 0) setTimeout(() => click(tries - 1), 100);
    };
    setTimeout(() => click(30), 300);
  }

  return {
    backend,
    on(name, cb) {
      const set = listeners[name] as Set<typeof cb>;
      set.add(cb);
      return () => set.delete(cb);
    },
  };
}
