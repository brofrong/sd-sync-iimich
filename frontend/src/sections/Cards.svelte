<script lang="ts">
  // The Cards section: the selected card first (shown even while it is not
  // plugged in), then the mounted removable volumes, then the cards the app
  // remembers. A mounted card can be selected; the selected mounted card can
  // be ejected; a remembered or selected card can be forgotten, after a
  // confirmation, except while it syncs. The list reloads on every "state"
  // event (Go sends one when a volume mounts or unmounts, or a card is
  // forgotten).
  import { flip } from "svelte/animate";
  import { fade, slide } from "svelte/transition";

  import type { CardView } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/gui";
  import Button from "../components/Button.svelte";
  import GlassCard from "../components/GlassCard.svelte";
  import { appState, backend, status } from "../lib/backend";
  import { formatBytes, formatNumber, t } from "../lib/i18n";
  import { icons } from "../lib/icons";
  import { reducedMotion } from "../lib/stores";

  let cards = $state<CardView[] | null>(null);
  let loadError = $state("");
  let actionError = $state("");
  // The card whose "Forget" awaits confirmation.
  let confirming = $state<string | null>(null);
  // Only the selected card syncs; it can't be forgotten meanwhile.
  const syncing = $derived(
    $status.code === "syncing" || $status.code === "scanning",
  );
  const ms = (n: number) => ($reducedMotion ? 0 : n);

  function message(err: unknown): string {
    return err instanceof Error ? err.message : String(err);
  }

  let generation = 0;
  function load(): void {
    const g = ++generation;
    backend.Cards().then(
      (list) => {
        if (g !== generation) return; // a newer load is under way
        cards = list ?? [];
        loadError = "";
      },
      (err) => {
        if (g === generation)
          loadError = t("ui.load_failed", { detail: message(err) });
      },
    );
  }

  // appState changes on every "state" event (and once on open).
  $effect(() => {
    void $appState;
    load();
  });

  async function act(fn: () => Promise<void>): Promise<void> {
    actionError = "";
    try {
      await fn();
    } catch (err) {
      actionError = t("ui.action_failed", { detail: message(err) });
    }
    load();
  }
</script>

