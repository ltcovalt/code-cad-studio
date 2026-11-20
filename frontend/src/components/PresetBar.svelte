<script lang="ts">
  import { studio } from '../lib/studioState.svelte';

  let drawerOpen = $state(false);
  let label = $state('');
  let description = $state('');
  let scope = $state<'user' | 'project'>('user');
  let saving = $state(false);
  let savedMessage = $state('');
  let saveError = $state('');

  async function handleSave() {
    if (!label.trim()) {
      saveError = 'Preset name is required';
      return;
    }
    saving = true;
    saveError = '';
    savedMessage = '';
    try {
      const msg = await studio.savePreset(label.trim(), description.trim(), scope);
      if (msg) {
        savedMessage = msg;
        label = '';
        description = '';
        drawerOpen = false;
      }
    } catch (err) {
      saveError = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }
</script>

<div class="space-y-2">
  <div class="flex items-center gap-1.5">
    <select
      aria-label="Preset"
      class="select select-bordered select-xs flex-1"
      value={studio.selectedPresetKey}
      onchange={(e) => studio.selectPreset((e.currentTarget as HTMLSelectElement).value)}
    >
      {#each studio.presets as preset (preset.key)}
        <option value={preset.key}>
          {preset.label}{preset.source === 'user' ? ' (local)' : ''}
        </option>
      {/each}
    </select>

    <button
      type="button"
      class="btn btn-ghost btn-xs"
      title="Reset all parameters to canonical dataclass defaults"
      onclick={() => {
        savedMessage = '';
        studio.resetParams();
      }}
    >
      Reset
    </button>

    <button
      type="button"
      class="btn btn-outline btn-xs"
      title="Save current parameters as a preset"
      onclick={() => {
        drawerOpen = !drawerOpen;
        saveError = '';
      }}
    >
      Save
    </button>
  </div>

  {#if savedMessage}
    <div class="text-[11px] text-success">{savedMessage}</div>
  {/if}

  {#if drawerOpen}
    <div class="p-2 rounded-box bg-base-200 border border-base-300 space-y-1.5">
      <input
        type="text"
        bind:value={label}
        placeholder="Preset name (e.g. Prusa 12.7mm)"
        class="input input-bordered input-xs w-full"
      />
      <input
        type="text"
        bind:value={description}
        placeholder="Short description (optional)"
        class="input input-bordered input-xs w-full"
      />
      {#if saveError}
        <div class="text-[11px] text-error">{saveError}</div>
      {/if}
      <div class="flex items-center justify-between gap-2">
        <select bind:value={scope} class="select select-bordered select-xs">
          <option value="user">User local (~/.config)</option>
          <option value="project">Project presets.json</option>
        </select>
        <button
          type="button"
          class="btn btn-primary btn-xs"
          disabled={saving}
          onclick={handleSave}
        >
          {#if saving}
            <span class="loading loading-spinner loading-xs"></span>
          {:else}
            Save Preset
          {/if}
        </button>
      </div>
    </div>
  {/if}
</div>
