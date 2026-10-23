<script lang="ts">
  // The plain dark sidebar. A glowing pill slides with a spring to the
  // selected section; the Status item carries a dot coloured by the
  // current status so a problem is visible from any section.
  import { t } from "../lib/i18n";
  import { icons } from "../lib/icons";
  import { sections, toneFor } from "../lib/stores";
  import { appState, status } from "../lib/backend";

  interface Props {
    current: string;
    onSelect: (id: string) => void;
  }

  let { current, onSelect }: Props = $props();

  const index = $derived(Math.max(0, sections.indexOf(current as (typeof sections)[number])));
  const tone = $derived(toneFor($status.code));

  let buttons = $state<HTMLButtonElement[]>([]);

  function onKey(e: KeyboardEvent, i: number): void {
    let next = -1;
    if (e.key === "ArrowDown") next = (i + 1) % sections.length;
    else if (e.key === "ArrowUp") next = (i - 1 + sections.length) % sections.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = sections.length - 1;
    if (next < 0) return;
    e.preventDefault();
    onSelect(sections[next]);
    buttons[next]?.focus();
  }
</script>

<aside class="sidebar">
  <div class="titlebar drag" aria-hidden="true"></div>
  <nav aria-label={t("sidebar.label")}>
    <span class="pill" style:transform="translateY({index * 34}px)" aria-hidden="true"></span>
    {#each sections as id, i (id)}
      <button
        type="button"
        class="item"
        class:on={id === current}
        aria-current={id === current ? "page" : undefined}
        tabindex={id === current ? 0 : -1}
        bind:this={buttons[i]}
        onclick={() => onSelect(id)}
        onkeydown={(e) => onKey(e, i)}
      >
        <svg class="icon" viewBox="0 0 24 24" aria-hidden="true"><path d={icons[id]} /></svg>
        <span class="name">{t(`section.${id}`)}</span>
        {#if id === "status" && tone !== "idle"}
          <span class="dot {tone}" aria-hidden="true"></span>
        {/if}
      </button>
    {/each}
  </nav>
  {#if $appState.version}
    <div class="version">{$appState.version}</div>
  {/if}
</aside>

<style>
  .sidebar {
    position: relative;
    display: flex;
    flex-direction: column;
    width: 196px;
    flex-shrink: 0;
    padding: 0 10px 12px;
    background: var(--bg-sidebar);
    border-right: 1px solid var(--line);
  }

  /* Clear of the traffic lights; drags the window. */
  .titlebar {
    height: 52px;
    flex-shrink: 0;
  }

  nav {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .pill {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 32px;
    border-radius: var(--radius-m);
    background: linear-gradient(90deg, rgba(140, 110, 255, 0.5), rgba(255, 255, 255, 0.06));
    box-shadow: var(--glow-pill);
    transition: transform 0.45s var(--spring);
    pointer-events: none;
  }

  .item {
    position: relative;
    display: flex;
    align-items: center;
    gap: 9px;
    height: 32px;
    padding: 0 10px;
    border: 0;
    border-radius: var(--radius-m);
    background: transparent;
    color: var(--text-2);
    font-size: 13px;
    text-align: left;
    cursor: pointer;
    transition: color 0.25s, background-color 0.2s;
  }
  .item:hover:not(.on) {
    background: rgba(255, 255, 255, 0.04);
    color: var(--text);
  }
  .item.on {
    color: #fff;
  }
  .item:focus-visible {
    outline-offset: -2px;
  }

  .icon {
    width: 16px;
    height: 16px;
    flex-shrink: 0;
    fill: none;
    stroke: currentColor;
    stroke-width: 1.7;
    stroke-linecap: round;
    stroke-linejoin: round;
    opacity: 0.85;
  }
  .name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .dot.accent {
    background: var(--accent-3);
    box-shadow: 0 0 8px var(--accent-3);
    animation: breathe 1.6s ease-in-out infinite;
  }
  .dot.error {
    background: var(--red);
    box-shadow: 0 0 8px var(--red);
    animation: breathe 1.2s ease-in-out infinite;
  }
  .dot.warn {
    background: var(--amber);
  }
  .dot.success {
    background: var(--green);
  }
  @keyframes breathe {
    50% {
      opacity: 0.45;
      transform: scale(0.8);
    }
  }

  .version {
    margin-top: auto;
    padding: 0 10px;
    color: var(--text-3);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
  }
</style>
