// The window's connection to Go: the bound Service, the "status", "state"
// and "navigate" events, and stores that hold the latest status and state.
//
// Outside the Wails webview (a plain browser, `npm run dev`) or with
// VITE_MOCK=1 the same interface is served by mock.ts, so the UI can be
// developed and screenshotted without the app.
import { writable, type Readable } from "svelte/store";
import { Events } from "@wailsio/runtime";
import { Service, StateView } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/gui";
import { Status } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/app";
import { lang } from "./i18n";
import { navigate } from "./stores";

type ServiceApi = typeof Service;

/** Backend has the methods of the bound Service, returning plain promises. */
export type Backend = {
  [K in keyof ServiceApi]: (...args: Parameters<ServiceApi[K]>) => Promise<Awaited<ReturnType<ServiceApi[K]>>>;
};

/** Payloads of the events Go sends to the window. */
export interface EventMap {
  status: Status;
  state: StateView;
  navigate: string;
}

/** Transport is a backend plus a way to listen to its events. */
export interface Transport {
  backend: Backend;
  on<E extends keyof EventMap>(name: E, cb: (data: EventMap[E]) => void): () => void;
}

declare global {
  interface Window {
    webkit?: { messageHandlers?: Record<string, unknown> };
    chrome?: { webview?: unknown };
  }
}

/** True inside the Wails webview (the native message bridge exists). */
function hasWailsRuntime(): boolean {
  return Boolean(window.webkit?.messageHandlers?.["external"] || window.chrome?.webview);
}

export const isMock = import.meta.env.VITE_MOCK === "1" || !hasWailsRuntime();

function wailsTransport(): Transport {
  return {
    backend: Service,
    on(name, cb) {
      return Events.On(name, (ev: { data: unknown }) => cb(ev.data as never));
    },
  };
}

const transport: Promise<Transport> = isMock
  ? import("./mock").then((m) => m.createMockTransport())
  : Promise.resolve(wailsTransport());

/** backend calls the Service (or the mock once it has loaded). */
export const backend: Backend = new Proxy({} as Backend, {
  get(_, name: string) {
    return (...args: unknown[]) =>
      transport.then((tr) => (tr.backend as unknown as Record<string, (...a: unknown[]) => Promise<unknown>>)[name](...args));
  },
});

const statusStore = writable<Status>(new Status());
const stateStore = writable<StateView>(new StateView());

/** The latest status. Its code is "" until the first state arrives. */
export const status: Readable<Status> = { subscribe: statusStore.subscribe };

/** The latest full state (server, card, settings, language, …). */
export const appState: Readable<StateView> = { subscribe: stateStore.subscribe };

let lastStatusAt = 0;

/** parseTime parses a Go time (RFC 3339 with up to nanoseconds); 0 if invalid. */
export function parseTime(s: string | null | undefined): number {
  if (!s) return 0;
  return Date.parse(s.replace(/(\.\d{3})\d+/, "$1")) || 0;
}

function setStatus(s: Status): void {
  const at = parseTime(s.at);
  // A "state" event can carry an older status than the last "status"
  // event (status events are pushed more often); keep the newest.
  if (at) {
    if (at < lastStatusAt) return;
    lastStatusAt = at;
  }
  statusStore.set({ ...s, params: s.params ?? {}, actions: s.actions ?? [] });
}

function setState(v: StateView): void {
  stateStore.set(v);
  if (v.language) lang.set(v.language);
  setStatus(v.status);
}

/**
 * connect loads the current state and subscribes to the backend events.
 * It returns a function that unsubscribes.
 */
export function connect(): () => void {
  let offs: (() => void)[] = [];
  let closed = false;
  transport.then((tr) => {
    if (closed) return;
    offs = [
      tr.on("status", setStatus),
      tr.on("state", setState),
      tr.on("navigate", navigate),
    ];
    tr.backend.State().then(setState, (err) => console.error("State failed", err));
  });
  return () => {
    closed = true;
    offs.forEach((off) => off());
  };
}
