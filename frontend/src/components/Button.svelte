<script lang="ts">
  // A push button. "primary" is the one glowing, accent-filled action of a
  // card; "secondary" is a quiet glass button. When onclick returns a
  // promise the button shows a busy state until it settles.
  import type { Snippet } from "svelte";

  interface Props {
    variant?: "primary" | "secondary";
    disabled?: boolean;
    onclick?: () => unknown;
    children: Snippet;
  }

  let { variant = "secondary", disabled = false, onclick, children }: Props = $props();

  let busy = $state(false);

  async function handle(): Promise<void> {
    if (busy || !onclick) return;
    const result = onclick();
    if (result instanceof Promise) {
      busy = true;
      try {
        await result;
      } catch {
        // The caller reports errors; the button only tracks the busy state.
      } finally {
        busy = false;
      }
    }
  }
</script>

<button
  type="button"
  class="btn {variant}"
  class:busy
  disabled={disabled || busy}
  aria-busy={busy}
  onclick={handle}
>
  {#if busy}<span class="spinner" aria-hidden="true"></span>{/if}
  <span class="label">{@render children()}</span>
</button>

<style>
  .btn {
    position: relative;
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 28px;
    padding: 0 13px;
    border-radius: var(--radius-s);
    border: 1px solid rgba(255, 255, 255, 0.1);
    background: rgba(255, 255, 255, 0.1);
    color: var(--text);
    font-size: 12.5px;
    font-weight: 500;
    white-space: nowrap;
    cursor: pointer;
    transition:
      transform 0.2s var(--spring),
      box-shadow 0.25s,
      background-color 0.2s,
      opacity 0.2s;
  }
  .btn:hover:not(:disabled) {
    background-color: rgba(255, 255, 255, 0.16);
  }
  .btn:active:not(:disabled) {
    transform: scale(0.96);
  }
  .btn:disabled {
    cursor: default;
    opacity: 0.6;
  }
  .btn.busy {
    opacity: 0.85;
  }

  .primary {
    border-color: transparent;
    background: var(--accent);
    box-shadow: var(--glow), inset 0 1px 0 rgba(255, 255, 255, 0.22);
  }
  .primary:hover:not(:disabled) {
    background: var(--accent);
    box-shadow: var(--glow-strong), inset 0 1px 0 rgba(255, 255, 255, 0.28);
  }

  .spinner {
    width: 11px;
    height: 11px;
    border-radius: 50%;
    border: 1.5px solid rgba(255, 255, 255, 0.3);
    border-top-color: #fff;
    animation: spin 0.8s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
