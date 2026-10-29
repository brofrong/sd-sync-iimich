<script lang="ts">
  // The Status section: the status card, the selected card's own actions
  // (eject, format) while it is plugged in, then three tiles (card, server,
  // last sync) that each open the section where it can be changed. Once
  // the server is set up, a card asks, once, whether to send anonymous
  // statistics.
  import { onMount } from "svelte";
  import type { SyncRun } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/state";
  import Button from "../components/Button.svelte";
  import FormatDialog from "../components/FormatDialog.svelte";
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

  // ---- The selected card's actions --------------------------------------------
  const card = $derived($appState.card);
  const showCardBar = $derived(
    Boolean(card?.uuid && card.mounted) && code !== "" && code !== "not_configured" && code !== "no_card_selected",
  );
  // While the card is read, verified or erased it is left alone.
  const cardBusy = $derived(["scanning", "syncing", "verifying", "format_pending", "formatting"].includes(code));
  const serverReady = $derived(Boolean($appState.server?.url && $appState.server?.hasKey));
  const formatHint = $derived(
    $appState.settings?.autoFormatNeedsAdmin
      ? t("card_bar.format_needs_admin")
      : !serverReady
        ? t("card_bar.format_needs_server")
        : "",
  );
  let formatOpen = $state(false);
  let cardError = $state("");

  // ---- The statistics question --------------------------------------------------
  let statsError = $state("");
  async function answerStats(enabled: boolean): Promise<void> {
    statsError = "";
    try {
      await backend.SetTelemetry(enabled);
    } catch (err) {
      statsError = t("ui.action_failed", { detail: err instanceof Error ? err.message : String(err) });
    }
  }

  function failed(err: unknown): void {
    cardError = t("ui.action_failed", { detail: err instanceof Error ? err.message : String(err) });
  }

  async function eject(): Promise<void> {
    cardError = "";
    try {
      await backend.EjectCard();
    } catch (err) {
      failed(err);
    }
  }

  async function format(): Promise<void> {
    formatOpen = false;
    cardError = "";
    try {
      await backend.FormatCard();
    } catch (err) {
      failed(err);
    }
  }

  // The dialog is about the card that was plugged in when it opened.
  $effect(() => {
    if (!showCardBar || cardBusy) formatOpen = false;
  });

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

  {#if showCardBar}
    <GlassCard padding="10px 12px 10px 14px">
      <div class="card-bar">
        <svg class="card-icon" viewBox="0 0 24 24" aria-hidden="true"><path d={icons.cards} /></svg>
        <span class="card-name" title={card.name}>{card.name}</span>
        {#if card.sizeBytes > 0}<span class="card-size">{formatBytes(card.sizeBytes)}</span>{/if}
        <div class="card-actions">
          <Button disabled={cardBusy} onclick={eject}>{t("card_bar.eject")}</Button>
          <span title={formatHint}>
            <Button disabled={cardBusy || formatHint !== ""} onclick={() => (formatOpen = true)}>{t("card_bar.format")}</Button>
          </span>
        </div>
      </div>
      {#if cardError}<p class="card-error" role="alert">{cardError}</p>{/if}
    </GlassCard>
  {/if}

  {#if configured && !$appState.settings.telemetryAsked}
    <GlassCard padding="14px 16px">
      <div class="ask" role="region" aria-labelledby="stats-ask-title">
        <svg class="ask-icon" viewBox="0 0 24 24" aria-hidden="true"><path d={icons.stats} /></svg>
        <div class="ask-text">
          <h2 id="stats-ask-title">{t("stats.ask_title")}</h2>
          <p>{t("stats.ask_text")}</p>
          {#if statsError}<p class="card-error" role="alert">{statsError}</p>{/if}
        </div>
        <div class="ask-actions">
          <Button onclick={() => answerStats(false)}>{t("stats.ask_no")}</Button>
          <Button variant="primary" onclick={() => answerStats(true)}>{t("stats.ask_yes")}</Button>
        </div>
      </div>
    </GlassCard>
  {/if}

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

{#if formatOpen}
  <FormatDialog name={card.name} sizeBytes={card.sizeBytes} onconfirm={format} oncancel={() => (formatOpen = false)} />
{/if}

<style>
  .status {
    display: grid;
    gap: 14px;
  }

  /* ---- The statistics question ---- */
  .ask {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 10px 14px;
  }
  .ask-icon {
    width: 20px;
    height: 20px;
    flex-shrink: 0;
    margin-top: 1px;
    fill: none;
    stroke: var(--accent-soft, var(--text-2));
    stroke-width: 1.8;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  .ask-text {
    flex: 1 1 320px;
    min-width: 0;
  }
  .ask-text h2 {
    margin: 0 0 4px;
    color: var(--text);
    font-size: 14px;
    font-weight: 600;
  }
  .ask-text p {
    margin: 0;
    color: var(--text-2);
    font-size: 12.5px;
    line-height: 1.45;
  }
  .ask-actions {
    display: flex;
    gap: 8px;
    margin-left: auto;
    align-self: center;
  }

  /* ---- The selected card's actions ---- */
  .card-bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 10px;
  }
  .card-icon {
    width: 16px;
    height: 16px;
    flex-shrink: 0;
    fill: none;
    stroke: var(--text-2);
    stroke-width: 1.8;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  .card-name {
    min-width: 0;
    overflow: hidden;
    color: var(--text);
    font-size: 13px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .card-size {
    color: var(--text-3);
    font-size: 12px;
    white-space: nowrap;
  }
  .card-actions {
    display: flex;
    gap: 8px;
    margin-left: auto;
  }
  .card-error {
    margin: 8px 0 0;
    color: #ff8a82;
    font-size: 12px;
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
