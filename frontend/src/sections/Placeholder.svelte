<script lang="ts">
  // Stand-in for sections that are not built yet.
  import GlassCard from "../components/GlassCard.svelte";
  import { t } from "../lib/i18n";
  import { icons } from "../lib/icons";
  import type { SectionId } from "../lib/stores";

  let { id }: { id: SectionId } = $props();
</script>

<GlassCard>
  <div class="soon">
    <svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons[id]} /></svg>
    <p>{t("ui.coming_soon")}</p>
  </div>
</GlassCard>

<style>
  .soon {
    display: flex;
    align-items: center;
    gap: 12px;
    color: var(--text-2);
  }
  svg {
    width: 22px;
    height: 22px;
    flex-shrink: 0;
    fill: none;
    stroke: var(--accent-soft);
    stroke-width: 1.7;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  p {
    margin: 0;
  }
</style>
