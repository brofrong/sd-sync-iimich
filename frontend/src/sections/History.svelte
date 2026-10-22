<script lang="ts">
  // The History section: the last 50 sync runs grouped by day, each with
  // its result, card, counts, size and duration. A run without a finish
  // time is the sync in progress (the newest one, while syncing) or one the
  // app never finished (it quit or crashed mid-sync).
  import { fly } from "svelte/transition";
  import type { SyncRun } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/state";
  import GlassCard from "../components/GlassCard.svelte";
  import { backend, parseTime, status } from "../lib/backend";
  import { formatBytes, formatDay, formatDuration, formatNumber, formatTime, t } from "../lib/i18n";
  import { icons } from "../lib/icons";
  import { errorKindLines } from "../lib/runs";
  import { reducedMotion, toneFor, type Tone } from "../lib/stores";

  const LIMIT = 50;

  let runs = $state<SyncRun[] | null>(null);
  let loadError = $state("");
  const ms = (n: number) => ($reducedMotion ? 0 : n);

  function message(err: unknown): string {
    return err instanceof Error ? err.message : String(err);
  }

  function load(): void {
    backend.History(LIMIT).then(
      (r) => {
        runs = r ?? [];
        loadError = "";
      },
      (err) => {
        loadError = t("ui.load_failed", { detail: message(err) });
      },
    );
  }

  // A run starts or ends whenever the status code changes; reload then
  // (and once the first status has arrived).
  const code = $derived($status.code as string);
  const active = $derived(code === "scanning" || code === "syncing");
  let loadedFor: string | null = null;
  $effect(() => {
    if (!code || code === loadedFor) return;
    loadedFor = code;
    load();
  });

  interface Row {
    id: number;
    title: string;
    tone: Tone;
    time: string;
    card: string;
    stats: { text: string; bad?: boolean }[];
    kinds: string[];
    detail: string;
  }

  function toRow(run: SyncRun, newest: boolean): Row {
    const started = parseTime(run.startedAt);
    const finished = parseTime(run.finishedAt);
    let title: string;
    let tone: Tone;
    if (!finished || !run.resultCode) {
      const running = newest && active && !finished;
      title = running ? t("history.in_progress") : t("history.interrupted");
      tone = running ? "accent" : "warn";
    } else {
      title = t(`status.${run.resultCode}.title`);
      tone = toneFor(run.resultCode);
    }

    // Go writes the counts when a run finishes: an unfinished run's zeros
    // mean "unknown", so it shows none.
    const counted = Boolean(finished) || run.uploaded + run.skipped + run.errors > 0;
    const stats: Row["stats"] = counted ? [{ text: t("history.uploaded", { count: formatNumber(run.uploaded) }) }] : [];
    if (run.skipped > 0) stats.push({ text: t("history.skipped", { count: formatNumber(run.skipped) }) });
    if (run.errors > 0) stats.push({ text: t("history.errors", { count: formatNumber(run.errors) }), bad: true });
    if (run.bytes > 0) stats.push({ text: formatBytes(run.bytes) });
    if (started && finished > started) stats.push({ text: formatDuration((finished - started) / 1000) });

    const kinds = errorKindLines(run.errorSummary);

    // The technical reason a run stopped, as Go reported it.
    const p = run.resultParams ?? {};
    const detail = finished && p.detail ? [p.status, p.detail].filter(Boolean).join(" ") : "";

    return {
      id: run.id,
      title,
      tone,
      time: started ? formatTime(new Date(started)) : "",
      card: run.volumeName || t("cards.unnamed"),
      stats,
      kinds,
      detail,
    };
  }

  const groups = $derived.by(() => {
    if (!runs) return [];
    const now = new Date();
    const out: { day: string; rows: Row[] }[] = [];
    runs.forEach((run, i) => {
      const at = parseTime(run.startedAt);
      const day = at ? formatDay(new Date(at), now) : "";
      const row = toRow(run, i === 0);
      const last = out[out.length - 1];
      if (last && last.day === day) last.rows.push(row);
      else out.push({ day, rows: [row] });
    });
    return out;
  });
</script>

