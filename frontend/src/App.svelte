<script lang="ts">
  // Temporary stub proving that bindings and events work end to end; the
  // real UI replaces it (status section, then the other sections).
  import { onMount } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { Service } from "../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/gui";

  let code = $state("…");

  onMount(() => {
    Service.State().then((s) => (code = s.status.code));
    return Events.On("status", (ev) => {
      code = ev.data.code;
    });
  });

  function syncNow(): void {
    Service.SyncNow().catch(console.error);
  }
</script>

<main>
  <p>Status: <code>{code}</code></p>
  <button onclick={syncNow}>Sync now</button>
</main>

<style>
  :global(body) {
    margin: 0;
    background: #0f0e14;
    color: #e8e6f0;
    font-family: -apple-system, BlinkMacSystemFont, sans-serif;
  }
  main {
    padding: 64px 32px;
  }
</style>
