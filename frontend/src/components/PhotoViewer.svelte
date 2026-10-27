<script lang="ts">
  // The photo viewer over the whole window, opened from the Gallery: one
  // photo at a time on black, whole (object-fit: contain), with its name,
  // when it was taken, its size and its status (dot and word) on top, a
  // Full Screen button and ✕. ‹ › at the sides and ← → step through all
  // the card's photos, across days (the button at either end is disabled);
  // a pill says which photo of its day this is ("17 of 48"), and a strip
  // of the neighbouring thumbnails below jumps to one on click. F (or the
  // button) puts the window into full screen; Esc leaves full screen
  // first and closes the viewer second, and closing it leaves the full
  // screen the viewer entered. The thumbnail stands in until the photo
  // has loaded; the photos either side are fetched ahead. The photos are
  // the Gallery's live list, so the status follows the sync; the viewer
  // keeps to its photo when the list reloads and closes once the photo is
  // gone (the card was removed).
  import { onDestroy, onMount, untrack } from "svelte";
  import { fade } from "svelte/transition";
  import type { Photo } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/gallery";
  import { appInfo, backend, parseTime } from "../lib/backend";
  import { dayOf } from "../lib/days";
  import { formatBytes, formatDay, formatNumber, formatTime, t } from "../lib/i18n";
  import { icons } from "../lib/icons";
  import { logsOpen, reducedMotion } from "../lib/stores";

  interface Props {
    /** The Gallery's photos, newest first, with their live statuses. */
    photos: Photo[];
    /** The photo to show, an index into photos. */
    index: number;
    /** Closes the viewer; path is the photo it showed last. */
    onclose: (path?: string) => void;
  }

  let { photos, index, onclose }: Props = $props();

  const ms = (n: number) => ($reducedMotion ? 0 : n);
  const STRIP = 6; // thumbnails either side of the current one

  // ---- Position -------------------------------------------------------------
  // The photo shown, by path, with the index it had: a reloaded list may
  // have moved it.
  let at = $state(untrack(() => ({ path: photos[index]?.path ?? "", i: index })));

  // A new index from the Gallery (its tile was activated) shows that photo.
  let lastIndex = untrack(() => index);
  $effect(() => {
    if (index === lastIndex) return;
    lastIndex = index;
    untrack(() => show(index));
  });

  const current = $derived(
    photos[at.i]?.path === at.path ? at.i : photos.findIndex((p) => p.path === at.path),
  );
  const photo = $derived<Photo | undefined>(photos[current]);

  // The photo is gone (the card was removed): nothing left to show.
  $effect(() => {
    if (current < 0) untrack(() => onclose());
  });

  function show(i: number): void {
    const p = photos[i];
    if (p) at = { path: p.path, i };
  }

  const hasPrev = $derived(current > 0);
  const hasNext = $derived(current >= 0 && current < photos.length - 1);
  const prev = () => hasPrev && show(current - 1);
  const next = () => hasNext && show(current + 1);

  /** The photo's place within its day (newest first, as in the Gallery) and the day's count. */
  const position = $derived.by(() => {
    if (current < 0) return { n: 0, count: 0 };
    const key = dayOf(photos[current].takenAt);
    let first = current;
    while (first > 0 && dayOf(photos[first - 1].takenAt) === key) first--;
    let last = current;
    while (last < photos.length - 1 && dayOf(photos[last + 1].takenAt) === key) last++;
    return { n: current - first + 1, count: last - first + 1 };
  });

  /** Up to STRIP photos either side; null where the list ends, so the current stays centred. */
  const strip = $derived.by(() => {
    const slots: ({ photo: Photo; i: number } | null)[] = [];
    for (let i = current - STRIP; i <= current + STRIP; i++) {
      slots.push(current >= 0 && i >= 0 && i < photos.length ? { photo: photos[i], i } : null);
    }
    return slots;
  });

  // ---- Details --------------------------------------------------------------
  const takenAt = $derived(photo ? parseTime(photo.takenAt) : 0);
  const when = $derived(takenAt > 0 ? `${formatDay(new Date(takenAt))}, ${formatTime(new Date(takenAt))}` : "");
  const statusLabel = $derived(photo ? t(`gallery.status_${photo.status}`) : "");

  // The full photo replaces the thumbnail once it has loaded.
  let loaded = $state("");

  // The photos either side, fetched ahead once the user lingers a moment
  // (holding an arrow key down does not fetch every photo it passes).
  // Keyed on the two URLs, not on photos: the list is a new array on every
  // reload during a sync, and the same neighbours need no new fetch.
  let ahead: HTMLImageElement[] = [];
  const aroundKey = $derived(
    [photos[current - 1]?.src, photos[current + 1]?.src].filter(Boolean).join("|"),
  );
  $effect(() => {
    const around = aroundKey ? aroundKey.split("|") : [];
    const timer = setTimeout(() => {
      ahead = around.map((src) => {
        const img = new Image();
        img.decoding = "async";
        img.src = src;
        return img;
      });
    }, 150);
    return () => clearTimeout(timer);
  });

  // ---- Full screen ----------------------------------------------------------
  let full = $state(false);
  let entered = false; // the viewer put the window into full screen

  async function refreshFull(): Promise<void> {
    try {
      full = await backend.IsFullscreen();
    } catch {
      // Not critical: the button's label may lag.
    }
  }

  async function toggleFullscreen(): Promise<void> {
    try {
      const on = !(await backend.IsFullscreen());
      await backend.SetFullscreen(on);
      full = on;
      entered = on;
    } catch {
      // The window stays as it is.
    }
  }

  /** Esc: out of full screen first, then out of the viewer. */
  async function escape(): Promise<void> {
    let on = false;
    try {
      on = await backend.IsFullscreen();
    } catch {
      // Treat as not full screen: close.
    }
    if (!on) {
      close();
      return;
    }
    try {
      await backend.SetFullscreen(false);
      full = false;
      entered = false;
    } catch {
      // Stay in full screen.
    }
  }

  function close(): void {
    onclose(photo?.path);
  }

  // Entering or leaving full screen resizes the window (also from the
  // green button or the system); the button's state follows.
  let resizeTimer: ReturnType<typeof setTimeout> | undefined;
  function resized(): void {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(refreshFull, 200);
  }

  let closeButton = $state<HTMLButtonElement>();
  onMount(() => {
    closeButton?.focus();
    refreshFull();
  });

  onDestroy(() => {
    clearTimeout(resizeTimer);
    ahead = [];
    if (entered) {
      backend
        .IsFullscreen()
        .then((on) => (on ? backend.SetFullscreen(false) : undefined))
        .catch(() => {});
    }
  });

  // ---- Keys -----------------------------------------------------------------
  function keydown(e: KeyboardEvent): void {
    // The log panel opens above the viewer and has the keys to itself.
    if ($logsOpen || e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === "ArrowLeft") {
      e.preventDefault();
      prev();
    } else if (e.key === "ArrowRight") {
      e.preventDefault();
      next();
    } else if (e.code === "KeyF" || e.key === "f" || e.key === "F") {
      e.preventDefault();
      if (!e.repeat) toggleFullscreen();
    } else if (e.key === "Escape") {
      e.preventDefault();
      if (!e.repeat) escape();
    }
  }

  /** Moves the viewer to <body>, clear of the page's transforms and stacking. */
  function portal(node: HTMLElement): () => void {
    document.body.append(node);
    return () => node.remove();
  }

  // macOS: the top 50 px of the window drag it (an invisible title bar
  // under the traffic lights) and do not take clicks; the bar sits below.
  const safeTop = $derived($appInfo?.os === "macOS" ? "50px" : "0px");
