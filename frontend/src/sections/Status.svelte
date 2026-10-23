<script lang="ts">
  // The Status section: the status card, then three tiles (card, server,
  // last sync) that each open the section where it can be changed.
  import { onMount } from "svelte";
  import type { SyncRun } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/state";
  import GlassCard from "../components/GlassCard.svelte";
  import StatusCard from "../components/StatusCard.svelte";
  import { appState, backend, parseTime, status } from "../lib/backend";
  import { formatBytes, formatDuration, formatNumber, formatRelative, t } from "../lib/i18n";
  import { icons, type IconName } from "../lib/icons";
  import { errorKindLines } from "../lib/runs";
  import { pageVisible, toneFor, type Tone } from "../lib/stores";

  interface Props {
    onNavigate: (section: string) => void;
  }

  let { onNavigate }: Props = $props();

  const code = $derived($status.code as string);

  // ---- Last sync ----------------------------------------------------------------
  let lastRun = $state<SyncRun | null | undefined>(undefined);
  let now = $state(Date.now());

  function loadHistory(): void {
    backend.History(1).then(
      (runs) => (lastRun = runs[0] ?? null),
      (err) => console.error("History failed", err),
    );
  }

  // A run ends whenever the status code changes; reload then (and once
  // the first status has arrived).
  let historyCode: string | null = null;
  $effect(() => {
    if (!code || code === historyCode) return;
    historyCode = code;
    loadHistory();
  });

  // Keep "2 minutes ago" current while the window is visible.
  onMount(() => {
    const timer = setInterval(() => {
      if ($pageVisible) now = Date.now();
    }, 30_000);
    return () => clearInterval(timer);
  });

  // ---- Server check ---------------------------------------------------------------
  type Check =
    | { state: "idle" }
    | { state: "checking" }
    | { state: "ok"; latency: number; version: string }
    | { state: "fail"; code: string };
  let check = $state<Check>({ state: "idle" });

  const serverUrl = $derived($appState.server.url);
  const configured = $derived(Boolean(serverUrl && $appState.server.hasKey));
  const serverIssue = $derived(["server_unreachable", "auth_failed", "server_error"].includes(code));

  async function checkServer(url: string): Promise<void> {
    check = { state: "checking" };
    const started = performance.now();
    try {
      const res = await backend.TestServer(url, "");
      const latency = Math.round(performance.now() - started);
      check = res.ok ? { state: "ok", latency, version: res.version } : { state: "fail", code: res.code };
    } catch {
      check = { state: "fail", code: "failed" };
    }
  }

  // Check on open, when the URL changes, and when a server problem clears.
  let checkedFor: string | null = null;
  $effect(() => {
    if (!configured || serverIssue) return;
    const key = serverUrl;
    if (key === checkedFor) return;
    checkedFor = key;
    checkServer(serverUrl);
  });
  $effect(() => {
    if (serverIssue) checkedFor = null;
  });

  function hostOf(url: string): string {
    try {
      return new URL(url).host || url;
    } catch {
      return url;
    }
  }

  // ---- Tiles -------------------------------------------------------------------------
  interface Tile {
    id: string;
    icon: IconName;
    label: string;
    value: string;
    sub: string;
    tone: Tone | "muted";
  }

  const cardTile = $derived.by((): Tile => {
    const c = $appState.card;
    if (!c.uuid) {
      return { id: "cards", icon: "cards", label: t("tile.card"), value: t("tile.card_none"), sub: t("tile.card_choose"), tone: "muted" };
    }
    return {
      id: "cards",
      icon: "cards",
      label: t("tile.card"),
      value: c.name || c.uuid,
      sub: c.mounted
        ? t("tile.card_connected") + (c.sizeBytes > 0 ? ` · ${formatBytes(c.sizeBytes)}` : "")
        : t("tile.card_disconnected"),
      tone: c.mounted ? "success" : "muted",
    };
  });

  const serverTile = $derived.by((): Tile => {
    const base = { id: "server", icon: "server" as IconName, label: t("tile.server") };
    if (!serverUrl) return { ...base, value: t("tile.server_none"), sub: t("tile.server_setup"), tone: "muted" };
    const value = hostOf(serverUrl);
    const failure = (c: string): Tile => {
      if (c === "auth_failed") return { ...base, value, sub: t("tile.server_rejected"), tone: "error" };
      if (c === "server_error") return { ...base, value, sub: t("status.server_error.title"), tone: "error" };
      return { ...base, value, sub: t("tile.server_offline"), tone: "error" };
    };
    if (!$appState.server.hasKey) return { ...base, value, sub: t("tile.server_setup"), tone: "muted" };
    if (serverIssue) return failure(code);
    switch (check.state) {
      case "ok":
        return { ...base, value, sub: t("tile.server_online", { latency: formatNumber(check.latency) }), tone: "success" };
      case "fail":
        return failure(check.code);
      default:
        return { ...base, value, sub: t("tile.server_checking"), tone: "muted" };
    }
  });

  const lastTile = $derived.by((): Tile => {
    const base = { id: "history", icon: "clock" as IconName, label: t("tile.last_sync") };
    if (lastRun === undefined) return { ...base, value: "…", sub: "", tone: "muted" };
    if (lastRun === null) return { ...base, value: t("tile.last_sync_never"), sub: "", tone: "muted" };
    const finished = parseTime(lastRun.finishedAt) || parseTime(lastRun.startedAt);
    const started = parseTime(lastRun.startedAt);
    const value = formatRelative(new Date(finished), new Date(Math.max(now, finished)));
    const runTone = toneFor(lastRun.resultCode);
    let sub: string;
    if (["done", "done_with_errors"].includes(lastRun.resultCode)) {
      sub = t("tile.uploaded", { count: formatNumber(lastRun.uploaded) });
      if (started && finished > started) sub += ` · ${formatDuration((finished - started) / 1000)}`;
    } else {
      sub = t(`status.${lastRun.resultCode}.title`);
    }
    return { ...base, value, sub, tone: runTone === "idle" ? "muted" : runTone };
  });

  const tiles = $derived([cardTile, serverTile, lastTile]);

  // done_with_errors says why on the card: the last run's failures by kind.
  const errorKinds = $derived(
    code === "done_with_errors" && lastRun?.resultCode === "done_with_errors" ? errorKindLines(lastRun.errorSummary) : [],
  );
