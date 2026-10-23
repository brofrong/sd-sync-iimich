<script lang="ts">
  // The status card: why sync is (or isn't) running and the one-click fix.
  // Title and detail come from status.<code>.title/detail; the buttons from
  // status.actions. While syncing it shows the current file, counts, bytes,
  // speed, time left and how many files were uploaded, were already in
  // Immich (duplicates) or failed. Before setup it becomes an onboarding step.
  import { untrack } from "svelte";
  import { fade, fly } from "svelte/transition";
  import type { Status } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/app";
  import type { StateView } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/gui";
  import { backend } from "../lib/backend";
  import { formatBytes, formatDuration, formatNumber, formatSpeed, t } from "../lib/i18n";
  import { icons } from "../lib/icons";
  import { errorCodes, reducedMotion, toneFor } from "../lib/stores";
  import Button from "./Button.svelte";
  import GlassCard from "./GlassCard.svelte";
  import ProgressBar from "./ProgressBar.svelte";

  interface Props {
    status: Status;
    view: StateView;
    onNavigate: (section: string) => void;
    /** Why files failed, one translated line per error kind (done_with_errors). */
    errorKinds?: string[];
  }

  let { status, view, onNavigate, errorKinds = [] }: Props = $props();

  const code = $derived(status.code as string);
  const tone = $derived(toneFor(code));
  const onboarding = $derived(code === "not_configured" || code === "no_card_selected");
  const ms = (n: number) => ($reducedMotion ? 0 : n);

  // Counts in params are formatted for the current language.
  const countParams = new Set(["uploaded", "errors"]);
  const params = $derived(
    Object.fromEntries(
      Object.entries(status.params ?? {}).map(([k, v]) => [
        k,
        countParams.has(k) && /^\d+$/.test(v ?? "") ? formatNumber(Number(v)) : (v ?? ""),
      ]),
    ),
  );

  const title = $derived(code ? t(`status.${code}.title`, params) : "");
  const detail = $derived(code ? t(`status.${code}.detail`, params) : "");

  const progress = $derived(status.progress);
  const ratio = $derived.by(() => {
    const p = progress;
    if (!p) return 0;
    if (p.bytesTotal > 0) return p.bytesDone / p.bytesTotal;
    return p.total > 0 ? p.current / p.total : 0;
  });

  // ---- Error nudge --------------------------------------------------------
  let shake = $state(0);
  let lastCode = "";
  $effect(() => {
    if (code !== lastCode && errorCodes.includes(code)) untrack(() => shake++);
    lastCode = code;
  });

  // ---- Actions --------------------------------------------------------------
  let actionError = $state("");
  $effect(() => {
    void code;
    actionError = "";
  });

  const navigateTo: Record<string, string> = {
    open_server: "server",
    open_cards: "cards",
    open_logs: "logs",
  };

  async function run(action: string): Promise<void> {
    if (navigateTo[action]) {
      onNavigate(navigateTo[action]);
      return;
    }
    actionError = "";
    try {
      switch (action) {
        case "retry":
          return await backend.Retry();
        case "stop":
          return await backend.StopSync();
        case "select_card":
          return await backend.SelectCard(status.params?.mounted_uuid ?? "");
        case "eject":
          return await backend.EjectCard();
        case "open_immich":
          return await backend.OpenImmich();
      }
    } catch (err) {
      actionError = t("ui.action_failed", { detail: err instanceof Error ? err.message : String(err) });
      throw err;
    }
  }

  // Stop is the escape hatch while syncing, not the thing to click.
  const isPrimary = (action: string, i: number) => i === 0 && action !== "stop";

  // ---- Onboarding -------------------------------------------------------------
  const serverReady = $derived(Boolean(view.server.url && view.server.hasKey));
  const steps = $derived([
    { label: t("onboarding.server"), done: serverReady, current: code === "not_configured" },
    { label: t("onboarding.card"), done: false, current: code === "no_card_selected" },
  ]);
  const stepNumber = $derived(code === "not_configured" ? 1 : 2);

  // ---- Height animation ---------------------------------------------------------
  let stackHeight = $state(0);
</script>

