<script lang="ts">
  // A glowing progress bar with a light running along the fill. A null
  // value shows an indeterminate bar (e.g. while scanning).
  interface Props {
    value: number | null;
    label: string;
  }

  let { value, label }: Props = $props();

  const pct = $derived(
    value === null
      ? null
      : Math.round(Math.min(1, Math.max(0, value)) * 1000) / 10,
  );
</script>

<div
  class="track"
  role="progressbar"
  aria-label={label}
  aria-valuemin={0}
  aria-valuemax={100}
  aria-valuenow={pct ?? undefined}
>
  {#if pct === null}
    <div class="fill indeterminate"></div>
  {:else}
    <div class="fill" style:width="{Math.max(pct, 1.5)}%"></div>
  {/if}
</div>

<style>
  .track {
    height: 6px;
    border-radius: 3px;
    background: rgba(255, 255, 255, 0.1);
    overflow: hidden;
  }
  .fill {
    position: relative;
    height: 100%;
    border-radius: 3px;
    background: linear-gradient(90deg, var(--accent-1), var(--accent-3));
    box-shadow: var(--glow-progress);
    transition: width 0.8s var(--ease-out);
    overflow: hidden;
  }
  .fill::after {
    content: "";
    position: absolute;
    inset: 0;
    background: linear-gradient(
      90deg,
      transparent,
      rgba(255, 255, 255, 0.55),
      transparent
    );
    animation: shimmer 1.4s infinite;
  }
  @keyframes shimmer {
    from {
      transform: translateX(-100%);
    }
    to {
      transform: translateX(100%);
    }
  }

  .indeterminate {
    width: 35%;
    animation: slide 1.6s var(--ease-out) infinite;
  }
  @keyframes slide {
    from {
      transform: translateX(-100%);
    }
    to {
      transform: translateX(290%);
    }
  }
</style>
