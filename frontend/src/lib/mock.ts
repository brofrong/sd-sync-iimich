// A stand-in backend for developing the UI in a plain browser (no Wails
// runtime) or with VITE_MOCK=1. It replays the mockup's scenario in a loop,
// syncing → server_unreachable → done, and answers every Service method
// with plausible data.
//
// URL parameters (handy for screenshots):
//   ?state=<code>   pin the status to one code (e.g. done, not_configured)
//   ?lang=<tag>     UI language (default: the browser's language)
//   ?section=<id>   open a section
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
  const run = (hoursAgo: number, minutes: number, code: string, p: Record<string, string>, uploaded: number, errors = 0) => {
    const finished = new Date(now - hoursAgo * 3600e3);
    return new SyncRun({
      startedAt: new Date(finished.getTime() - minutes * 60e3).toISOString(),
      finishedAt: finished.toISOString(),
      volumeUUID: mainCard.uuid,
      volumeName: mainCard.name,
      resultCode: code,
      resultParams: p,
      uploaded,
      skipped: 3,
      errors,
      bytes: uploaded * 14.6e6,
      errorSummary: errors ? { network: errors } : {},
    });
  };
  const runs = [
    run(2, 108, "done", { uploaded: "2114" }, 2114),
    run(27, 12, "server_unreachable", params.server_unreachable, 412),
    run(75, 41, "done_with_errors", { uploaded: "806", errors: "4" }, 806, 4),
    run(170, 3, "nothing_new", {}, 0),
  ];
  return runs.map((r, i) => Object.assign(r, { id: runs.length - i }));
}

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

  const settings = new SettingsView({ language: query.get("lang") ?? "", concurrency: 4, autoPaths: true });
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
  if (pinned === "not_configured") {
    state.server = new ServerView();
    state.card = new CardView();
  } else if (pinned === "no_card_selected") {
    state.card = new CardView();
  } else if (pinned === "waiting_for_card") {
    state.card = new CardView({ ...mainCard, mounted: false, mountPoint: "", sizeBytes: 0 });
  }
  if (pinned === "syncing") state.status = makeStatus("syncing", syncProgress(0.38, 0));
  if (pinned === "scanning") state.status = makeStatus("scanning");

  let history = sampleHistory();
  const cards = [new CardView(mainCard), new CardView(otherCard)];

  function setStatus(s: Status): void {
    state.status = s;
    emit("status", s);
  }
  function pushState(): void {
    emit("state", new StateView({ ...state, status: state.status }));
  }
  function record(code: string, p: Record<string, string>, uploaded: number): void {
    const finished = new Date();
    history = [
      new SyncRun({
        id: (history[0]?.id ?? 0) + 1,
        startedAt: new Date(finished.getTime() - 14 * 60e3).toISOString(),
        finishedAt: finished.toISOString(),
        volumeUUID: state.card.uuid,
        volumeName: state.card.name,
        resultCode: code,
        resultParams: p,
        uploaded,
        bytes: uploaded * 14.6e6,
      }),
      ...history,
    ].slice(0, 50);
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
      setStatus(makeStatus("syncing", syncProgress(fraction, tick)));
    } else if (code === "server_unreachable") {
      record(code, params[code], Math.round(FILES * fraction) - 412);
      setStatus(makeStatus(code, null, { ...params[code] }));
    } else {
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
    setStatus(s);
    hold = pinned ? 0 : ms;
  }

  const delay = <T>(ms: number, v: T) => new Promise<T>((r) => setTimeout(() => r(v), ms));

  const backend: Backend = {
    State: async () => new StateView({ ...state }),
    Cards: async () => cards.map((c) => new CardView({ ...c, selected: c.uuid === state.card.uuid })),
    SelectCard: async (uuid) => {
      const card = cards.find((c) => c.uuid === uuid);
      if (!card) throw new Error("card is not mounted");
      state.card = new CardView({ ...card, selected: true });
      pushState();
      if (!pinned) enter(0);
    },
    EjectCard: async () => {
      await delay(400, null);
      state.card = new CardView({ ...state.card, mounted: false, mountPoint: "", sizeBytes: 0 });
      pushState();
      interrupt(makeStatus("waiting_for_card", null, { name: state.card.name }), 5000);
      setTimeout(() => {
        state.card = new CardView(mainCard);
        pushState();
      }, 5000);
    },
    TestServer: async (url, apiKey) => {
      if (url.includes("bad")) {
        return delay(600, new ServerCheckView({ ok: false, code: "server_unreachable", params: { host: url, detail: "no such host" } }));
      }
      if (apiKey === "bad") {
        return delay(300, new ServerCheckView({ ok: false, code: "auth_failed", params: {} }));
      }
      return delay(220, new ServerCheckView({ ok: true, version: "v1.122.3", code: "", params: {} }));
    },
    SaveServer: async (url, apiKey) => {
      state.server = new ServerView({ url, hasKey: state.server.hasKey || apiKey !== "" });
      pushState();
    },
    SaveSettings: async (v) => {
      state.settings = new SettingsView(v);
      state.language = resolveLanguage(v.language, navigator.languages);
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
    LogTail: async (lines) => {
      const out: string[] = [];
      const t0 = Date.now() - lines * 2000;
      for (let i = 0; i < lines; i++) {
        const ts = new Date(t0 + i * 2000).toISOString();
        out.push(
          i % 7 === 6
            ? `${ts} WARN upload failed, retrying file=DSC0${7100 + i}.ARW err="dial tcp: lookup ${HOST}: no such host"`
            : `${ts} INFO uploaded file=DSC0${7100 + i}.ARW bytes=${14_600_000 + i * 1013}`,
        );
      }
      return out.join("\n");
    },
    RevealLog: async () => console.info("[mock] RevealLog"),
    OpenImmich: async () => console.info("[mock] OpenImmich", SERVER_URL),
    CheckForUpdates: async () => {
      const u = await delay(700, new UpdateView({ version: "v0.5.0", notes: "Faster scanning.", url: "https://github.com/" }));
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