<GlassCard {tone} {shake} padding="0">
  <div class="body" style:height={stackHeight ? `${stackHeight}px` : "auto"}>
    <div class="stack" bind:offsetHeight={stackHeight} aria-live="polite" aria-busy={!code}>
      {#key code}
        <div class="state" in:fly={{ y: 10, duration: ms(350), delay: ms(120) }} out:fade={{ duration: ms(150) }}>
          {#if !code}
            <div class="skeleton" aria-hidden="true"><i></i><i></i><i class="short"></i></div>
          {:else}
            {#if onboarding}
              <div class="eyebrow">{t("onboarding.step", { current: stepNumber, total: 2 })}</div>
            {/if}

            <div class="head">
              <span class="glyph {tone}" aria-hidden="true">
                {#if tone === "accent"}
                  <span class="spin"></span>
                {:else if tone === "success"}
                  <svg class="check" viewBox="0 0 24 24">
                    <circle class="ring" cx="12" cy="12" r="10" />
                    <path class="tick" d="M7 12.5l3.2 3.2L17 9" />
                  </svg>
                {:else if tone === "error" || code === "done_with_errors"}
                  <svg class="line pop" viewBox="0 0 24 24"><path d={icons.warning} /></svg>
                {:else if code === "cancelled"}
                  <svg class="line" viewBox="0 0 24 24"><path d={icons.pause} /></svg>
                {:else if code === "not_configured"}
                  <svg class="line" viewBox="0 0 24 24"><path d={icons.server} /></svg>
                {:else}
                  <svg class="line" class:pulse={code === "waiting_for_card"} viewBox="0 0 24 24"><path d={icons.cards} /></svg>
                {/if}
              </span>
              <h2 class="title">{title}</h2>
            </div>

            {#if code === "syncing" && progress}
              <p class="detail file">
                <span class="filename">{t("progress.file", { file: progress.file })}</span>
                {#if progress.bytesPerSec > 0}
                  <span class="sep">·</span><span class="nowrap">{formatSpeed(progress.bytesPerSec)}</span>
                {/if}
                {#if progress.etaSeconds > 0}
                  <span class="sep">·</span><span class="nowrap">{t("progress.eta", { time: formatDuration(progress.etaSeconds) })}</span>
                {/if}
              </p>
              <ProgressBar value={ratio} label={title} />
              <div class="meta">
                <span>{t("progress.count", { current: formatNumber(progress.current), total: formatNumber(progress.total) })}</span>
                {#if progress.bytesTotal > 0}
                  <span>{t("progress.bytes", { done: formatBytes(progress.bytesDone), total: formatBytes(progress.bytesTotal) })}</span>
                {/if}
              </div>
              <div class="counts">
                <span class="uploaded"><i aria-hidden="true"></i>{t("progress.uploaded", { count: formatNumber(progress.uploaded) })}</span>
                <span class="duplicates"><i aria-hidden="true"></i>{t("progress.duplicates", { count: formatNumber(progress.duplicates) })}</span>
                <span class="errors" class:bad={progress.errors > 0}><i aria-hidden="true"></i>{t("progress.errors", { count: formatNumber(progress.errors) })}</span>
              </div>
            {:else}
              <p class="detail">{detail}</p>
              {#if code === "done_with_errors" && errorKinds.length}
                <ul class="kinds">
                  {#each errorKinds as kind, k (k)}<li>{kind}</li>{/each}
                </ul>
              {/if}
              {#if code === "scanning"}
                <ProgressBar value={null} label={title} />
              {/if}
            {/if}

            {#if onboarding}
              <ol class="steps">
                {#each steps as step, i (i)}
                  <li class:done={step.done} class:current={step.current}>
                    <span class="num" aria-hidden="true">
                      {#if step.done}
                        <svg viewBox="0 0 24 24"><path d="M6 12.5l4 4L18 8" /></svg>
                      {:else}{i + 1}{/if}
                    </span>
                    {step.label}
                  </li>
                {/each}
              </ol>
            {/if}

            {#if status.actions?.length}
              <div class="actions">
                {#each status.actions as action, i (action)}
                  <Button variant={isPrimary(action, i) ? "primary" : "secondary"} onclick={() => run(action)}>
                    {t(`action.${action}`)}
                  </Button>
                {/each}
              </div>
            {/if}
            {#if actionError}
              <p class="action-error" role="alert">{actionError}</p>
            {/if}
          {/if}
        </div>
      {/key}
    </div>
  </div>
</GlassCard>

<style>
  .body {
    transition: height 0.45s var(--spring);
  }
  .stack {
    display: grid;
  }
  .state {
    grid-area: 1 / 1;
    padding: 18px 20px 20px;
    min-width: 0;
  }

  .eyebrow {
    margin-bottom: 8px;
    color: var(--accent-soft);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
  }

  .head {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .title {
    margin: 0;
    font-size: 17px;
    font-weight: 650;
    letter-spacing: -0.01em;
  }

  .glyph {
    display: grid;
    place-items: center;
    width: 22px;
    height: 22px;
    flex-shrink: 0;
    color: var(--accent-soft);
  }
  .glyph.error {
    color: var(--red);
  }
  .glyph.warn {
    color: var(--amber);
  }
  .glyph.success {
    color: var(--green);
  }
  .line {
    width: 20px;
    height: 20px;
    fill: none;
    stroke: currentColor;
    stroke-width: 1.8;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  .pop {
    animation: pop 0.45s var(--spring) 0.15s both;
  }
  @keyframes pop {
    from {
      transform: scale(0.4);
      opacity: 0;
    }
  }
  .pulse {
    animation: pulse 2.4s ease-in-out infinite;
  }
  @keyframes pulse {
    50% {
      opacity: 0.45;
      transform: translateY(1px);
    }
  }

  .spin {
    width: 16px;
    height: 16px;
    border-radius: 50%;
    border: 2px solid rgba(255, 255, 255, 0.22);
    border-top-color: var(--accent-soft);
    border-right-color: var(--accent-3);
    animation: spin 0.9s linear infinite;
    filter: drop-shadow(0 0 4px rgba(123, 92, 255, 0.7));
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }

  /* The drawn check mark: the ring draws, then the tick. */
  .check {
    width: 22px;
    height: 22px;
    fill: none;
    stroke: currentColor;
    stroke-linecap: round;
    stroke-linejoin: round;
    filter: drop-shadow(0 0 5px rgba(48, 209, 88, 0.55));
  }
  .ring {
    stroke-width: 1.8;
    stroke-dasharray: 63;
    stroke-dashoffset: 63;
    opacity: 0.55;
    transform: rotate(-90deg);
    transform-origin: center;
    animation: draw 0.5s 0.1s ease-out forwards;
  }
  .tick {
    stroke-width: 2.6;
    stroke-dasharray: 16;
    stroke-dashoffset: 16;
    animation: draw 0.4s 0.5s ease-out forwards;
  }
  @keyframes draw {
    to {
      stroke-dashoffset: 0;
    }
  }

  .detail {
    margin: 8px 0 0;
    color: var(--text-2);
    font-size: 12.5px;
    line-height: 1.5;
    -webkit-user-select: text;
    user-select: text;
    overflow-wrap: anywhere;
  }
  .detail.file {
    display: flex;
    flex-wrap: wrap;
    gap: 0 6px;
    margin-bottom: 12px;
  }
  .filename {
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .sep {
    color: var(--text-3);
  }
  .nowrap {
    white-space: nowrap;
  }
  .detail + :global(.track) {
    margin-top: 14px;
  }

  .meta {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    margin-top: 7px;
    color: var(--text-3);
    font-size: 11.5px;
    font-variant-numeric: tabular-nums;
  }

  .counts {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 16px;
    margin-top: 10px;
    color: var(--text-2);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
  }
  .counts span {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .counts i {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: rgba(255, 255, 255, 0.3);
  }
  .counts .uploaded i {
    background: #34c759;
    box-shadow: 0 0 6px rgba(52, 199, 89, 0.6);
  }
  .counts .duplicates i {
    background: #64a8ff;
  }
  .counts .bad {
    color: #ff8a80;
  }
  .counts .bad i {
    background: #ff453a;
    box-shadow: 0 0 6px rgba(255, 69, 58, 0.6);
  }

  .steps {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 18px;
    margin: 14px 0 0;
    padding: 0;
    list-style: none;
    color: var(--text-3);
    font-size: 12.5px;
  }
  .steps li {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .steps li.current {
    color: var(--text);
  }
  .steps li.done {
    color: var(--text-2);
  }
  .num {
    display: grid;
    place-items: center;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    border: 1px solid rgba(255, 255, 255, 0.2);
    font-size: 11px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  .current .num {
    border-color: transparent;
    background: var(--accent);
    box-shadow: var(--glow);
    color: #fff;
  }
  .done .num {
    border-color: rgba(48, 209, 88, 0.5);
    color: var(--green);
  }
  .num svg {
    width: 12px;
    height: 12px;
    fill: none;
    stroke: currentColor;
    stroke-width: 3;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 16px;
  }
  .kinds {
    margin: 6px 0 0;
    padding: 0;
    list-style: none;
    color: rgba(255, 179, 64, 0.9);
    font-size: 12px;
    line-height: 1.6;
    font-variant-numeric: tabular-nums;
  }

  .action-error {
    margin: 10px 0 0;
    color: #ff8a80;
    font-size: 12px;
    -webkit-user-select: text;
    user-select: text;
  }

  .skeleton {
    display: grid;
    gap: 10px;
  }
  .skeleton i {
    display: block;
    height: 12px;
    border-radius: 6px;
    background: linear-gradient(90deg, rgba(255, 255, 255, 0.06), rgba(255, 255, 255, 0.12), rgba(255, 255, 255, 0.06));
    background-size: 200% 100%;
    animation: wave 1.4s ease-in-out infinite;
  }
  .skeleton i:first-child {
    width: 40%;
    height: 16px;
  }
  .skeleton i.short {
    width: 60%;
  }
  @keyframes wave {
    from {
      background-position: 100% 0;
    }
    to {
      background-position: -100% 0;
    }
  }
</style>
