// A stand-in backend for developing the UI in a plain browser (no Wails
// runtime) or with VITE_MOCK=1. It replays the mockup's scenario in a loop,
// syncing → server_unreachable → done, and answers every Service method
// with plausible data.
//
// URL parameters (handy for screenshots):
//   ?state=<code>   pin the status to one code (e.g. done, not_configured);
//                   waiting_for_card and other_card_mounted also unplug the
//                   selected card, so Cards shows it as not connected
//   ?lang=<tag>     UI language (default: the browser's language)
//   ?section=<id>   open a section
//   ?update=none    "Check for Updates" finds nothing (default: v0.5.0)
//   ?history=none   no sync runs yet
//
// In the Server section a URL containing "bad" is unreachable, the API key
// "bad" is rejected, and a URL containing "readonly" fails to save.
import {
  CardView,
  ServerCheckView,
  ServerView,
  SettingsView,
  StateView,
  UpdateView,
} from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/gui";
import { Progress, Status } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/app";
import type { Action, Code } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/app";
import { SyncRun } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/state";
import type { Backend, EventMap, Transport } from "./backend";
import { resolveLanguage } from "./i18n";

const SERVER_URL = "https://immich.brofrong.ru";
const HOST = "immich.brofrong.ru";

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
  done_with_errors: ["open_logs"],
  server_unreachable: ["retry"],
  server_error: ["retry"],
  auth_failed: ["open_server"],
  failed: ["open_logs"],
  syncing: ["stop"],
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
    counts: { uploaded: number; skipped?: number; errors?: number; summary?: Record<string, number> },
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
      bytes: counts.uploaded * 14.6e6,
      errorSummary: counts.summary ?? {},
    });
  };
  return [
    run(3, 108, mainCard, "done", { uploaded: "2114" }, { uploaded: 2114, skipped: 3 }),
    run(5, 2, mainCard, "nothing_new", {}, { uploaded: 0, skipped: 2117 }),
    run(27, 12, mainCard, "server_unreachable", params.server_unreachable, { uploaded: 412 }),
    run(30, 41, otherCard, "done_with_errors", { uploaded: "806", errors: "6" }, {
      uploaded: 806,
      skipped: 14,
      errors: 6,
      summary: { network: 4, rejected: 2 },
    }),
    run(52, 7, mainCard, "cancelled", { uploaded: "96" }, { uploaded: 96 }),
    run(98, null, mainCard, "", {}, { uploaded: 0 }),
    run(170, 18, mainCard, "card_removed", { uploaded: "300" }, { uploaded: 300, skipped: 1 }),
    run(7400, 64, otherCard, "done", { uploaded: "1290" }, { uploaded: 1290 }),
  ];
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
  // download in place.
  const translocated = query.get("bundle") === "translocated";
  const inBundle = query.get("bundle") !== "none" && !translocated;
  const settings = new SettingsView({
    language: query.get("lang") ?? "",
    concurrency: 4,
    autoPaths: true,
    autostart: inBundle,
    autostartAvailable: inBundle,
    autostartTranslocated: translocated,
  });
  const state = new StateView({
    status: makeStatus(pinned ?? "syncing"),
    language: resolveLanguage(settings.language, navigator.languages),
    server: new ServerView({ url: SERVER_URL, hasKey: true }),
    card: new CardView(mainCard),
    settings,
    version: "0.4.0",
    update: null,
  });
  // Pinned onboarding and card codes need a matching configuration.
  // The mounted removable volumes; the selected card is state.card.
  let mounted = [new CardView(mainCard), new CardView(otherCard), new CardView(spareCard)];
  const unplugged = (c: CardView) => new CardView({ ...c, mounted: false, mountPoint: "", sizeBytes: 0 });

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
  if (pinned === "scanning") state.status = makeStatus("scanning");

  let history = query.get("history") === "none" ? [] : sampleHistory();

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
  let fraction = 0.2;
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
    if (!pinned && !document.hidden) timer = setInterval(step, TICK);
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
    Cards: async () => {
      const list = mounted.map((c) => new CardView({ ...c, selected: c.uuid === state.card.uuid }));
      const i = list.findIndex((c) => c.selected);
      if (i > 0) list.unshift(...list.splice(i, 1));
      if (i < 0 && state.card.uuid) list.unshift(new CardView({ ...state.card, selected: true }));
      return delay(120, list);
    },
    SelectCard: async (uuid) => {
      const card = mounted.find((c) => c.uuid === uuid);
      if (!card) throw new Error(`card ${uuid} is not mounted`);
      state.card = new CardView({ ...card, selected: true });
      pushState();
      if (!pinned) enter(0);
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
      if (url.includes("bad")) {
        return delay(600, new ServerCheckView({ ok: false, code: "server_unreachable", params: { host: url, detail: "no such host" } }));
      }
      if (apiKey === "bad") {
        return delay(300, new ServerCheckView({ ok: false, code: "auth_failed", params: {} }));
      }
      return delay(220, new ServerCheckView({ ok: true, version: "v1.122.3", code: "", params: {} }));
    },
    SaveServer: async (url, apiKey) => {
      if (!validUrl(url)) throw new Error("server URL must start with http:// or https://");
      await delay(200, null);
      if (!apiKey.trim() && state.server.hasKey && hostOf(url) !== hostOf(state.server.url)) {
        throw new Error("key_required");
      }
      if (url.includes("readonly")) {
        throw new Error("save server settings: open ~/.config/sd-sync-immich/config.yaml: permission denied");
      }
      state.server = new ServerView({ url: normalizeUrl(url), hasKey: state.server.hasKey || apiKey.trim() !== "" });
      pushState();
    },
    SaveSettings: async (v) => {
      if (v.concurrency < 1 || v.concurrency > 16) throw new Error("concurrency must be between 1 and 16");
      // As in Go, the autostart fields are not saved here.
      state.settings = new SettingsView({
        ...v,
        autostart: state.settings.autostart,
        autostartAvailable: state.settings.autostartAvailable,
        autostartTranslocated: state.settings.autostartTranslocated,
      });
      state.language = resolveLanguage(v.language, navigator.languages);
      pushState();
    },
    SetAutostart: async (enabled) => {
      if (enabled && state.settings.autostartTranslocated) throw new Error("autostart_translocated");
      if (!state.settings.autostartAvailable) {
        throw new Error("autostart is only available when the app runs from SD Sync Immich.app");
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
    LogTail: async (lines) => delay(150, sampleLog(lines)),
    RevealLog: async () => console.info("[mock] RevealLog"),
    OpenImmich: async () => console.info("[mock] OpenImmich", SERVER_URL),
    CheckForUpdates: async () => {
      await delay(900, null);
      if (query.get("update") === "none") return null;
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
    },
  };

  const section = query.get("section");
  if (section) setTimeout(() => emit("navigate", section), 0);

  return {
    backend,
    on(name, cb) {
      const set = listeners[name] as Set<typeof cb>;
      set.add(cb);
      return () => set.delete(cb);
    },
  };
}
