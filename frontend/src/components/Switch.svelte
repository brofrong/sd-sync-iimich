<script lang="ts">
  // An on/off switch: a native checkbox with role="switch", drawn as a
  // track with a knob that slides with a spring. The track glows while on.
  // It is controlled: it always shows `checked`, whatever was clicked.
  interface Props {
    checked: boolean;
    disabled?: boolean;
    id?: string;
    /** id of the element that describes the setting (aria-describedby). */
    describedby?: string;
    onchange: (checked: boolean) => void;
  }

  let {
    checked,
    disabled = false,
    id,
    describedby,
    onchange,
  }: Props = $props();
</script>

<span class="switch" class:on={checked} class:disabled>
  <input
    type="checkbox"
    role="switch"
    {id}
    {checked}
    {disabled}
    aria-checked={checked}
    aria-describedby={describedby}
    onchange={(e) => {
      const input = e.currentTarget;
      onchange(input.checked);
      // Controlled: the box shows the prop, not the click. A parent that
      // doesn't take the change (e.g. auto-format waits for a confirmation)
      // leaves the switch as it was, and the next click toggles again.
      input.checked = checked;
    }}
  />
  <span class="knob" aria-hidden="true"></span>
</span>

<style>
  .switch {
    position: relative;
    display: inline-block;
    width: 38px;
    height: 22px;
    flex-shrink: 0;
    border-radius: 11px;
    background: rgba(255, 255, 255, 0.14);
    box-shadow: inset 0 0 0 1px rgba(255, 255, 255, 0.08);
    transition:
      background-color 0.25s,
      box-shadow 0.3s;
  }
  .switch.on {
    background: linear-gradient(90deg, var(--accent-1), var(--accent-2));
    box-shadow:
      var(--glow),
      inset 0 1px 0 rgba(255, 255, 255, 0.2);
  }
  .switch.disabled {
    opacity: 0.5;
  }

  input {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    margin: 0;
    border-radius: inherit;
    opacity: 0;
    cursor: pointer;
  }
  input:disabled {
    cursor: default;
  }
  .switch:has(input:focus-visible) {
    outline: 2px solid var(--accent-soft);
    outline-offset: 2px;
  }

  .knob {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 18px;
    height: 18px;
    border-radius: 50%;
    background: #fff;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.35);
    pointer-events: none;
    transition: transform 0.35s var(--spring);
  }
  .on .knob {
    transform: translateX(16px);
  }
</style>
