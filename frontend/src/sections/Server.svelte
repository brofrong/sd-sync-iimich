<script lang="ts">
  // The Server section: the Immich URL and API key. "Test Connection"
  // checks what is typed (an empty key means the saved one) and shows the
  // server version or why it failed; "Save" checks the URL first and saves
  // only a valid one. The saved key never comes back from Go: the field
  // stays empty, and an empty field keeps the saved key.
  import { onDestroy, untrack } from "svelte";
  import { fade, fly } from "svelte/transition";
  import type { ServerCheckView } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/gui";
  import Button from "../components/Button.svelte";
  import GlassCard from "../components/GlassCard.svelte";
  import { appState, backend } from "../lib/backend";
  import { t } from "../lib/i18n";
  import { icons } from "../lib/icons";
  import { reducedMotion } from "../lib/stores";

  const ms = (n: number) => ($reducedMotion ? 0 : n);

  const savedUrl = $derived($appState.server.url);
  const hasKey = $derived($appState.server.hasKey);

  let url = $state("");
  let key = $state("");
  let showKey = $state(false);
  let urlInput: HTMLInputElement;

  // The URL field follows the saved URL until the user edits it.
  let edited = false;
  $effect(() => {
    const saved = savedUrl;
    untrack(() => {
      if (!edited) url = saved;
    });
  });

  /** The URL as Go saves it (see normalizeServer), to tell real edits apart. */
  function normalize(u: string): string {
    return u.trim().replace(/\/+$/, "").replace(/\/api$/, "").replace(/\/+$/, "");
  }

  const dirty = $derived(normalize(url) !== savedUrl || key.trim() !== "");

  type Busy = "" | "test" | "save";
  let busy = $state<Busy>("");
  let urlError = $state(false);
  let result = $state<ServerCheckView | null>(null);
  let saveError = $state<string | null>(null);
  let saved = $state(false);
  let savedTimer: ReturnType<typeof setTimeout> | undefined;
  onDestroy(() => clearTimeout(savedTimer));

  function message(err: unknown): string {
    return err instanceof Error ? err.message : String(err);
  }

  function clearFeedback(): void {
    urlError = false;
    result = null;
    saveError = null;
    saved = false;
    clearTimeout(savedTimer);
  }

  function onUrlInput(): void {
    edited = true;
    clearFeedback();
  }

  async function check(): Promise<ServerCheckView> {
    try {
      return await backend.TestServer(url, key);
    } catch (err) {
      return { ok: false, version: "", code: "failed", params: { detail: message(err) } };
    }
  }

  async function test(): Promise<void> {
    if (busy) return;
    busy = "test";
    clearFeedback();
    try {
      const r = await check();
      if (r.code === "invalid_url") {
        urlError = true;
        urlInput?.focus();
      } else {
        result = r;
      }
    } finally {
      busy = "";
    }
  }

  async function save(): Promise<void> {
    if (busy || !dirty) return;
    busy = "save";
    clearFeedback();
    try {
      const r = await check();
      if (r.code === "invalid_url") {
        urlError = true;
        urlInput?.focus();
        return;
      }
      try {
        await backend.SaveServer(url, key);
      } catch (err) {
        saveError = message(err);
        return;
      }
      url = normalize(url);
      key = "";
      showKey = false;
      edited = false;
      result = r;
      saved = true;
      savedTimer = setTimeout(() => (saved = false), 2500);
    } finally {
      busy = "";
    }
  }

  function onEnter(e: KeyboardEvent): void {
    if (e.key === "Enter" && !e.isComposing) {
      e.preventDefault();
      save();
    }
  }

  // The technical reason a check failed, as Go reported it.
  const resultDetail = $derived(
    result && !result.ok ? [result.params?.status, result.params?.detail].filter(Boolean).join(" ") : "",
  );
</script>

