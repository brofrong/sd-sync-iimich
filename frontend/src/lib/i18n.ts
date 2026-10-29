// Translation and locale-aware formatting. The catalogs are the JSON files
// shared with the Go backend (frontend/locales); t() follows the same rules
// as Go's Catalog.T: the current language, then English, then the key.
import { get, type Writable, writable } from "svelte/store";

type Messages = Record<string, string>;

const modules = import.meta.glob<Messages>("../../locales/*.json", {
  eager: true,
  import: "default",
});

const catalogs: Record<string, Messages> = {};
for (const [path, messages] of Object.entries(modules)) {
  const name = path.slice(path.lastIndexOf("/") + 1, -".json".length);
  catalogs[name] = messages;
}

/** Supported languages, in the order language pickers offer them (as in Go). */
export const supported = [
  "en",
  "ru",
  "de",
  "es",
  "fr",
  "pt-BR",
  "zh-Hans",
  "ja",
];

/** Each supported language in its own name, for language pickers. */
export const languageNames: Record<string, string> = {
  en: "English",
  ru: "Русский",
  de: "Deutsch",
  es: "Español",
  fr: "Français",
  "pt-BR": "Português (Brasil)",
  "zh-Hans": "简体中文",
  ja: "日本語",
};

/** The resolved UI language; the backend sets it from StateView.language. */
export const lang: Writable<string> = writable("en");

lang.subscribe((l) => {
  if (typeof document !== "undefined") document.documentElement.lang = l;
});

/** t translates key, substituting {name} placeholders from params. */
export function t(
  key: string,
  params?: Record<string, string | number | undefined>,
): string {
  const l = get(lang);
  const text = catalogs[l]?.[key] ?? catalogs.en?.[key] ?? key;
  if (!params) return text;
  return text.replace(/\{([a-zA-Z_]+)\}/g, (whole, name: string) => {
    const v = params[name];
    return v === undefined ? whole : String(v);
  });
}

/**
 * resolveLanguage mirrors Go's i18n.Resolve for the browser preview: an
 * explicit supported preference wins, then the first matching system
 * language, then English.
 */
export function resolveLanguage(
  preference: string,
  system: readonly string[],
): string {
  const exact = (tag: string) =>
    supported.find((s) => s.toLowerCase() === tag.toLowerCase());
  if (preference && exact(preference)) return exact(preference)!;
  for (const sys of system) {
    const s = sys.trim();
    if (!s) continue;
    if (s.toLowerCase().startsWith("zh-hans")) return "zh-Hans";
    if (s.toLowerCase().startsWith("pt")) return "pt-BR";
    const match = exact(s.split("-")[0]);
    if (match) return match;
  }
  return "en";
}

// ---- Formatting -----------------------------------------------------------

const cache = new Map<string, Intl.NumberFormat>();

function nf(options: Intl.NumberFormatOptions): Intl.NumberFormat {
  const l = get(lang);
  const id = l + JSON.stringify(options);
  let f = cache.get(id);
  if (!f) {
    f = new Intl.NumberFormat(l, options);
    cache.set(id, f);
  }
  return f;
}

/** formatNumber formats a count with grouping ("2,114", "2 114"). */
export function formatNumber(n: number): string {
  return nf({ maximumFractionDigits: 0 }).format(n);
}

const byteUnits = ["byte", "kilobyte", "megabyte", "gigabyte", "terabyte"];

function scaleBytes(bytes: number): [number, string] {
  let v = Math.max(0, bytes);
  let i = 0;
  // Decimal units, as Finder shows them.
  while (v >= 1000 && i < byteUnits.length - 1) {
    v /= 1000;
    i++;
  }
  return [v, byteUnits[i]];
}

function digits(v: number): Intl.NumberFormatOptions {
  return { maximumFractionDigits: v < 10 ? 1 : 0, minimumFractionDigits: 0 };
}

/** formatBytes formats a size: "8.1 GB", "256 ГБ". */
export function formatBytes(bytes: number): string {
  const [v, unit] = scaleBytes(bytes);
  return nf({ style: "unit", unit, unitDisplay: "short", ...digits(v) }).format(
    v,
  );
}

/** formatSpeed formats a transfer rate: "4.9 MB/s". */
export function formatSpeed(bytesPerSec: number): string {
  const [v, unit] = scaleBytes(bytesPerSec);
  return nf({
    style: "unit",
    unit: `${unit}-per-second`,
    unitDisplay: "short",
    ...digits(v),
  }).format(v);
}

/** formatDuration formats seconds compactly: "45 sec", "6 min", "1 hr 48 min". */
export function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  const unit = (n: number, u: string) =>
    nf({ style: "unit", unit: u, unitDisplay: "short" }).format(n);
  if (s < 60) return unit(s, "second");
  if (s < 3600) return unit(Math.ceil(s / 60), "minute");
  const h = Math.floor(s / 3600);
  const m = Math.round((s % 3600) / 60);
  return m ? `${unit(h, "hour")} ${unit(m, "minute")}` : unit(h, "hour");
}

/** formatRelative formats a past or future moment relative to now: "2 hours ago". */
export function formatRelative(date: Date, now: Date = new Date()): string {
  const rtf = new Intl.RelativeTimeFormat(get(lang), { numeric: "auto" });
  const diff = (date.getTime() - now.getTime()) / 1000;
  const abs = Math.abs(diff);
  const steps: [number, Intl.RelativeTimeFormatUnit][] = [
    [60, "second"],
    [3600, "minute"],
    [86400, "hour"],
    [604800, "day"],
    [2629800, "week"],
    [31557600, "month"],
    [Infinity, "year"],
  ];
  const sizes: Record<string, number> = {
    second: 1,
    minute: 60,
    hour: 3600,
    day: 86400,
    week: 604800,
    month: 2629800,
    year: 31557600,
  };
  for (const [limit, u] of steps) {
    if (abs < limit) {
      if (u === "second") return rtf.format(0, "second");
      return rtf.format(Math.round(diff / sizes[u]), u);
    }
  }
  return rtf.format(Math.round(diff / sizes.year), "year");
}

const dateFormats = new Map<string, Intl.DateTimeFormat>();

function df(options: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  const l = get(lang);
  const id = l + JSON.stringify(options);
  let f = dateFormats.get(id);
  if (!f) {
    f = new Intl.DateTimeFormat(l, options);
    dateFormats.set(id, f);
  }
  return f;
}

/** formatTime formats the time of day: "14:05", "2:05 PM". */
export function formatTime(date: Date): string {
  return df({ timeStyle: "short" }).format(date);
}

/**
 * formatDay names the calendar day of date: "Today", "Yesterday", then
 * "Monday, October 19" within the year and "October 19, 2025" before it.
 */
export function formatDay(date: Date, now: Date = new Date()): string {
  const midnight = (d: Date) =>
    new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const days = Math.round((midnight(date) - midnight(now)) / 86400e3);
  if (days === 0 || days === -1) {
    const text = new Intl.RelativeTimeFormat(get(lang), {
      numeric: "auto",
    }).format(days, "day");
    return text.charAt(0).toLocaleUpperCase(get(lang)) + text.slice(1);
  }
  if (date.getFullYear() === now.getFullYear()) {
    return df({ weekday: "long", month: "long", day: "numeric" }).format(date);
  }
  return df({ dateStyle: "long" }).format(date);
}
