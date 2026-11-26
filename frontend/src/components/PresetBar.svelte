<script lang="ts">
  import { studio } from '../lib/studioState.svelte';
  import {
    formatVerificationTooltip,
    getPresetVerification,
    getVerificationMeta,
  } from '../lib/verification';
  import PresetPicker from './PresetPicker.svelte';
  import VerificationIcon from './VerificationIcon.svelte';

  const selectedPreset = $derived(studio.selectedPreset);
  const current = $derived(getPresetVerification(selectedPreset));
  const currentMeta = $derived(getVerificationMeta(current.status));
  const modified = $derived(studio.paramsModified);
  const hasNoteOnly = $derived(current.status === 'unverified' && current.note !== '');

  const statusTooltip = $derived.by(() => {
    if (!selectedPreset) return 'Preset details & print status';
    return [
      selectedPreset.label,
      selectedPreset.description,
      formatVerificationTooltip(current.status, current.note, modified),
      'Click to view details & print status',
    ]
      .filter(Boolean)
      .join('\n');
  });
</script>

<div class="space-y-1 min-w-0">
  <div class="flex items-center justify-between gap-2">
    <span class="text-[11px] font-medium opacity-60 select-none">Preset</span>
    <div class="flex items-center gap-0.5 shrink-0">
      <button
        type="button"
        disabled={!selectedPreset}
        aria-label={selectedPreset
          ? `Preset details and print status: ${currentMeta.label}${modified ? ' (parameters modified)' : ''}`
          : 'Preset details and print status'}
        title={statusTooltip}
        class="relative btn btn-ghost btn-xs btn-square h-6 w-6 min-h-6 shrink-0 {current.status ===
          'unverified' && !hasNoteOnly
          ? 'opacity-70 hover:opacity-100'
          : modified
            ? 'opacity-55 hover:opacity-90'
            : 'opacity-95 hover:opacity-100'}"
        onclick={() => {
          studio.presetDetailsModalOpen = true;
        }}
      >
        <VerificationIcon status={current.status} class="h-3.5 w-3.5" />
        {#if modified && current.status !== 'unverified'}
          <span
            class="absolute top-0.5 right-0.5 h-1.5 w-1.5 rounded-full bg-warning ring-1 ring-base-100"
            aria-hidden="true"
          ></span>
        {:else if hasNoteOnly}
          <span
            class="absolute top-0.5 right-0.5 h-1.5 w-1.5 rounded-full bg-info ring-1 ring-base-100"
            aria-hidden="true"
          ></span>
        {/if}
      </button>

      <button
        type="button"
        class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-6 shrink-0 opacity-75 hover:opacity-100"
        aria-label={selectedPreset
          ? `Reset parameters to ${selectedPreset.label}`
          : 'Reset parameters to selected preset'}
        title={selectedPreset
          ? `Reset parameters to "${selectedPreset.label}"`
          : 'Reset parameters to selected preset'}
        onclick={() => studio.resetParams()}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-3.5 w-3.5"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" />
          <path d="M3 3v5h5" />
        </svg>
      </button>

      <button
        type="button"
        class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-6 shrink-0 opacity-75 hover:opacity-100"
        aria-label="Save preset"
        title="Save current parameters as a preset"
        onclick={() => {
          studio.savePresetModalOpen = true;
        }}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-3.5 w-3.5"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M15.2 3a2 2 0 0 1 1.4.6l3.8 3.8a2 2 0 0 1 .6 1.4V19a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z" />
          <path d="M17 21v-7a1 1 0 0 0-1-1H8a1 1 0 0 0-1 1v7" />
          <path d="M7 3v4a1 1 0 0 0 1 1h7" />
        </svg>
      </button>

      <button
        type="button"
        class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-6 shrink-0 opacity-75 hover:opacity-100"
        aria-label="Manage presets"
        title="Manage presets & source file defaults"
        onclick={() => {
          studio.managePresetsModalOpen = true;
        }}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-3.5 w-3.5"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <circle cx="12" cy="12" r="1" />
          <circle cx="19" cy="12" r="1" />
          <circle cx="5" cy="12" r="1" />
        </svg>
      </button>
    </div>
  </div>

  <PresetPicker
    presets={studio.presets}
    selectedKey={studio.selectedPresetKey}
    defaultKey={studio.defaultPresetKey}
    modified={studio.paramsModified}
    onSelect={(key) => studio.selectPreset(key)}
  />
</div>