</script>

<div class="status">
  <StatusCard status={$status} view={$appState} {onNavigate} {errorKinds} />

  <div class="tiles">
    {#each tiles as tile (tile.id)}
      <GlassCard padding="0">
        <button type="button" class="tile" onclick={() => onNavigate(tile.id)}>
          <span class="label">
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons[tile.icon]} /></svg>
            {tile.label}
            <svg class="chevron" viewBox="0 0 24 24" aria-hidden="true"><path d="M9 6l6 6-6 6" /></svg>
          </span>
          <span class="value" title={tile.value}>{tile.value}</span>
          {#if tile.sub}
            <span class="sub" title={tile.sub}><i class="dot {tile.tone}" aria-hidden="true"></i><span class="text">{tile.sub}</span></span>
          {/if}
        </button>
      </GlassCard>
    {/each}
  </div>
</div>

<style>
  .status {
    display: grid;
    gap: 14px;
  }

  .tiles {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
    gap: 12px;
  }

  .tile {
    display: grid;
    gap: 4px;
    width: 100%;
    padding: 12px 14px 13px;
    border: 0;
    border-radius: inherit;
    background: transparent;
    text-align: left;
    cursor: pointer;
  }
  .tile:focus-visible {
    outline-offset: -3px;
    border-radius: var(--radius-l);
  }

  .label {
    display: flex;
    align-items: center;
    gap: 6px;
    color: var(--text-3);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }
  .label svg {
    width: 13px;
    height: 13px;
    fill: none;
    stroke: currentColor;
    stroke-width: 1.8;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  .label .chevron {
    margin-left: auto;
    opacity: 0;
    transform: translateX(-4px);
    transition:
      opacity 0.2s,
      transform 0.3s var(--spring);
  }
  .tile:hover .chevron,
  .tile:focus-visible .chevron {
    opacity: 1;
    transform: none;
  }

  .value {
    overflow: hidden;
    color: var(--text);
    font-size: 15px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .sub {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    overflow: hidden;
    color: var(--text-2);
    font-size: 12px;
  }
  .sub .text {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .dot {
    width: 7px;
    height: 7px;
    flex-shrink: 0;
    border-radius: 50%;
    background: rgba(255, 255, 255, 0.3);
    transition: background-color 0.4s, box-shadow 0.4s;
  }
  .dot.success {
    background: var(--green);
    box-shadow: 0 0 6px rgba(48, 209, 88, 0.6);
  }
  .dot.error {
    background: var(--red);
    box-shadow: 0 0 6px rgba(255, 69, 58, 0.6);
  }
  .dot.warn {
    background: var(--amber);
  }
  .dot.accent {
    background: var(--accent-3);
  }
</style>
