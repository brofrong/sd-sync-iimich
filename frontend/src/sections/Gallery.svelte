<script lang="ts">
  // The Gallery section: the JPEG photos on the selected card while it is
  // plugged in, newest first, grouped by the day they were taken. A header
  // card names the card, counts its photos and those already in Immich
  // (with a thin bar for the share) and says how many videos and RAW files
  // are not shown. Each thumbnail carries a status dot: in Immich (green
  // check), uploading right now (blue arrow and a bar along the bottom),
  // waiting (an empty ring) or failed in the card's last sync (red "!" and
  // a red frame). Without the card it asks for it, or says another card is
  // plugged in and links to Cards. The list reloads on entry, when the
  // card or the status changes, and while syncing as files finish, at most
  // every 2 s. Clicking a photo opens it in the viewer.
  import { onDestroy, untrack } from "svelte";
  import type { List, Photo } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/gallery";
  import Button from "../components/Button.svelte";
  import GlassCard from "../components/GlassCard.svelte";
  import { appState, backend, parseTime, status } from "../lib/backend";
  import { formatDay, formatNumber, t } from "../lib/i18n";
  import { icons } from "../lib/icons";

  interface Props {
    onNavigate: (section: string) => void;
  }

  let { onNavigate }: Props = $props();

  /** A photo's status as the tile shows it: Go's, or "uploading". */
  type TileStatus = "in_immich" | "uploading" | "waiting" | "failed";

  // undefined while the first answer is pending, null without the card.
  // Raw: thousands of photos are replaced wholesale, never mutated.
  let list = $state.raw<List | null | undefined>(undefined);
  let loadError = $state("");
  // The photo the viewer shows (an index into list.photos).
  let openIndex = $state<number | null>(null);

  function message(err: unknown): string {
    return err instanceof Error ? err.message : String(err);
  }

  // ---- Loading --------------------------------------------------------------
  // Only the newest request's answer is shown.
  const RELOAD_EVERY = 2000; // ms, at most, while syncing
  let seq = 0;
  let lastLoad = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;

  function load(): void {
    clearTimeout(timer);
    timer = undefined;
    lastLoad = Date.now();
    const n = ++seq;
    backend.Gallery().then(
      (l) => {
        if (n !== seq) return;
        list = l;
        loadError = "";
      },
      (err) => {
        if (n !== seq) return;
        loadError = t("ui.load_failed", { detail: message(err) });
      },
    );
  }

  /** scheduleLoad reloads now, or once 2 s have passed since the last load. */
  function scheduleLoad(): void {
    if (timer) return;
    timer = setTimeout(load, Math.max(0, lastLoad + RELOAD_EVERY - Date.now()));
  }

  onDestroy(() => {
    clearTimeout(timer);
    seq++;
  });

  const code = $derived($status.code as string);
  const card = $derived(`${$appState.card?.uuid ?? ""}|${$appState.card?.mounted ?? false}`);
  // On entry, and whenever the card or the status changes.
  $effect(() => {
    void code;
    void card;
    untrack(load);
  });

  // While syncing, each finished file changes the statuses.
  const current = $derived(code === "syncing" ? ($status.progress?.current ?? 0) : -1);
  let lastCurrent = -1;
  $effect(() => {
    if (current === lastCurrent) return;
    const started = lastCurrent < 0;
    lastCurrent = current;
    // Entering "syncing" already reloaded (above).
    if (current >= 0 && !started) untrack(scheduleLoad);
  });

  // ---- Statuses -------------------------------------------------------------
  // The files uploading right now. The key is a string so that status
  // events with the same files do not touch the tiles.
  const inFlightKey = $derived(($status.progress?.inFlight ?? []).join("\n"));
  const inFlight = $derived(new Set(inFlightKey ? inFlightKey.split("\n") : []));

  function statusOf(p: Photo): TileStatus {
    if (p.status !== "in_immich" && inFlight.has(p.path)) return "uploading";
    if (p.status === "in_immich" || p.status === "failed") return p.status;
    return "waiting";
  }

  const statusLabel = (s: TileStatus) => t(`gallery.status_${s}`);

  /** The photos with the status each tile shows, for the viewer. */
  const photos = $derived.by(() => {
    if (!list) return [];
    return list.photos.map((p) => {
      const s = statusOf(p);
      return s === p.status ? p : { ...p, status: s };
    });
  });

  // ---- Days -----------------------------------------------------------------
  interface Group {
    key: number; // local midnight, ms
    day: string;
    items: { photo: Photo; index: number }[];
  }

  const groups = $derived.by(() => {
    const out: Group[] = [];
    if (!list) return out;
    const now = new Date();
    list.photos.forEach((photo, index) => {
      const at = parseTime(photo.takenAt);
      const d = new Date(at);
      const key = at > 0 ? new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() : 0;
      let last = out[out.length - 1];
      if (!last || last.key !== key) {
        last = { key, day: at > 0 ? formatDay(d, now) : "", items: [] };
        out.push(last);
      }
      last.items.push({ photo, index });
    });
    return out;
  });

  const total = $derived(list?.photos.length ?? 0);
  const done = $derived(list ? list.photos.reduce((n, p) => n + (p.status === "in_immich" ? 1 : 0), 0) : 0);
  const summary = $derived(t("gallery.summary", { count: formatNumber(total), done: formatNumber(done) }));

  // ---- Off-screen days ------------------------------------------------------
  // A day off screen is skipped by the renderer (content-visibility); its
  // estimated height keeps the scrollbar honest until it has been seen.
  const MIN_TILE = 116;
  const GAP = 6;
  const DAY_HEAD = 36;
  let width = $state(0);

  function dayHeight(count: number): number {
    const w = width || 700;
    const cols = Math.max(1, Math.floor((w + GAP) / (MIN_TILE + GAP)));
    const rows = Math.ceil(count / cols);
    const tile = ((w - GAP * (cols - 1)) / cols) * (2 / 3);
    return Math.round(DAY_HEAD + rows * tile + (rows - 1) * GAP);
  }

  const otherCard = $derived(code === "other_card_mounted");
  const params = $derived($status.params ?? {});
