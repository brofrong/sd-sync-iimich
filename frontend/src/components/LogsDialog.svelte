<script lang="ts">
  // The log panel over the whole window (opened from Settings, or by a
  // status's "Open Logs"): the end of app.log in a monospace view,
  // scrolled to the newest line, with Refresh, Copy (to attach the log to
  // an issue) and Show in Finder (File Explorer on Windows; on Linux the
  // file manager opens the log's folder). Warning and error lines are
  // tinted. The close button, Escape and a click outside close it.
  import { onDestroy, onMount, tick } from "svelte";
  import { fade, scale } from "svelte/transition";

  import { appInfo, backend } from "../lib/backend";
  import { formatNumber, t } from "../lib/i18n";
  import { icons } from "../lib/icons";
  import { reducedMotion } from "../lib/stores";
  import Button from "./Button.svelte";

  interface Props {
    onclose: () => void;
  }

  let { onclose }: Props = $props();

  const LINES = 300;
  const ms = (n: number) => ($reducedMotion ? 0 : n);

  const revealLabel = $derived(
    $appInfo?.os === "macOS"
      ? t("logs.reveal_mac")
      : $appInfo?.os === "Windows"
        ? t("logs.reveal_windows")
        : t("logs.reveal"),
  );

  let text = $state<string | null>(null);
  let loadError = $state("");
  let actionError = $state("");
  let copied = $state(false);
  let copiedTimer: ReturnType<typeof setTimeout> | undefined;
  let view = $state<HTMLElement>();

  onDestroy(() => clearTimeout(copiedTimer));

  function message(err: unknown): string {
    return err instanceof Error ? err.message : String(err);
  }

  async function load(): Promise<void> {
    try {
      text = await backend.LogTail(LINES);
      loadError = "";
    } catch (err) {
      loadError = t("ui.load_failed", { detail: message(err) });
      return;
    }
    await tick();
    if (view) view.scrollTop = view.scrollHeight;
  }

  let closeButton = $state<HTMLButtonElement>();
  onMount(() => {
    closeButton?.focus();
    load();
  });

  function keydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      onclose();
    }
  }

  const lines = $derived(text ? text.split("\n") : []);

  function levelOf(line: string): string {
    const m = /\blevel=(WARN|ERROR|DEBUG)\b/.exec(line);
    return m ? m[1].toLowerCase() : "";
  }

  /** Copies text, falling back to a selection copy where the async clipboard is unavailable. */
  async function writeClipboard(value: string): Promise<void> {
    try {
      await navigator.clipboard.writeText(value);
      return;
    } catch {
      // Not a secure context or no permission: try the legacy path.
    }
    const area = document.createElement("textarea");
    area.value = value;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.append(area);
    area.select();
    const ok = document.execCommand("copy");
    area.remove();
    if (!ok) throw new Error("copy failed");
  }

  async function copy(): Promise<void> {
    actionError = "";
    try {
      await writeClipboard(text ?? "");
    } catch {
      actionError = t("logs.copy_failed");
      return;
    }
    copied = true;
    clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => (copied = false), 1800);
  }

  async function reveal(): Promise<void> {
    actionError = "";
    try {
      await backend.RevealLog();
    } catch (err) {
      actionError = t("ui.action_failed", { detail: message(err) });
    }
  }
</script>

<svelte:window onkeydown={keydown} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions (Escape closes it, see keydown) -->
<div
  class="backdrop"
  transition:fade={{ duration: ms(180) }}
  onclick={(e) => e.target === e.currentTarget && onclose()}