<div class="section">
  {#if loadError}
    <GlassCard tone="error">
      <p class="error-text" role="alert">{loadError}</p>
    </GlassCard>
  {:else if runs === null}
    <GlassCard>
      <div class="skeleton-lines" aria-hidden="true"><i></i><i></i><i></i></div>
    </GlassCard>
  {:else if runs.length === 0}
    <GlassCard>
      <p class="empty-state"><svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons.history} /></svg><span>{t("history.empty")}</span></p>
    </GlassCard>
  {:else}
    <GlassCard padding="4px 18px 6px">
      {#each groups as group, g (group.day + g)}
        <section class="day" aria-labelledby="day-{g}">
          <h2 id="day-{g}">{group.day}</h2>
          <ol>
            {#each group.rows as row, i (row.id)}
              <li in:fly={{ y: 6, duration: ms(300), delay: ms(Math.min(g * 3 + i, 12) * 30) }}>
                <span class="dot {row.tone}" aria-hidden="true"></span>
                <div class="main">
                  <div class="line">
                    <span class="title">{row.title}</span>
                    <span class="card" title={row.card}>{row.card}</span>
                    <time class="time">{row.time}</time>
                  </div>
                  {#if row.stats.length}
                    <div class="stats">
                      {#each row.stats as stat, s (s)}
                        <span class:bad={stat.bad}>{stat.text}</span>
                      {/each}
                    </div>
                  {/if}
                  {#if row.kinds.length}
                    <div class="kinds">
                      {#each row.kinds as kind, k (k)}<span>{kind}</span>{/each}
                    </div>
                  {/if}
                  {#if row.detail}
                    <p class="detail-text">{row.detail}</p>
                  {/if}
                </div>
              </li>
            {/each}
          </ol>
        </section>
      {/each}
    </GlassCard>
  {/if}
</div>

<style>
  .day + .day {
    border-top: 1px solid var(--line);
  }
  h2 {
    margin: 0;
    padding: 12px 0 2px;
    color: var(--text-3);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.05em;
    text-transform: uppercase;
  }
  ol {
    margin: 0;
    padding: 0;
    list-style: none;
  }
  li {
    display: flex;
    gap: 11px;
    padding: 10px 0;
  }
  li + li {
    border-top: 1px solid rgba(255, 255, 255, 0.05);
  }

  .main {
    flex: 1;
    min-width: 0;
  }
  .line {
    display: flex;
    align-items: baseline;
    gap: 8px;
    min-width: 0;
  }
  .title {
    flex-shrink: 0;
    max-width: 60%;
    overflow: hidden;
    color: var(--text);
    font-size: 13px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .card {
    min-width: 0;
    overflow: hidden;
    color: var(--text-2);
    font-size: 12.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .time {
    margin-left: auto;
    flex-shrink: 0;
    color: var(--text-3);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
  }

  .stats,
  .kinds {
    display: flex;
    flex-wrap: wrap;
    gap: 2px 0;
    margin-top: 3px;
    color: var(--text-3);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
  }
  .stats span:not(:last-child)::after,
  .kinds span:not(:last-child)::after {
    content: "·" / "";
    margin: 0 6px;
    color: rgba(255, 255, 255, 0.25);
  }
  .stats .bad {
    color: #ff8a80;
  }
  .kinds {
    color: rgba(255, 179, 64, 0.85);
    font-size: 11.5px;
  }

  .dot {
    width: 8px;
    height: 8px;
    margin-top: 5px;
    flex-shrink: 0;
    border-radius: 50%;
    background: rgba(255, 255, 255, 0.3);
  }
  .dot.success {
    background: var(--green);
    box-shadow: 0 0 6px rgba(48, 209, 88, 0.5);
  }
  .dot.error {
    background: var(--red);
    box-shadow: 0 0 6px rgba(255, 69, 58, 0.5);
  }
  .dot.warn {
    background: var(--amber);
  }
  .dot.accent {
    background: var(--accent-3);
    box-shadow: 0 0 8px var(--accent-3);
    animation: breathe 1.6s ease-in-out infinite;
  }
  @keyframes breathe {
    50% {
      opacity: 0.45;
      transform: scale(0.8);
    }
  }


</style>
