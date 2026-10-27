<script lang="ts">
  // The Settings section: updates first (the running version, when the
  // last check was, a manual check, and the available update's notes with
  // an Install button that asks first; the app restarts after it; opening
  // Settings checks by itself when the last check is over
  // AUTO_CHECK_AFTER old), then the Immich server (ServerSettings),
  // language, parallel uploads, automatic camera
  // paths and autostart (each saved as soon as it changes; Go answers with
  // a "state" event, which also switches the UI language; autostart is
  // disabled when the app does not run from its bundle), what happens to
  // the card after a sync (turning auto-format on asks first, in the row,
  // and its button only works after a countdown),
  // the notification switches (the kinds are disabled while notifications
  // are off), and last a button that opens the log panel.
  import { onDestroy, onMount } from "svelte";
  import { fade, fly, slide } from "svelte/transition";
  import { NotificationsView, SettingsView, type UpdateView } from "../../bindings/github.com/dmitiigorshkov/sd-sync-immich/internal/gui";
  import Button from "../components/Button.svelte";
  import GlassCard from "../components/GlassCard.svelte";
  import ServerSettings from "../components/ServerSettings.svelte";
  import Switch from "../components/Switch.svelte";
  import { appInfo, appState, backend, parseTime } from "../lib/backend";
  import { formatNumber, formatRelative, languageNames, supported, t } from "../lib/i18n";
  import { icons } from "../lib/icons";
  import { logsOpen, reducedMotion } from "../lib/stores";

  // As Go's config.DefaultConcurrency and maxConcurrency.
  const DEFAULT_CONCURRENCY = 4;
  const MAX_CONCURRENCY = 16;

  const ms = (n: number) => ($reducedMotion ? 0 : n);

  // Where the app's icon lives differs by system: the macOS menu bar, the
  // Windows notification area; a Linux desktop may have no tray at all,
  // so it (and the window before AppInfo loads) gets neutral wording.
  const os = $derived($appInfo?.os ?? "");
  const autostartHint = $derived(
    os === "macOS"
      ? t("settings.autostart_hint_mac")
      : os === "Windows"
        ? t("settings.autostart_hint_windows")
        : t("settings.autostart_hint"),
  );
  const notifyHint = $derived(
    os === "macOS"
      ? t("settings.notify_enabled_hint_mac")
      : os === "Windows"
        ? t("settings.notify_enabled_hint_windows")
        : t("settings.notify_enabled_hint"),
  );

  function message(err: unknown): string {
    return err instanceof Error ? err.message : String(err);
  }

  // ---- General ------------------------------------------------------------------
  let language = $state("");
  // Each sync picks the number of parallel uploads itself (Go: 0).
  let auto = $state(true);
  let concurrency = $state(DEFAULT_CONCURRENCY);
  let autoPaths = $state(true);
  let autostart = $state(false);
  let autoEject = $state(true);
  let autoFormat = $state(false);
  // Turning auto-format on waits for this confirmation, whose button only
  // works once confirmWait has counted down to 0 (from CONFIRM_SECONDS,
  // each time the confirmation opens): erasing the card cannot be undone.
  const CONFIRM_SECONDS = 10;
  let confirmFormat = $state(false);
  let confirmWait = $state(0);
  let confirmTimer: ReturnType<typeof setInterval> | undefined;
  let notifications = $state(new NotificationsView({ enabled: true, done: true, errors: true, otherCard: true, server: true, card: true }));

  // The notification kinds, in the order of the spec.
  const notifyKinds = [
    { key: "done", label: "settings.notify_done" },
    { key: "errors", label: "settings.notify_errors" },
    { key: "otherCard", label: "settings.notify_other_card" },
    { key: "server", label: "settings.notify_server" },
    { key: "card", label: "settings.notify_card" },
  ] as const;

  function syncFromState(s: SettingsView): void {
    language = s.language;
    auto = s.concurrency <= 0;
    concurrency = s.concurrency >= 1 && s.concurrency <= MAX_CONCURRENCY ? s.concurrency : DEFAULT_CONCURRENCY;
    autoPaths = s.autoPaths;
    autostart = s.autostart;
    autoEject = s.autoEject;
    autoFormat = s.autoFormat;
    if (s.notifications) notifications = new NotificationsView(s.notifications);
  }
  $effect(() => syncFromState($appState.settings));

  // Which card the "Saved" / error feedback shows in: the one whose
  // setting changed last.
  type Group = "general" | "card" | "notify";
  let group = $state<Group>("general");
  let saveError = $state("");
  let saved = $state(false);
  let savedTimer: ReturnType<typeof setTimeout> | undefined;

  function showSaved(): void {
    saved = true;
    clearTimeout(savedTimer);
    savedTimer = setTimeout(() => (saved = false), 2000);
  }

  async function save(g: Group = "general"): Promise<void> {
    group = g;
    saveError = "";
    saved = false;
    try {
      await backend.SaveSettings(
        new SettingsView({
          language,
          concurrency: auto ? 0 : concurrency,
          autoPaths,
          autoEject,
          autoFormat,
          notifications: new NotificationsView(notifications),
        }),
      );
    } catch (err) {
      saveError = t("settings.save_failed", { detail: message(err) });
      syncFromState($appState.settings);
      return;
    }
    showSaved();
  }

  async function saveAutostart(enabled: boolean): Promise<void> {
    group = "general";
    autostart = enabled;
    saveError = "";
    saved = false;
    try {
      await backend.SetAutostart(enabled);
    } catch (err) {
      // Go answers a translocated app with this code instead of a message.
      saveError =
        message(err) === "autostart_translocated"
          ? t("settings.autostart_translocated")
          : t("settings.save_failed", { detail: message(err) });
      autostart = $appState.settings.autostart;
      return;
    }
    showSaved();
  }

  function openConfirm(): void {
    confirmFormat = true;
    confirmWait = CONFIRM_SECONDS;
    clearInterval(confirmTimer);
    confirmTimer = setInterval(() => {
      confirmWait--;
      if (confirmWait <= 0) {
        confirmWait = 0;
        clearInterval(confirmTimer);
        confirmTimer = undefined;
      }
    }, 1000);
  }

  function closeConfirm(): void {
    confirmFormat = false;
    confirmWait = 0;
    clearInterval(confirmTimer);
    confirmTimer = undefined;
  }

  function setAutoFormat(enabled: boolean): void {
    if (enabled) {
      // The switch stays off until the user confirms.
      openConfirm();
      return;
    }
    closeConfirm();
    autoFormat = false;
    save("card");
  }

  function enableAutoFormat(): void {
    if (confirmWait > 0) return;
    closeConfirm();
    autoFormat = true;
    save("card");
  }

  function setNotify(key: keyof NotificationsView, enabled: boolean): void {
    notifications = new NotificationsView({ ...notifications, [key]: enabled });
    save("notify");
  }

  const fill = $derived(((concurrency - 1) / (MAX_CONCURRENCY - 1)) * 100);

  // ---- Updates ---------------------------------------------------------------------
  // Go checks at launch and every 12 hours; opening Settings checks too
  // when the last check is older than this.
  const AUTO_CHECK_AFTER = 10 * 60 * 1000;

  type Check = "idle" | "checking" | "failed";
  let check = $state<Check>("idle");
  let checkError = $state("");
  let found = $state<UpdateView | null>(null);
  const update = $derived($appState.update ?? found);
  // When the last check succeeded (0: never); "now" ticks so "Checked 5
  // minutes ago" stays true while Settings is open.
  const checkedAt = $derived(parseTime($appState.updateCheckedAt));
  let now = $state(Date.now());
  const clock = setInterval(() => (now = Date.now()), 30_000);

  /** Checks for updates; a failed automatic check stays quiet. */
  async function checkUpdates(auto = false): Promise<void> {
    check = "checking";
    try {
      found = await backend.CheckForUpdates();
      check = "idle";
    } catch (err) {
      if (auto) {
        // Development builds are never checked, and the next manual
        // check reports a network problem.
        console.warn("automatic update check failed", err);
        check = "idle";
        return;
      }
      checkError = t("settings.check_failed", { detail: message(err) });
      check = "failed";
    }
  }

  onMount(() => {
    if (Date.now() - checkedAt > AUTO_CHECK_AFTER) checkUpdates(true);
  });

  let confirming = $state(false);
  let installing = $state(false);
  let installError = $state("");

  async function install(): Promise<void> {
    installError = "";
    installing = true;
    try {
      // On success the app quits and the new version starts (launchd on
      // macOS, the installer on Windows, the new AppImage on Linux).
      await backend.InstallUpdate();
    } catch (err) {
      // Go answers a copy that cannot update itself (Linux outside an
      // AppImage) with this code instead of a message.
      installError =
        message(err) === "update_manual"
          ? t("settings.update_manual")
          : t("settings.install_failed", { detail: message(err) });
      installing = false;
      confirming = false;
    }
  }

  onDestroy(() => {
    clearTimeout(savedTimer);
    clearInterval(confirmTimer);
    clearInterval(clock);
  });