<div class="section">
  <p class="intro">{t("cards.hint")}</p>

  {#if loadError}
    <GlassCard tone="error">
      <p class="error-text" role="alert">{loadError}</p>
    </GlassCard>
  {:else if cards === null}
    <GlassCard>
      <div class="skeleton-lines" aria-hidden="true"><i></i><i></i></div>
    </GlassCard>
  {:else if cards.length === 0}
    <GlassCard>
      <p class="empty-state">
        <svg class="pulse" viewBox="0 0 24 24" aria-hidden="true"
          ><path d={icons.cards} /></svg
        ><span>{t("cards.empty")}</span>
      </p>
    </GlassCard>
  {:else}
    <ul class="list">
      {#each cards as card (card.uuid)}
        <li
          animate:flip={{ duration: ms(450) }}
          in:fade={{ duration: ms(250) }}
        >
          <GlassCard
            tone={card.selected && card.mounted ? "accent" : "idle"}
            padding="14px 16px"
          >
            <div class="row" class:away={!card.mounted}>
              <span class="icon" class:on={card.selected} aria-hidden="true">
                <svg viewBox="0 0 24 24"><path d={icons.cards} /></svg>
              </span>
              <div class="info">
                <div class="name-line">
                  <span
                    class="name"
                    id="card-{card.uuid}"
                    title={card.name || t("cards.unnamed")}
                  >
                    {card.name || t("cards.unnamed")}
                  </span>
                  {#if card.selected}
                    <span class="badge" in:fade={{ duration: ms(200) }}
                      >{t("cards.selected")}</span
                    >
                  {/if}
                </div>
                <div class="sub">
                  {#if card.mounted}
                    {#if card.sizeBytes > 0}<span
                        >{formatBytes(card.sizeBytes)}</span
                      >{/if}
                    {#if card.mountPoint}<span
                        class="path"
                        title={card.mountPoint}>{card.mountPoint}</span
                      >{/if}
                  {:else}
                    <span class="offline"
                      ><i class="dot" aria-hidden="true"></i>{t(
                        "cards.not_connected",
                      )}</span
                    >
                  {/if}
                  {#if card.remembered}
                    <span
                      >{t("cards.uploaded", {
                        count: formatNumber(card.uploaded),
                      })}</span
                    >
                  {/if}
                </div>
                <div class="uuid">{t("cards.uuid", { uuid: card.uuid })}</div>
              </div>
              <div class="actions">
                {#if card.mounted && !card.selected}
                  <Button
                    describedby="card-{card.uuid}"
                    onclick={() => act(() => backend.SelectCard(card.uuid))}
                  >
                    {t("cards.select")}
                  </Button>
                {:else if card.mounted && card.selected}
                  <Button
                    describedby="card-{card.uuid}"
                    onclick={() => act(() => backend.EjectCard())}
                  >
                    {t("cards.eject")}
                  </Button>
                {/if}
                {#if (card.remembered || card.selected) && confirming !== card.uuid}
                  <Button
                    describedby="card-{card.uuid}"
                    disabled={card.selected && syncing}
                    onclick={() => (confirming = card.uuid)}
                  >
                    {t("cards.forget")}
                  </Button>
                {/if}
              </div>
            </div>
            {#if confirming === card.uuid}
              <div class="confirm" transition:slide={{ duration: ms(250) }}>
                <p id="forget-{card.uuid}">
                  {t("cards.forget_confirm", {
                    name: card.name || t("cards.unnamed"),
                  })}
                </p>
                <div class="actions">
                  <Button onclick={() => (confirming = null)}
                    >{t("cards.cancel")}</Button
                  >
                  <Button
                    variant="primary"
                    describedby="forget-{card.uuid}"
                    disabled={card.selected && syncing}
                    onclick={() =>
                      act(async () => {
                        await backend.ForgetCard(card.uuid);
                        confirming = null;
                      })}
                  >
                    {t("cards.forget")}
                  </Button>
                </div>
              </div>
            {/if}
          </GlassCard>
        </li>
      {/each}
    </ul>
  {/if}

  {#if actionError}
    <p class="error-text" role="alert">{actionError}</p>
  {/if}
</div>

<style>
  .intro {
    margin: 0;
    color: var(--text-2);
    font-size: 12.5px;
    line-height: 1.5;
  }

  .list {
    display: grid;
    gap: 10px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .row {
    display: flex;
    align-items: center;
    gap: 13px;
  }

  .icon {
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    flex-shrink: 0;
    border-radius: var(--radius-m);
    background: rgba(255, 255, 255, 0.07);
    color: var(--text-2);
    transition:
      background-color 0.4s,
      color 0.4s,
      box-shadow 0.4s;
  }
  .icon.on {
    background: linear-gradient(
      135deg,
      rgba(123, 92, 255, 0.55),
      rgba(10, 132, 255, 0.4)
    );
    color: #fff;
  }
  .row:not(.away) .icon.on {
    box-shadow: var(--glow);
  }
  .icon svg {
    width: 19px;
    height: 19px;
    fill: none;
    stroke: currentColor;
    stroke-width: 1.7;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  .away .icon {
    opacity: 0.55;
  }

  .info {
    flex: 1;
    min-width: 0;
  }
  .name-line {
    display: flex;
    align-items: center;
    gap: 8px;
    min-width: 0;
  }
  .name {
    min-width: 0;
    overflow: hidden;
    font-size: 14px;
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .badge {
    flex-shrink: 0;
    padding: 1px 8px;
    border-radius: 999px;
    border: 1px solid rgba(179, 157, 255, 0.45);
    background: rgba(123, 92, 255, 0.2);
    color: var(--accent-soft);
    font-size: 10.5px;
    font-weight: 600;
    letter-spacing: 0.02em;
  }

  .sub {
    display: flex;
    flex-wrap: wrap;
    gap: 0;
    min-width: 0;
    margin-top: 2px;
    color: var(--text-2);
    font-size: 12px;
  }
  .sub span:not(:last-child)::after {
    content: "·" / "";
    margin: 0 6px;
    color: rgba(255, 255, 255, 0.25);
  }
  .path {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .offline {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    color: var(--text-3);
  }
  .dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: rgba(255, 255, 255, 0.3);
  }

  .uuid {
    margin-top: 3px;
    overflow: hidden;
    color: var(--text-3);
    font-family: var(--mono);
    font-size: 10.5px;
    text-overflow: ellipsis;
    white-space: nowrap;
    -webkit-user-select: text;
    user-select: text;
  }

  .actions {
    display: flex;
    flex-shrink: 0;
    gap: 8px;
  }

  .confirm {
    display: flex;
    align-items: center;
    gap: 13px;
    margin-top: 12px;
    padding-top: 12px;
    border-top: 1px solid rgba(255, 255, 255, 0.08);
  }
  .confirm p {
    flex: 1;
    margin: 0;
    color: var(--text-2);
    font-size: 12.5px;
    line-height: 1.5;
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
</style>
