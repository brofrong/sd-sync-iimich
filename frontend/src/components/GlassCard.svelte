<script lang="ts">
  // A frosted-glass card: blurred and tinted over whatever lies behind it,
  // with a grain layer and (unless `sheen` is off) a soft highlight that
  // follows the pointer. The
  // tone recolours the tint, border and glow with a crossfade; bumping
  // `shake` makes the card nudge once (used when an error appears).
  import type { Snippet } from "svelte";
  import { get } from "svelte/store";
  import type { Tone } from "../lib/stores";
  import { reducedMotion } from "../lib/stores";

  interface Props {
    tone?: Tone;
    shake?: number;
    padding?: string;
    /** The highlight under the pointer. */
    sheen?: boolean;
    children: Snippet;
  }

  let { tone = "idle", shake = 0, padding = "16px 18px", sheen = true, children }: Props = $props();

  let el: HTMLDivElement;
  const tones: Tone[] = ["idle", "accent", "error", "warn", "success"];

  $effect(() => {
    if (!shake || !el || get(reducedMotion)) return;
    el.animate(
      [
        { transform: "translateX(0)" },
        { transform: "translateX(-5px)", offset: 0.2 },
        { transform: "translateX(5px)", offset: 0.4 },
        { transform: "translateX(-3px)", offset: 0.6 },
        { transform: "translateX(2px)", offset: 0.8 },
        { transform: "translateX(0)" },
      ],
      // After the new state has started to fly in.
      { duration: 500, delay: 200, easing: "ease-out" },
    );
  });

  function track(e: PointerEvent): void {
    const r = el.getBoundingClientRect();
    el.style.setProperty("--mx", `${e.clientX - r.left}px`);
    el.style.setProperty("--my", `${e.clientY - r.top}px`);
  }
</script>

<div class="glass noise tone-{tone}" style:padding bind:this={el} onpointermove={sheen ? track : undefined} role="presentation">
  <div class="tints" aria-hidden="true">
    {#each tones as t (t)}
      <i class="tint {t}" class:on={t === tone}></i>
    {/each}
  </div>
  {#if sheen}<div class="sheen" aria-hidden="true"></div>{/if}
  <div class="content">{@render children()}</div>
</div>

<style>
  .glass {
    position: relative;
    border-radius: var(--radius-l);
    background: var(--glass);
    border: 1px solid var(--glass-border);
    box-shadow: var(--glass-shadow);
    backdrop-filter: var(--glass-blur);
    -webkit-backdrop-filter: var(--glass-blur);
    overflow: hidden;
    isolation: isolate;
    transition:
      border-color 0.6s,
      box-shadow 0.6s;
  }

  .tints,
  .tint,
  .sheen {
    position: absolute;
    inset: 0;
    border-radius: inherit;
    pointer-events: none;
  }
  .tints {
    z-index: -1;
  }
  .tint {
    opacity: 0;
    transition: opacity 0.6s;
  }
  .tint.on {
    opacity: 1;
  }
  .tint.idle {
    background: linear-gradient(150deg, rgba(120, 90, 255, 0.08), transparent 70%);
  }
  .tint.accent {
    background: linear-gradient(150deg, rgba(123, 92, 255, 0.24), rgba(10, 132, 255, 0.06) 75%);
  }
  .tint.error {
    background: linear-gradient(150deg, rgba(255, 69, 58, 0.26), rgba(40, 15, 20, 0.45));
  }
  .tint.warn {
    background: linear-gradient(150deg, rgba(255, 179, 64, 0.2), rgba(40, 28, 10, 0.4));
  }
  .tint.success {
    background: linear-gradient(150deg, rgba(48, 209, 88, 0.2), rgba(15, 35, 20, 0.45));
  }

  .tone-accent {
    border-color: rgba(170, 150, 255, 0.34);
  }
  .tone-error {
    border-color: rgba(255, 100, 100, 0.45);
    box-shadow: 0 0 28px rgba(255, 69, 58, 0.26), var(--glass-shadow);
  }
  .tone-warn {
    border-color: rgba(255, 179, 64, 0.38);
    box-shadow: 0 0 26px rgba(255, 179, 64, 0.16), var(--glass-shadow);
  }
  .tone-success {
    border-color: rgba(48, 209, 88, 0.4);
    box-shadow: 0 0 28px rgba(48, 209, 88, 0.22), var(--glass-shadow);
  }

  /* A soft light under the pointer, as if the glass caught it. */
  .sheen {
    z-index: -1;
    opacity: 0;
    background: radial-gradient(
      260px circle at var(--mx, 50%) var(--my, 0%),
      rgba(255, 255, 255, 0.07),
      transparent 70%
    );
    transition: opacity 0.4s;
  }
  .glass:hover .sheen {
    opacity: 1;
  }

  .content {
    position: relative;
  }
</style>