</script>

{#snippet feedback(g: Group)}
  <div class="feedback" aria-live="polite">
    {#if group === g}
      {#if saveError}
        <p class="error-text" role="alert">{saveError}</p>
      {:else if saved}
        <p class="ok-text" transition:fade={{ duration: ms(200) }}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons.check} /></svg>{t("settings.saved")}
        </p>
      {/if}
    {/if}
  </div>
{/snippet}

<div class="section">
  <GlassCard tone={update ? "accent" : "idle"} padding="16px 18px 18px">
    <h2 class="eyebrow">{t("settings.updates")}</h2>

    <div class="version-row">
      <div class="text">
        <div class="app-version">{t("settings.version", { version: $appState.version || "—" })}</div>
        <div class="check-result" aria-live="polite">
          {#if check === "failed"}
            <p class="error-text" in:fly={{ y: 4, duration: ms(250) }}>{checkError}</p>
          {:else if checkedAt && !update}
            <p class="ok-text" in:fly={{ y: 4, duration: ms(250) }}>
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons.check} /></svg>{t("settings.up_to_date")}
            </p>
          {/if}
          {#if checkedAt}
            <p class="hint">
              {t("settings.last_checked", { when: formatRelative(new Date(checkedAt), new Date(Math.max(now, checkedAt))) })}
            </p>
          {/if}
        </div>
      </div>
      <Button busy={check === "checking"} disabled={installing} onclick={() => checkUpdates()}>{t("settings.check_updates")}</Button>
    </div>

    {#if update}
      <div class="update" transition:slide={{ duration: ms(350) }}>
        <div class="update-head">
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons.download} /></svg>
          <h3>{t("settings.update_available", { version: update.version })}</h3>
        </div>
        {#if update.notes.trim()}
          <!-- Plain text: Svelte escapes it; pre-wrap keeps the line breaks. -->
          <div class="notes">{update.notes.trim()}</div>
        {/if}

        {#if installing}
          <p class="installing" role="status"><span class="spinner" aria-hidden="true"></span>{t("settings.installing")}</p>
        {:else if confirming}
          <div class="confirm" in:fly={{ y: 4, duration: ms(250) }}>
            <p>{t("settings.install_confirm", { version: update.version })}</p>
            <div class="buttons">
              <Button variant="primary" onclick={install}>{t("settings.install_now")}</Button>
              <Button onclick={() => (confirming = false)}>{t("settings.cancel")}</Button>
            </div>
          </div>
        {:else}
          <div class="buttons">
            <Button variant="primary" onclick={() => (confirming = true)}>{t("settings.install")}</Button>
          </div>
        {/if}
        {#if installError}
          <p class="error-text" role="alert">{installError}</p>
        {/if}
      </div>
    {/if}
  </GlassCard>

  <ServerSettings />

  <GlassCard padding="16px 18px 6px">
    <h2 class="eyebrow">{t("settings.general")}</h2>

    <div class="row">
      <div class="text"><label for="set-language">{t("settings.language")}</label></div>
      <select
        id="set-language"
        class="input control"
        value={language}
        onchange={(e) => {
          language = e.currentTarget.value;
          save();
        }}
      >
        <option value="">{t("settings.language_system")}</option>
        {#each supported as tag (tag)}
          <option value={tag} lang={tag}>{languageNames[tag]}</option>
        {/each}
      </select>
    </div>

    <div class="row">
      <div class="text">
        <label for="set-concurrency-auto">{t("settings.concurrency")}</label>
        <p class="hint" id="set-concurrency-hint">
          {auto
            ? t("settings.concurrency_auto_hint")
            : t("settings.concurrency_hint", { default: formatNumber(DEFAULT_CONCURRENCY) })}
        </p>
      </div>
      <div class="auto control">
        <span aria-hidden="true">{t("settings.concurrency_auto")}</span>
        <Switch
          id="set-concurrency-auto"
          checked={auto}
          describedby="set-concurrency-hint"
          onchange={(v) => {
            auto = v;
            save();
          }}
        />
      </div>
      {#if !auto}
        <div class="manual" transition:slide={{ duration: ms(250) }}>
          <div class="range control">
            <input
              id="set-concurrency"
              type="range"
              min="1"
              max={MAX_CONCURRENCY}
              step="1"
              style:--fill="{fill}%"
              aria-label={t("settings.concurrency")}
              aria-describedby="set-concurrency-hint"
              value={concurrency}
              oninput={(e) => (concurrency = Number(e.currentTarget.value))}
              onchange={() => save()}
            />
            <output for="set-concurrency">{formatNumber(concurrency)}</output>
          </div>
        </div>
      {/if}
    </div>

    <div class="row">
      <div class="text">
        <label for="set-auto-paths">{t("settings.auto_paths")}</label>
        <p class="hint" id="set-auto-paths-hint">{t("settings.auto_paths_hint")}</p>
      </div>
      <Switch
        id="set-auto-paths"
        checked={autoPaths}
        describedby="set-auto-paths-hint"
        onchange={(v) => {
          autoPaths = v;
          save();
        }}
      />
    </div>

    <div class="row">
      <div class="text">
        <label for="set-autostart">{t("settings.autostart")}</label>
        <p class="hint" id="set-autostart-hint">
          {#if $appState.settings.autostartTranslocated}
            {t("settings.autostart_translocated")}
          {:else if $appState.settings.autostartAvailable}
            {autostartHint}
          {:else}
            {t("settings.autostart_unavailable")}
          {/if}
        </p>
      </div>
      <Switch
        id="set-autostart"
        checked={autostart}
        disabled={!$appState.settings.autostartAvailable}
        describedby="set-autostart-hint"
        onchange={saveAutostart}
      />
    </div>

    {@render feedback("general")}
  </GlassCard>

  <GlassCard padding="16px 18px 6px">
    <h2 class="eyebrow">{t("settings.card")}</h2>

    <div class="row">
      <div class="text">
        <label for="set-auto-eject">{t("settings.auto_eject")}</label>
        <p class="hint" id="set-auto-eject-hint">{t("settings.auto_eject_hint")}</p>
      </div>
      <Switch
        id="set-auto-eject"
        checked={autoEject}
        describedby="set-auto-eject-hint"
        onchange={(v) => {
          autoEject = v;
          save("card");
        }}
      />
    </div>

    <div class="row">
      <div class="text">
        <label for="set-auto-format">{t("settings.auto_format")}</label>
        <p class="hint" id="set-auto-format-hint">{t("settings.auto_format_hint")}</p>
        {#if $appState.settings.autoFormatNeedsAdmin}
          <!-- Windows without administrator rights: the card is never
               formatted, only ejected (if that is on). -->
          <p class="hint" id="set-auto-format-admin-hint">{t("settings.auto_format_needs_admin_hint")}</p>
        {/if}
      </div>
      <Switch
        id="set-auto-format"
        checked={autoFormat}
        describedby={$appState.settings.autoFormatNeedsAdmin
          ? "set-auto-format-hint set-auto-format-admin-hint"
          : "set-auto-format-hint"}
        onchange={setAutoFormat}
      />
      {#if confirmFormat}
        <div class="confirm-row" transition:slide={{ duration: ms(250) }}>
          <svg viewBox="0 0 24 24" aria-hidden="true"><path d={icons.warning} /></svg>
          <p id="set-auto-format-confirm">{t("settings.auto_format_confirm")}</p>
          <div class="buttons">
            <Button onclick={closeConfirm}>{t("settings.cancel")}</Button>
            <Button
              variant="primary"
              disabled={confirmWait > 0}
              describedby="set-auto-format-confirm"
              onclick={enableAutoFormat}
            >
              <!-- The widest label (the countdown's start) reserves the
                   button's width, so it does not jump as the count ends. -->
              <span class="reserve">
                <span class="slot" aria-hidden="true">
                  {t("settings.auto_format_enable_countdown", { seconds: formatNumber(CONFIRM_SECONDS) })}
                </span>
                <span class="slot" aria-hidden="true">{t("settings.auto_format_enable")}</span>
                <span>
                  {confirmWait > 0
                    ? t("settings.auto_format_enable_countdown", { seconds: formatNumber(confirmWait) })
                    : t("settings.auto_format_enable")}
                </span>
              </span>
            </Button>
          </div>
        </div>
      {/if}
    </div>

    {@render feedback("card")}
  </GlassCard>

  <GlassCard padding="16px 18px 6px">
    <h2 class="eyebrow">{t("settings.notifications")}</h2>

    <div class="row">
      <div class="text">
        <label for="set-notify">{t("settings.notify_enabled")}</label>
        <p class="hint" id="set-notify-hint">{notifyHint}</p>
      </div>
      <Switch
        id="set-notify"
        checked={notifications.enabled}
        describedby="set-notify-hint"
        onchange={(v) => setNotify("enabled", v)}
      />
    </div>

    <ul class="kinds" class:off={!notifications.enabled}>
      {#each notifyKinds as kind (kind.key)}
        <li>
          <label for="set-notify-{kind.key}">{t(kind.label)}</label>
          <Switch
            id="set-notify-{kind.key}"
            checked={notifications[kind.key]}
            disabled={!notifications.enabled}
            onchange={(v) => setNotify(kind.key, v)}
          />
        </li>
      {/each}
    </ul>

    {@render feedback("notify")}
  </GlassCard>

  <GlassCard padding="16px 18px 6px">
    <h2 class="eyebrow">{t("settings.logs")}</h2>

    <div class="row">
      <div class="text">
        <p class="hint" id="set-logs-hint">{t("settings.logs_hint")}</p>
      </div>
      <Button describedby="set-logs-hint" onclick={() => logsOpen.set(true)}>
        <svg class="button-icon" viewBox="0 0 24 24" aria-hidden="true"><path d={icons.logs} /></svg>
        {t("settings.open_logs")}
      </Button>
    </div>
  </GlassCard>
</div>

<style>
  h2.eyebrow {
    margin-bottom: 4px;
  }

  .row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 8px 20px;
    padding: 12px 0;
  }
  .row + .row {
    border-top: 1px solid rgba(255, 255, 255, 0.06);
  }
  .text {
    flex: 1 1 220px;
    min-width: 0;
  }
  label {
    color: var(--text);
    font-size: 13px;
    font-weight: 500;
  }
  .hint {
    margin-top: 3px;
  }
  .control {
    flex: 0 0 auto;
  }
  select.control {
    width: 210px;
  }

  /* ---- Range ---- */
  .auto {
    display: flex;
    align-items: center;
    gap: 10px;
    color: var(--text-2);
    font-size: 12.5px;
  }
  .manual {
    display: flex;
    flex-basis: 100%;
    justify-content: flex-end;
  }

  .range {
    display: flex;
    align-items: center;
    gap: 12px;
    width: 210px;
  }
  input[type="range"] {
    flex: 1;
    height: 18px;
    margin: 0;
    background: transparent;
    appearance: none;
    cursor: pointer;
  }
  input[type="range"]::-webkit-slider-runnable-track {
    height: 5px;
    border-radius: 3px;
    background:
      linear-gradient(90deg, var(--accent-1), var(--accent-2)) 0 0 / var(--fill) 100% no-repeat,
      rgba(255, 255, 255, 0.14);
  }
  input[type="range"]::-webkit-slider-thumb {
    width: 16px;
    height: 16px;
    margin-top: -5.5px;
    border-radius: 50%;
    background: #fff;
    box-shadow: 0 1px 4px rgba(0, 0, 0, 0.4);
    appearance: none;
    transition:
      transform 0.25s var(--spring),
      box-shadow 0.25s;
  }
  input[type="range"]:active::-webkit-slider-thumb {
    transform: scale(1.15);
    box-shadow: 0 0 12px rgba(123, 92, 255, 0.7), 0 1px 4px rgba(0, 0, 0, 0.4);
  }
  input[type="range"]::-moz-range-track {
    height: 5px;
    border-radius: 3px;
    background: rgba(255, 255, 255, 0.14);
  }
  input[type="range"]::-moz-range-progress {
    height: 5px;
    border-radius: 3px;
    background: linear-gradient(90deg, var(--accent-1), var(--accent-2));
  }
  input[type="range"]::-moz-range-thumb {
    width: 16px;
    height: 16px;
    border: 0;
    border-radius: 50%;
    background: #fff;
  }
  output {
    min-width: 22px;
    color: var(--text);
    font-size: 14px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    text-align: right;
  }

  /* ---- Auto-format confirmation ---- */
  .confirm-row {
    display: flex;
    flex-basis: 100%;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 10px;
    padding: 10px 12px;
    border: 1px solid rgba(255, 179, 64, 0.28);
    border-radius: var(--radius-m);
    background: rgba(255, 159, 10, 0.08);
  }
  .confirm-row svg {
    width: 18px;
    height: 18px;
    flex-shrink: 0;
    fill: none;
    stroke: var(--amber);
    stroke-width: 1.8;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  .confirm-row p {
    flex: 1 1 200px;
    margin: 0;
    color: var(--text);
    font-size: 12.5px;
    line-height: 1.45;
  }
  .confirm-row .buttons {
    margin: 0;
    margin-left: auto;
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

  /* ---- Notification kinds ---- */
  .kinds {
    margin: 0 0 6px;
    padding: 2px 0 0 14px;
    border-left: 2px solid rgba(255, 255, 255, 0.08);
    list-style: none;
    transition: opacity 0.25s;
  }
  .kinds.off {
    opacity: 0.55;
  }
  .kinds li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    padding: 7px 0;
  }
  .kinds label {
    min-width: 0;
    font-weight: 400;
    color: var(--text-2);
  }

  .feedback {
    min-height: 6px;
  }
  .feedback p {
    margin: 0 0 12px;
  }
  .ok-text {
    display: flex;
    align-items: center;
    gap: 5px;
    margin: 0;
    color: var(--green);
    font-size: 12.5px;
  }
  .ok-text svg {
    width: 14px;
    height: 14px;
    flex-shrink: 0;
    fill: none;
    stroke: currentColor;
    stroke-width: 2.6;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  .button-icon {
    width: 15px;
    height: 15px;
    margin-right: 6px;
    vertical-align: -3px;
    fill: none;
    stroke: currentColor;
    stroke-width: 1.8;
    stroke-linecap: round;
    stroke-linejoin: round;
  }

  /* ---- Updates ---- */
  .version-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 10px 20px;
    padding-top: 8px;
  }
  .app-version {
    font-size: 13px;
    font-weight: 500;
    font-variant-numeric: tabular-nums;
  }
  .check-result p {
    margin: 3px 0 0;
  }

  .update {
    margin-top: 16px;
    padding-top: 14px;
    border-top: 1px solid rgba(255, 255, 255, 0.08);
  }
  .update-head {
    display: flex;
    align-items: center;
    gap: 8px;
    color: var(--accent-soft);
  }
  .update-head svg {
    width: 18px;
    height: 18px;
    flex-shrink: 0;
    fill: none;
    stroke: currentColor;
    stroke-width: 2;
    stroke-linecap: round;
    stroke-linejoin: round;
  }
  h3 {
    margin: 0;
    color: var(--text);
    font-size: 14px;
    font-weight: 650;
  }
  .notes {
    max-height: 180px;
    margin-top: 10px;
    padding: 10px 12px;
    overflow: auto;
    border-radius: var(--radius-m);
    background: rgba(8, 6, 16, 0.35);
    color: var(--text-2);
    font-size: 12.5px;
    line-height: 1.55;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
    -webkit-user-select: text;
    user-select: text;
  }
  .buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 14px;
  }
  .confirm p {
    margin: 14px 0 0;
    color: var(--text);
    font-size: 12.5px;
  }
  .confirm .buttons {
    margin-top: 10px;
  }
  .installing {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 14px 0 0;
    color: var(--text-2);
    font-size: 12.5px;
  }
  .spinner {
    width: 13px;
    height: 13px;
    flex-shrink: 0;
    border-radius: 50%;
    border: 2px solid rgba(255, 255, 255, 0.22);
    border-top-color: var(--accent-soft);
    animation: spin 0.9s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>
