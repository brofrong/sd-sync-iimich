// UI-wide state that is not backend data: the current section, page
// visibility and the reduced-motion preference, plus how status codes map
// onto the card's colour tone.
import { readable, writable } from "svelte/store";

/** Window sections, in sidebar order. */
export const sections = [
  "status",
  "gallery",
  "history",
  "cards",
  "settings",
] as const;
export type SectionId = (typeof sections)[number];

export function isSection(id: string): id is SectionId {
  return (sections as readonly string[]).includes(id);
}

/** The section the window shows. The "navigate" event from Go sets it too. */
export const section = writable<SectionId>("status");

/** True while the log panel is open over the window. */
export const logsOpen = writable(false);

/**
 * Navigates to id if it names a section. "server" (the server settings,
 * first in Settings) shows Settings; "logs" opens the log panel over the
 * current section.
 */
export function navigate(id: string): void {
  if (id === "logs") logsOpen.set(true);
  else if (id === "server") section.set("settings");
  else if (isSection(id)) section.set(id);
}

/** False while the window is hidden or minimised. */
export const pageVisible = readable(
  typeof document === "undefined" || !document.hidden,
  (set) => {
    const update = () => set(!document.hidden);
    document.addEventListener("visibilitychange", update);
    return () => document.removeEventListener("visibilitychange", update);
  },
);

/** True when the user asked the system to reduce motion. */
export const reducedMotion = readable(false, (set) => {
  const mq = window.matchMedia("(prefers-reduced-motion: reduce)");
  set(mq.matches);
  const update = () => set(mq.matches);
  mq.addEventListener("change", update);
  return () => mq.removeEventListener("change", update);
});

/** Colour tone of the status card. */
export type Tone = "idle" | "accent" | "error" | "warn" | "success";

/** Status codes that mean sync stopped because something went wrong. */
export const errorCodes: readonly string[] = [
  "server_unreachable",
  "auth_failed",
  "server_error",
  "card_removed",
  "failed",
  "format_failed",
  "format_needs_admin",
];

export function toneFor(code: string): Tone {
  if (errorCodes.includes(code)) return "error";
  switch (code) {
    case "scanning":
    case "syncing":
    case "verifying":
    case "format_pending":
    case "formatting":
      return "accent";
    case "done":
    case "nothing_new":
    case "ejected":
    case "formatted":
      return "success";
    case "done_with_errors":
    case "other_card_mounted":
    case "format_skipped_missing":
    case "format_skipped_unsupported":
      return "warn";
    default:
      return "idle";
  }
}