>
  <div
    class="dialog"
    role="dialog"
    aria-modal="true"
    aria-labelledby="logs-title"
    transition:scale={{ start: 0.96, duration: ms(220) }}
  >
    <header class="head">
      <div>
        <h2 id="logs-title">{t("logs.label")}</h2>
        <p class="caption">
          {t("logs.caption", { count: formatNumber(LINES) })}
        </p>
      </div>
      <button
        type="button"
        class="close"
        aria-label={t("logs.close")}
        title={t("logs.close")}
        bind:this={closeButton}
        onclick={onclose}
      >
        <svg viewBox="0 0 24 24" aria-hidden="true"
          ><path d={icons.close} /></svg
        >
      </button>
    </header>

    <div class="toolbar">
      <div class="buttons">
        <span class="copied" aria-live="polite">
          {#if copied}
            <span class="copied-text" transition:fade={{ duration: ms(180) }}>
              <svg viewBox="0 0 24 24" aria-hidden="true"
                ><path d={icons.check} /></svg
              >{t("logs.copied")}
            </span>
          {/if}
        </span>
        <Button onclick={load}>{t("logs.refresh")}</Button>
        <Button disabled={!text} onclick={copy}>{t("logs.copy")}</Button>
        <Button onclick={reveal}>{revealLabel}</Button>
      </div>
    </div>

    {#if loadError}
      <p class="error-text" role="alert">{loadError}</p>
    {:else if text === null}
      <div class="log loading" aria-hidden="true"></div>
    {:else if lines.length === 0}
      <div class="log empty"><p>{t("logs.empty")}</p></div>
    {:else}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex: the log scrolls; keyboard users need to reach it -->
      <pre
        class="log"
        bind:this={view}
        tabindex="0"
        aria-label={t("logs.label")}>{#each lines as line, i (i)}<span
          class="l {levelOf(line)}"
          >{line}
</span>{/each}</pre>
    {/if}

    {#if actionError}
      <p class="error-text" role="alert">{actionError}</p>
    {/if}
  </div>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 100;
    display: grid;
    place-items: center;
    padding: 24px;
    background: rgba(6, 4, 14, 0.62);
    backdrop-filter: blur(6px);
    -webkit-backdrop-filter: blur(6px);
  }

  .dialog {
    display: flex;
    flex-direction: column;
    width: min(860px, 100%);
    /* The window's height less the backdrop's padding: the log scrolls
       inside, it does not grow the panel. */
    height: calc(100vh - 48px);
    padding: 16px 18px 18px;
    border: 1px solid var(--glass-border);
    border-radius: var(--radius-l);
    background: linear-gradient(160deg, #221d3a, var(--bg-main) 70%);
    box-shadow: 0 24px 60px rgba(0, 0, 0, 0.6);
  }

  .head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 12px;
  }
  h2 {
    margin: 0;
    font-size: 17px;
    font-weight: 700;
    letter-spacing: -0.01em;
  }
  .caption {
    margin: 2px 0 0;
    color: var(--text-3);
    font-size: 12px;
  }
  .close {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    flex-shrink: 0;
    border: 0;
    border-radius: 50%;
    background: rgba(255, 255, 255, 0.08);
    color: var(--text-2);
    cursor: pointer;
    transition:
      color 0.2s,
      background-color 0.2s;
  }
  .close:hover {
    background: rgba(255, 255, 255, 0.16);
    color: var(--text);
  }
  .close svg {
    width: 14px;
    height: 14px;
    fill: none;
    stroke: currentColor;
    stroke-width: 2.4;
    stroke-linecap: round;
  }

  .toolbar {
    margin-bottom: 12px;
  }
  .buttons {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
  }
  .copied {
    display: inline-flex;
  }
  .copied-text {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    margin-right: 2px;
    color: var(--green);
    font-size: 12.5px;
  }
  .copied-text svg {
    width: 14px;
    height: 14px;
    fill: none;
    stroke: currentColor;
    stroke-width: 2.6;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  .log {
    flex: 1;
    min-height: 120px;
    margin: 0;
    padding: 10px 12px;
    overflow: auto;
    border-radius: var(--radius-m);
    border: 1px solid rgba(255, 255, 255, 0.08);
    background: rgba(8, 6, 16, 0.55);
    box-shadow: inset 0 1px 3px rgba(0, 0, 0, 0.35);
    color: rgba(255, 255, 255, 0.78);
    font-family: var(--mono);
    font-size: 11px;
    line-height: 1.6;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    -webkit-user-select: text;
    user-select: text;
    cursor: text;
  }
  .log:focus-visible {
    outline-offset: 1px;
  }
  /* One block per line; wrapped continuations are indented. */
  .l {
    display: block;
    padding-left: 2ch;
    text-indent: -2ch;
  }
  .l.warn {
    color: #ffd18a;
  }
  .l.error {
    color: #ff9a90;
  }
  .l.debug {
    color: var(--text-3);
  }

  .log.loading {
    background:
      linear-gradient(
          90deg,
          rgba(255, 255, 255, 0.02),
          rgba(255, 255, 255, 0.06),
          rgba(255, 255, 255, 0.02)
        )
        0 0 / 200% 100%,
      rgba(8, 6, 16, 0.55);
    animation: wave 1.4s ease-in-out infinite;
  }
  @keyframes wave {
    from {
      background-position: 100% 0;
    }
    to {
      background-position: -100% 0;
    }
  }
  .log.empty {
    display: grid;
    place-items: center;
    color: var(--text-3);
    font-family: var(--font);
    font-size: 12.5px;
    cursor: default;
  }
</style>