<div class="section">
  <GlassCard padding="18px 20px 20px">
    <div class="field">
      <label for="server-url">{t("server.url")}</label>
      <input
        id="server-url"
        class="input"
        type="url"
        inputmode="url"
        autocomplete="url"
        autocapitalize="off"
        spellcheck="false"
        placeholder={t("server.url_placeholder")}
        aria-invalid={urlError}
        aria-describedby={urlError ? "server-url-error" : "server-url-hint"}
        bind:this={urlInput}
        bind:value={url}
        oninput={onUrlInput}
        onkeydown={onEnter}
      />
      {#if urlError}
        <p id="server-url-error" class="error-text" role="alert" in:fly={{ y: -4, duration: ms(220) }}>
          {t("server.invalid_url")}
        </p>
      {:else}
        <p id="server-url-hint" class="hint">{t("server.url_hint")}</p>
      {/if}
    </div>

    <div class="field">
      <label for="server-key">{t("server.key")}</label>
      <div class="key">
        <input
          id="server-key"
          class="input"
          type={showKey ? "text" : "password"}
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          placeholder={hasKey ? t("server.key_saved_placeholder") : t("server.key_placeholder")}
          aria-describedby="server-key-hint"
          bind:value={key}
          oninput={clearFeedback}
          onkeydown={onEnter}
        />
        <button
          type="button"
          class="reveal"
          aria-label={showKey ? t("server.hide_key") : t("server.show_key")}
          aria-pressed={showKey}
          aria-controls="server-key"
          title={showKey ? t("server.hide_key") : t("server.show_key")}
          onclick={() => (showKey = !showKey)}
        >
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d={showKey ? icons.eyeOff : icons.eye} /></svg>
        </button>
      </div>
      <p id="server-key-hint" class="hint">{t("server.key_hint")}</p>
    </div>

    <div class="actions">
      <Button busy={busy === "test"} disabled={busy === "save"} onclick={test}>{t("server.test")}</Button>
      <Button variant="primary" busy={busy === "save"} disabled={!dirty || busy === "test"} onclick={save}>
        {t("server.save")}
      </Button>
      <span class="saved" aria-live="polite">
        {#if saved}
          <span class="saved-text" transition:fade={{ duration: ms(200) }}>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons.check} /></svg>{t("server.saved")}
          </span>
        {/if}
      </span>
    </div>

    {#if saveError !== null}
      <div class="save-error" role="alert">
        <p class="error-text">{t("server.save_failed")}</p>
        {#if saveError}<p class="detail-text">{saveError}</p>{/if}
      </div>
    {/if}

    <div aria-live="polite">
      {#if result}
        <div class="result" class:ok={result.ok} in:fly={{ y: 6, duration: ms(300) }}>
          <span class="glyph" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d={result.ok ? icons.check : icons.warning} /></svg>
          </span>
          <div class="result-text">
            {#if result.ok}
              <p class="result-title">{t("server.connected", { version: result.version })}</p>
            {:else}
              <p class="result-title">{t(`status.${result.code}.title`)}</p>
              {#if resultDetail}<p class="detail-text">{resultDetail}</p>{/if}
            {/if}
          </div>
        </div>
      {/if}
    </div>
  </GlassCard>
</div>

<style>
  .field + .field {
    margin-top: 16px;
  }
  label {
    display: block;
    margin-bottom: 6px;
    color: var(--text-2);
    font-size: 12px;
    font-weight: 600;
  }

  .key {
    position: relative;
  }
  .key .input {
    padding-right: 38px;
  }
  .reveal {
    position: absolute;
    top: 3px;
    right: 3px;
    display: grid;
    place-items: center;
    width: 28px;
    height: 26px;
    border: 0;
    border-radius: 5px;
    background: transparent;
    color: var(--text-3);
    cursor: pointer;
    transition:
      color 0.2s,
      background-color 0.2s;
  }
  .reveal:hover {
    background: rgba(255, 255, 255, 0.08);
    color: var(--text);
  }
  .reveal[aria-pressed="true"] {
    color: var(--accent-soft);
  }
  .reveal svg {
    width: 16px;
    height: 16px;
    fill: none;
    stroke: currentColor;
    stroke-width: 1.8;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px;
    margin-top: 20px;
  }
  .saved {
    display: inline-flex;
    margin-left: 4px;
  }
  .saved-text {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    color: var(--green);
    font-size: 12.5px;
  }
  .saved-text svg {
    width: 14px;
    height: 14px;
    fill: none;
    stroke: currentColor;
    stroke-width: 2.6;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  .save-error {
    margin-top: 10px;
  }

  .result {
    display: flex;
    gap: 10px;
    margin-top: 16px;
    padding: 11px 13px;
    border-radius: var(--radius-m);
    border: 1px solid rgba(255, 100, 100, 0.35);
    background: rgba(255, 69, 58, 0.1);
  }
  .result.ok {
    border-color: rgba(48, 209, 88, 0.35);
    background: rgba(48, 209, 88, 0.09);
  }
  .glyph {
    flex-shrink: 0;
    color: var(--red);
  }
  .ok .glyph {
    color: var(--green);
  }
  .glyph svg {
    display: block;
    width: 18px;
    height: 18px;
    fill: none;
    stroke: currentColor;
    stroke-width: 2;
    stroke-linecap: round;
    stroke-linejoin: round;
    animation: pop 0.45s var(--spring) both;
  }
  @keyframes pop {
    from {
      transform: scale(0.4);
      opacity: 0;
    }
  }
  .result-text {
    min-width: 0;
  }
  .result-title {
    margin: 0;
    font-size: 13px;
    font-weight: 600;
    line-height: 18px;
    -webkit-user-select: text;
    user-select: text;
  }
</style>
