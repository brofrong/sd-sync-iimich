<script lang="ts">
  // The window: sidebar on the left, the current section on the right.
  // Behind the content, soft colour blobs (tinted by the status) give the
  // glass cards something to blur — the window itself is opaque. The log
  // panel opens over all of it.
  import { onMount } from "svelte";
  import { fade, fly } from "svelte/transition";

  import LogsDialog from "./components/LogsDialog.svelte";
  import Sidebar from "./components/Sidebar.svelte";
  import { connect, status } from "./lib/backend";
  import { lang, t } from "./lib/i18n";
  import {
    logsOpen,
    navigate,
    reducedMotion,
    section,
    toneFor,
  } from "./lib/stores";
  import Cards from "./sections/Cards.svelte";
  import Gallery from "./sections/Gallery.svelte";
  import History from "./sections/History.svelte";
  import Settings from "./sections/Settings.svelte";
  import Status from "./sections/Status.svelte";

  onMount(connect);

  const tone = $derived(toneFor($status.code));
  const ms = (n: number) => ($reducedMotion ? 0 : n);
</script>

{#key $lang}
  <div class="window">
    <Sidebar current={$section} onSelect={navigate} />

    <main class="main">
      <div class="ambient tone-{tone}" aria-hidden="true">
        <i class="blob b1"></i>
        <i class="blob b2"></i>
        <i class="blob b3"></i>
      </div>

      <header class="head drag">
        {#key $section}
          <h1 in:fly={{ x: -8, duration: ms(300) }}>
            {t(`section.${$section}`)}
          </h1>
        {/key}
      </header>

      <div class="scroll">
        <div class="pages">
          {#key $section}
            <div
              class="page"
              in:fly={{ y: 12, duration: ms(350), delay: ms(90) }}
              out:fade={{ duration: ms(120) }}
            >
              {#if $section === "status"}
                <Status onNavigate={navigate} />
              {:else if $section === "gallery"}
                <Gallery onNavigate={navigate} />
              {:else if $section === "history"}
                <History />
              {:else if $section === "cards"}
                <Cards />
              {:else}
                <Settings />
              {/if}
            </div>
          {/key}
        </div>
      </div>
    </main>
  </div>

  {#if $logsOpen}
    <LogsDialog onclose={() => logsOpen.set(false)} />
  {/if}
{/key}

<style>
  .window {
    display: flex;
    height: 100vh;
  }

  .main {
    position: relative;
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
    overflow: hidden;
    background: var(--bg-main);
    isolation: isolate;
  }
  /* A faint grain over the whole content area. */
  .main::after {
    content: "";
    position: absolute;
    inset: 0;
    z-index: -1;
    pointer-events: none;
    opacity: 0.035;
    background-image: var(--noise);
  }

  .head {
    display: flex;
    align-items: flex-end;
    height: 56px;
    flex-shrink: 0;
    padding: 0 28px 8px;
  }
  h1 {
    margin: 0;
    font-size: 20px;
    font-weight: 700;
    letter-spacing: -0.015em;
  }

  .scroll {
    flex: 1;
    overflow-y: auto;
    padding: 8px 28px 28px;
  }
  .pages {
    display: grid;
  }
  .page {
    grid-area: 1 / 1;
    min-width: 0;
  }

  /* ---- Ambient light behind the glass ---- */
  .ambient {
    position: absolute;
    inset: 0;
    z-index: -1;
    overflow: hidden;
    pointer-events: none;
  }
  .blob {
    position: absolute;
    width: 440px;
    height: 440px;
    border-radius: 50%;
    background: radial-gradient(circle, currentColor 0, transparent 66%);
    opacity: 0.32;
    transition: color 1.2s ease;
    will-change: transform;
  }
  .b1 {
    top: -150px;
    left: -60px;
    animation: drift-a 22s ease-in-out infinite alternate;
  }
  .b2 {
    top: -40px;
    right: -140px;
    opacity: 0.26;
    animation: drift-b 26s ease-in-out infinite alternate;
  }
  .b3 {
    top: 190px;
    left: 30%;
    width: 380px;
    height: 380px;
    opacity: 0.2;
    animation: drift-c 30s ease-in-out infinite alternate;
  }
  @keyframes drift-a {
    to {
      transform: translate(90px, 50px) scale(1.12);
    }
  }
  @keyframes drift-b {
    to {
      transform: translate(-110px, 70px) scale(0.92);
    }
  }
  @keyframes drift-c {
    to {
      transform: translate(-70px, -50px) scale(1.15);
    }
  }

  .tone-idle .b1 {
    color: #6a4cf0;
  }
  .tone-idle .b2 {
    color: #1f5fd6;
  }
  .tone-idle .b3 {
    color: #8a3cc4;
  }

  .tone-accent .b1 {
    color: #7b5cff;
  }
  .tone-accent .b2 {
    color: #0a84ff;
  }
  .tone-accent .b3 {
    color: #3ab8ff;
  }

  .tone-error .b1 {
    color: #ff453a;
  }
  .tone-error .b2 {
    color: #b0275a;
  }
  .tone-error .b3 {
    color: #6a3cd6;
  }

  .tone-warn .b1 {
    color: #ff9f0a;
  }
  .tone-warn .b2 {
    color: #7b5cff;
  }
  .tone-warn .b3 {
    color: #e0583a;
  }

  .tone-success .b1 {
    color: #30d158;
  }
  .tone-success .b2 {
    color: #00b3a0;
  }
  .tone-success .b3 {
    color: #0a84ff;
  }
</style>
