<script lang="ts">
  // The Logs section: the end of app.log in a monospace view, scrolled to
  // the newest line, with Refresh, Copy (to attach the log to an issue)
  // and Show in Finder. Warning and error lines are tinted.
  import { onDestroy, onMount, tick } from "svelte";
  import { fade } from "svelte/transition";
  import Button from "../components/Button.svelte";
  import GlassCard from "../components/GlassCard.svelte";
  import { backend } from "../lib/backend";
  import { formatNumber, t } from "../lib/i18n";
  import { icons } from "../lib/icons";
  import { reducedMotion } from "../lib/stores";

  const LINES = 300;
  const ms = (n: number) => ($reducedMotion ? 0 : n);

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

  onMount(() => {
    load();
  });

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

<div class="section">
  <GlassCard padding="14px 16px 16px">
    <div class="toolbar">
      <span class="eyebrow caption">{t("logs.caption", { count: formatNumber(LINES) })}</span>
      <div class="buttons">
        <span class="copied" aria-live="polite">
          {#if copied}
            <span class="copied-text" transition:fade={{ duration: ms(180) }}>
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons.check} /></svg>{t("logs.copied")}
            </span>
          {/if}
        </span>
        <Button onclick={load}>{t("logs.refresh")}</Button>
        <Button disabled={!text} onclick={copy}>{t("logs.copy")}</Button>
        <Button onclick={reveal}>{t("logs.reveal")}</Button>
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
      <pre class="log" bind:this={view} tabindex="0" aria-label={t("logs.label")}>{#each lines as line, i (i)}<span class="l {levelOf(line)}">{line}
</span>{/each}</pre>
    {/if}

    {#if actionError}
      <p class="error-text" role="alert">{actionError}</p>
    {/if}
  </GlassCard>
</div>

<style>
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 8px 12px;
    margin-bottom: 12px;
  }
  .caption {
    margin: 0;
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
    height: max(220px, calc(100vh - 210px));
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
      linear-gradient(90deg, rgba(255, 255, 255, 0.02), rgba(255, 255, 255, 0.06), rgba(255, 255, 255, 0.02)) 0 0 / 200% 100%,
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