</script>

<svelte:window onkeydown={keydown} onresize={resized} />

<!-- The wrapper stays in the Gallery; the viewer itself lives in <body>. -->
<div class="host">
  <div
    class="viewer"
    role="dialog"
    aria-modal="true"
    aria-labelledby="viewer-name"
    style:--safe-top={safeTop}
    transition:fade={{ duration: ms(180) }}
    {@attach portal}
  >
    <div class="titlebar drag" aria-hidden="true"></div>

    <header class="top">
      <div class="info">
        <h2 id="viewer-name" class="name" title={photo?.name}>{photo?.name ?? ""}</h2>
        <span class="meta">
          {#if when}<span>{when}</span>{/if}
          {#if photo}<span>{formatBytes(photo.size)}</span>{/if}
        </span>
      </div>
      {#if photo}
        <span class="status {photo.status}">
          <span class="dot" aria-hidden="true">
            {#if photo.status === "in_immich"}
              <svg viewBox="0 0 24 24"><path d={icons.check} /></svg>
            {:else if photo.status === "uploading"}
              <svg viewBox="0 0 24 24"><path d={icons.arrowUp} /></svg>
            {:else if photo.status === "failed"}
              <svg viewBox="0 0 24 24"><path d={icons.exclaim} /></svg>
            {/if}
          </span>
          {statusLabel}
        </span>
      {/if}
      <button
        type="button"
        class="round"
        aria-label={full ? t("viewer.exit_fullscreen") : t("viewer.fullscreen")}
        title="{full ? t('viewer.exit_fullscreen') : t('viewer.fullscreen')} (F)"
        aria-pressed={full}
        onclick={toggleFullscreen}
      >
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d={full ? icons.exitFullscreen : icons.fullscreen} /></svg>
      </button>
      <button
        type="button"
        class="round"
        aria-label={t("viewer.close")}
        title="{t('viewer.close')} (Esc)"
        bind:this={closeButton}
        onclick={close}
      >
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons.close} /></svg>
      </button>
    </header>

    <div class="stage">
      {#if photo}
        {#key photo.path}
          {#if loaded !== photo.path}
            <img class="photo stand-in" src={photo.thumb} alt="" draggable="false" />
          {/if}
          <img
            class="photo"
            class:hidden={loaded !== photo.path}
            src={photo.src}
            alt={photo.name}
            draggable="false"
            onload={() => (loaded = photo.path)}
          />
        {/key}
      {/if}

      <button
        type="button"
        class="arrow prev"
        aria-label={t("viewer.prev")}
        title="{t('viewer.prev')} (←)"
        disabled={!hasPrev}
        onclick={prev}
      >
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons.chevronLeft} /></svg>
      </button>
      <button
        type="button"
        class="arrow next"
        aria-label={t("viewer.next")}
        title="{t('viewer.next')} (→)"
        disabled={!hasNext}
        onclick={next}
      >
        <svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons.chevronRight} /></svg>
      </button>

      {#if position.count > 0}
        <span class="position" aria-live="polite">
          {t("viewer.position", { n: formatNumber(position.n), count: formatNumber(position.count) })}
        </span>
      {/if}
    </div>

    <div class="strip">
      {#each strip as slot, k (k)}
        {#if slot}
          <button
            type="button"
            class="thumb"
            class:on={slot.i === current}
            aria-label={slot.photo.name}
            aria-current={slot.i === current ? "true" : undefined}
            title={slot.photo.name}
            onclick={() => show(slot.i)}
          >
            <img src={slot.photo.thumb} alt="" draggable="false" />
          </button>
        {:else}
          <span class="thumb empty" aria-hidden="true"></span>
        {/if}
      {/each}
    </div>
  </div>
</div>

<style>
  .host {
    display: contents;
  }

  .viewer {
    position: fixed;
    inset: 0;
    /* Over the whole window; the log panel (z-index 100) may open above. */
    z-index: 90;
    background: #000;
    color: var(--text);
    -webkit-user-select: none;
    user-select: none;
  }

  /* macOS: the strip under the traffic lights drags the window. */
  .titlebar {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: var(--safe-top);
  }

  /* ---- Top bar ---- */
  .top {
    position: absolute;
    top: var(--safe-top);
    left: 0;
    right: 0;
    z-index: 2;
    display: flex;
    align-items: center;
    gap: 10px;
    height: 48px;
    padding: 0 12px 0 16px;
  }
  .info {
    display: flex;
    align-items: baseline;
    gap: 10px;
    min-width: 0;
  }
  .name {
    min-width: 0;
    margin: 0;
    overflow: hidden;
    font-size: 13.5px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .meta {
    display: flex;
    gap: 6px;
    flex-shrink: 0;
    color: var(--text-3);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .meta span + span::before {
    content: "·";
    margin-right: 6px;
  }

  .status {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    flex-shrink: 0;
    margin-left: auto;
    margin-right: 6px;
    font-size: 12px;
    white-space: nowrap;
  }
  .status.in_immich {
    color: var(--green);
  }
  .status.uploading {
    color: var(--accent-3);
  }
  .status.waiting {
    color: var(--text-2);
  }
  .status.failed {
    color: var(--red);
  }
  .dot {
    display: grid;
    place-items: center;
    width: 15px;
    height: 15px;
    box-sizing: border-box;
    border-radius: 50%;
  }
  .dot svg {
    width: 10px;
    height: 10px;
    fill: none;
    stroke: #fff;
    stroke-width: 3.4;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  .in_immich .dot {
    background: var(--green);
  }
  .uploading .dot {
    background: var(--accent-2);
  }
  .waiting .dot {
    border: 1.5px solid rgba(255, 255, 255, 0.8);
  }
  .failed .dot {
    background: var(--red);
  }

  .round {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    flex-shrink: 0;
    padding: 0;
    border: 0;
    border-radius: 50%;
    background: rgba(255, 255, 255, 0.12);
    color: var(--text-2);
    cursor: pointer;
    transition:
      color 0.2s,
      background-color 0.2s;
  }
  .round:hover {
    background: rgba(255, 255, 255, 0.2);
    color: var(--text);
  }
  .round svg {
    width: 14px;
    height: 14px;
    fill: none;
    stroke: currentColor;
    stroke-width: 2.4;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  /* ---- Photo ---- */
  .stage {
    position: absolute;
    top: calc(var(--safe-top) + 48px);
    left: 0;
    right: 0;
    bottom: 64px;
  }
  .photo {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: contain;
  }
  .photo.hidden {
    visibility: hidden;
  }
  .photo.stand-in {
    filter: blur(6px);
    transform: scale(1.01);
  }

  .arrow {
    position: absolute;
    top: 50%;
    z-index: 1;
    display: grid;
    place-items: center;
    width: 40px;
    height: 40px;
    padding: 0;
    border: 0;
    border-radius: 50%;
    background: rgba(0, 0, 0, 0.45);
    box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.14);
    color: var(--text);
    cursor: pointer;
    transform: translateY(-50%);
    transition:
      opacity 0.2s,
      background-color 0.2s;
  }
  .arrow.prev {
    left: 14px;
  }
  .arrow.next {
    right: 14px;
  }
  .arrow:hover:not(:disabled) {
    background: rgba(255, 255, 255, 0.2);
  }
  .arrow:disabled {
    opacity: 0.25;
    cursor: default;
  }
  .arrow svg {
    width: 20px;
    height: 20px;
    fill: none;
    stroke: currentColor;
    stroke-width: 2.2;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  .position {
    position: absolute;
    left: 50%;
    bottom: 8px;
    z-index: 1;
    padding: 2px 9px;
    border-radius: 10px;
    background: rgba(0, 0, 0, 0.55);
    color: var(--text-2);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
    transform: translateX(-50%);
  }

  /* ---- Strip ---- */
  .strip {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 5px;
    height: 64px;
    overflow: hidden;
  }
  .thumb {
    flex-shrink: 0;
    width: 50px;
    height: 34px;
    padding: 0;
    border: 0;
    border-radius: 4px;
    background: rgba(255, 255, 255, 0.06);
    overflow: hidden;
    opacity: 0.5;
    cursor: pointer;
    transition: opacity 0.2s;
  }
  .thumb:hover {
    opacity: 0.85;
  }
  .thumb.on {
    opacity: 1;
    outline: 2px solid #fff;
    outline-offset: 1px;
  }
  .thumb.empty {
    background: none;
    cursor: default;
  }
  .thumb img {
    display: block;
    width: 100%;
    height: 100%;
    object-fit: cover;
  }
</style>
