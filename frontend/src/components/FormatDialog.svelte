<script lang="ts">
  // The warning before formatting the card by hand: a red dialog over the
  // whole window. Its erase button only works once the countdown from
  // CONFIRM_SECONDS has reached 0, each time the dialog opens: erasing the
  // card cannot be undone. Cancel, Escape and a click outside close it.
  import { onDestroy } from "svelte";
  import { fade, scale } from "svelte/transition";

  import { formatBytes, formatNumber, t } from "../lib/i18n";
  import { icons } from "../lib/icons";
  import { reducedMotion } from "../lib/stores";
  import Button from "./Button.svelte";

  interface Props {
    name: string;
    sizeBytes: number;
    onconfirm: () => unknown;
    oncancel: () => void;
  }

  let { name, sizeBytes, onconfirm, oncancel }: Props = $props();

  const CONFIRM_SECONDS = 10;
  const ms = (n: number) => ($reducedMotion ? 0 : n);

  let wait = $state(CONFIRM_SECONDS);
  const timer = setInterval(() => {
    wait--;
    if (wait <= 0) {
      wait = 0;
      clearInterval(timer);
    }
  }, 1000);
  onDestroy(() => clearInterval(timer));

  let cancelButton = $state<HTMLDivElement>();
  $effect(() => {
    // Cancel has the focus: Enter must not erase the card.
    cancelButton?.querySelector("button")?.focus();
  });

  function keydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      oncancel();
    }
  }

  function confirm(): unknown {
    if (wait > 0) return;
    return onconfirm();
  }
</script>

<svelte:window onkeydown={keydown} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions (Escape closes it, see keydown) -->
<div
  class="backdrop"
  transition:fade={{ duration: ms(180) }}
  onclick={(e) => e.target === e.currentTarget && oncancel()}
>
  <div
    class="dialog"
    role="alertdialog"
    aria-modal="true"
    aria-labelledby="format-title"
    aria-describedby="format-body"
    transition:scale={{ start: 0.94, duration: ms(220) }}
  >
    <div class="icon" aria-hidden="true">
      <svg viewBox="0 0 24 24"><path d={icons.warning} /></svg>
    </div>
    <h2 id="format-title">{t("format_dialog.title", { name })}</h2>
    {#if sizeBytes > 0}
      <p class="size">
        {t("format_dialog.size", { size: formatBytes(sizeBytes) })}
      </p>
    {/if}

    <div id="format-body" class="body">
      <p class="erase"><strong>{t("format_dialog.erase")}</strong></p>
      <p>{t("format_dialog.verify")}</p>
      <p>{t("format_dialog.outside")}</p>
    </div>

    <div class="buttons">
      <div bind:this={cancelButton}>
        <Button onclick={oncancel}>{t("format_dialog.cancel")}</Button>
      </div>
      <Button variant="danger" disabled={wait > 0} onclick={confirm}>
        <!-- The widest label (the countdown's start) reserves the width. -->
        <span class="reserve">
          <span class="slot" aria-hidden="true"
            >{t("format_dialog.confirm_countdown", {
              seconds: formatNumber(CONFIRM_SECONDS),
            })}</span
          >
          <span>
            {wait > 0
              ? t("format_dialog.confirm_countdown", {
                  seconds: formatNumber(wait),
                })
              : t("format_dialog.confirm")}
          </span>
        </span>
      </Button>
    </div>
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
    background: rgba(20, 4, 6, 0.72);
    backdrop-filter: blur(6px);
    -webkit-backdrop-filter: blur(6px);
  }

  .dialog {
    display: grid;
    justify-items: center;
    gap: 10px;
    width: min(460px, 100%);
    max-height: 100%;
    overflow: auto;
    padding: 26px 26px 22px;
    border: 2px solid rgba(255, 69, 58, 0.75);
    border-radius: var(--radius-l);
    background: linear-gradient(160deg, #3a1114, #1c0c0f 70%);
    box-shadow:
      0 0 0 6px rgba(255, 69, 58, 0.12),
      0 24px 60px rgba(0, 0, 0, 0.6);
    text-align: center;
  }

  .icon {
    display: grid;
    place-items: center;
    width: 64px;
    height: 64px;
    border-radius: 50%;
    background: rgba(255, 69, 58, 0.18);
  }
  .icon svg {
    width: 38px;
    height: 38px;
    fill: none;
    stroke: var(--red);
    stroke-width: 1.9;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  h2 {
    margin: 4px 0 0;
    color: #fff;
    font-size: 20px;
    font-weight: 700;
    line-height: 1.25;
    overflow-wrap: anywhere;
  }
  .size {
    margin: -4px 0 0;
    color: var(--text-3);
    font-size: 12px;
  }

  .body {
    display: grid;
    gap: 8px;
    margin-top: 4px;
    padding: 12px 14px;
    border: 1px solid rgba(255, 69, 58, 0.35);
    border-radius: var(--radius-m);
    background: rgba(255, 69, 58, 0.08);
    text-align: left;
  }
  .body p {
    margin: 0;
    color: var(--text-2);
    font-size: 12.5px;
    line-height: 1.5;
  }
  .body .erase {
    color: #ff8a82;
    font-size: 14px;
  }

  .buttons {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 10px;
    margin-top: 8px;
  }

  .reserve {
    display: inline-grid;
    justify-items: center;
    font-variant-numeric: tabular-nums;
  }
  .reserve > span {
    grid-area: 1 / 1;
  }
  .reserve .slot {
    visibility: hidden;
  }
</style>