</script>

<div class="section">
  {#if loadError}
    <GlassCard tone="error">
      <p class="error-text" role="alert">{loadError}</p>
    </GlassCard>
  {/if}

  {#if list === undefined}
    {#if !loadError}
      <GlassCard>
        <div class="skeleton-lines" aria-hidden="true"><i></i><i></i></div>
      </GlassCard>
      <div class="grid skeleton" aria-hidden="true">
        {#each { length: 10 }, i (i)}<i></i>{/each}
      </div>
    {/if}
  {:else if list === null}
    <GlassCard tone={otherCard ? "warn" : "idle"}>
      <p class="empty-state">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons.gallery} /></svg>
        <span>
          {otherCard
            ? t("gallery.other_card", { mounted_name: params.mounted_name, selected_name: params.selected_name })
            : t("gallery.no_card")}
        </span>
      </p>
      {#if otherCard}
        <div class="actions">
          <Button onclick={() => onNavigate("cards")}>{t("action.open_cards")}</Button>
        </div>
      {/if}
    </GlassCard>
  {:else if total === 0}
    <GlassCard>
      <p class="empty-state">
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons.gallery} /></svg><span>{t("gallery.empty")}</span>
      </p>
      {#if list.other > 0}
        <p class="hint other">{t("gallery.hidden", { count: formatNumber(list.other) })}</p>
      {/if}
    </GlassCard>
  {:else}
    <GlassCard>
      <div class="head">
        <h2 class="name" title={list.cardName}>{list.cardName || t("cards.unnamed")}</h2>
        <span class="meta">{summary}</span>
      </div>
      <div
        class="bar"
        role="progressbar"
        aria-label={summary}
        aria-valuemin={0}
        aria-valuemax={total}
        aria-valuenow={done}
      >
        <i style:width="{(done / total) * 100}%"></i>
      </div>
      {#if list.other > 0}
        <p class="hint">{t("gallery.hidden", { count: formatNumber(list.other) })}</p>
      {/if}
    </GlassCard>

    <div class="days" bind:clientWidth={width}>
      {#each groups as group, g (group.key)}
        <section class="day" style:contain-intrinsic-block-size="auto {dayHeight(group.items.length)}px" aria-labelledby="gallery-day-{g}">
          <h3 id="gallery-day-{g}">
            <span class="day-name">{group.day}</span>
            <span class="count">{t("gallery.day_count", { count: formatNumber(group.items.length) })}</span>
          </h3>
          <ul class="grid">
            {#each group.items as item (item.photo.path)}
              {@const s = statusOf(item.photo)}
              <li>
                <button
                  type="button"
                  class="tile {s}"
                  title="{item.photo.name} — {statusLabel(s)}"
                  aria-label="{item.photo.name}, {statusLabel(s)}"
                  onclick={() => (openIndex = item.index)}
                >
                  <img src={item.photo.thumb} alt="" loading="lazy" decoding="async" draggable="false" />
                  <span class="badge {s}" aria-hidden="true">
                    {#if s === "in_immich"}
                      <svg viewBox="0 0 24 24"><path d={icons.check} /></svg>
                    {:else if s === "uploading"}
                      <svg viewBox="0 0 24 24"><path d="M12 19V5M6 11l6-6 6 6" /></svg>
                    {:else if s === "failed"}
                      <svg viewBox="0 0 24 24"><path d="M12 6v7.5M12 18h.01" /></svg>
                    {/if}
                  </span>
                </button>
              </li>
            {/each}
          </ul>
        </section>
      {/each}
    </div>
  {/if}
</div>

<style>
  /* ---- Header ---- */
  .head {
    display: flex;
    align-items: baseline;
    gap: 12px;
    min-width: 0;
  }
  .name {
    min-width: 0;
    margin: 0;
    overflow: hidden;
    font-size: 15px;
    font-weight: 650;
    letter-spacing: -0.01em;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .meta {
    margin-left: auto;
    flex-shrink: 0;
    color: var(--text-2);
    font-size: 12.5px;
    font-variant-numeric: tabular-nums;
  }
  .bar {
    height: 4px;
    margin-top: 10px;
    border-radius: 2px;
    background: rgba(255, 255, 255, 0.1);
    overflow: hidden;
  }
  .bar i {
    display: block;
    height: 100%;
    border-radius: 2px;
    background: var(--accent);
    transition: width 0.8s var(--ease-out);
  }
  .hint {
    margin-top: 9px;
  }
  .hint.other {
    margin: 10px 0 0 34px;
  }
  .actions {
    display: flex;
    margin: 12px 0 0 34px;
  }

  /* ---- Days ---- */
  .days {
    display: grid;
    gap: 6px;
    min-width: 0;
  }
  .day {
    content-visibility: auto;
  }
  h3 {
    display: flex;
    align-items: baseline;
    gap: 8px;
    margin: 0;
    padding: 10px 2px 8px;
    font-size: 13px;
    font-weight: 600;
  }
  .day-name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .day-name::first-letter {
    text-transform: uppercase;
  }
  .count {
    flex-shrink: 0;
    color: var(--text-3);
    font-size: 12px;
    font-weight: 400;
    font-variant-numeric: tabular-nums;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(116px, 1fr));
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .grid li {
    min-width: 0;
  }

  /* ---- Tiles ---- */
  .tile {
    position: relative;
    display: block;
    width: 100%;
    aspect-ratio: 3 / 2;
    padding: 0;
    border: 0;
    border-radius: 6px;
    background: rgba(255, 255, 255, 0.05);
    overflow: hidden;
    cursor: pointer;
    isolation: isolate;
  }
  .tile img {
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
    transition:
      transform 0.35s var(--ease-out),
      filter 0.25s;
  }
  .tile:hover img {
    transform: scale(1.04);
    filter: brightness(1.08);
  }
  .tile:active img {
    transform: scale(1.01);
  }
  .tile:focus-visible {
    outline-offset: 1px;
  }

  /* Failed: a red frame over the photo. */
  .tile.failed::before {
    content: "";
    position: absolute;
    inset: 0;
    z-index: 1;
    border-radius: inherit;
    box-shadow: inset 0 0 0 2px var(--red);
    pointer-events: none;
  }
  /* Uploading: a blue bar along the bottom with a light running over it. */
  .tile.uploading::after {
    content: "";
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 1;
    height: 3px;
    background:
      linear-gradient(90deg, transparent, rgba(255, 255, 255, 0.7), transparent) -100% 0 / 50% 100% no-repeat,
      var(--accent-2);
    box-shadow: 0 0 8px rgba(10, 132, 255, 0.8);
    animation: run 1.3s linear infinite;
  }
  @keyframes run {
    to {
      background-position: 200% 0, 0 0;
    }
  }

  .badge {
    position: absolute;
    right: 5px;
    bottom: 6px;
    z-index: 2;
    display: grid;
    place-items: center;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.55);
  }
  .badge svg {
    width: 11px;
    height: 11px;
    fill: none;
    stroke: #fff;
    stroke-width: 3.4;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  .badge.in_immich {
    background: var(--green);
  }
  .badge.uploading {
    background: var(--accent-2);
  }
  .badge.waiting {
    background: rgba(20, 20, 30, 0.7);
    border: 1.5px solid rgba(255, 255, 255, 0.8);
  }
  .badge.failed {
    background: var(--red);
  }

  /* ---- Loading ---- */
  .grid.skeleton {
    margin-top: 6px;
  }
  .grid.skeleton i {
    display: block;
    aspect-ratio: 3 / 2;
    border-radius: 6px;
    background: linear-gradient(90deg, rgba(255, 255, 255, 0.05), rgba(255, 255, 255, 0.1), rgba(255, 255, 255, 0.05));
    background-size: 200% 100%;
    animation: skeleton-wave 1.4s ease-in-out infinite;
  }
</style>
