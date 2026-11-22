<script lang="ts">
  import { studio } from '../lib/studioState.svelte';

  let label = $state('');
  let description = $state('');
  let scope = $state<'user' | 'project'>('user');
  let sparseOnly = $state(true);
  let setAsDefault = $state(false);

  let saving = $state(false);
  let formError = $state('');

  function slugifyLabel(raw: string): string {
    const s = raw
      .toLowerCase()
      .trim()
      .replace(/[^a-z0-9]+/g, '_')
      .replace(/^_+|_+$/g, '');
    return s === '' || s === 'default' ? 'custom_preset' : s;
  }

  const matchingExisting = $derived.by(() => {
    const trimmed = label.trim();
    if (!trimmed) return null;
    const slug = slugifyLabel(trimmed);
    return studio.presets.find((p) => p.key === slug && p.source === scope) ?? null;
  });

  $effect(() => {
    if (studio.savePresetModalOpen) {
      formError = '';
      sparseOnly = true;

      const cur = studio.presets.find((p) => p.key === studio.selectedPresetKey);
      if (cur && cur.key !== 'default' && (cur.source === 'user' || cur.source === 'project')) {
        label = cur.label;
        description = cur.description;
        scope = cur.source === 'project' ? 'project' : 'user';
        setAsDefault = studio.defaultPresetKey === cur.key;
      } else {
        label = '';
        description = '';
        scope = studio.config.preset_save_scope === 'project' ? 'project' : 'user';
        setAsDefault = false;
      }
    }
  });

  function closeModal() {
    studio.savePresetModalOpen = false;
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && studio.savePresetModalOpen) {
      closeModal();
    }
  }

  async function handleSubmit(e: SubmitEvent) {
    e.preventDefault();
    const trimmed = label.trim();
    if (!trimmed) {
      formError = 'Preset name is required.';
      return;
    }
    saving = true;
    formError = '';
    try {
      const saved = await studio.savePreset(trimmed, description.trim(), scope, !sparseOnly);
      if (saved) {
        if (setAsDefault && saved.key) {
          await studio.setDefaultPreset(saved.key);
        } else if (!setAsDefault && saved.key && studio.defaultPresetKey === saved.key) {
          await studio.setDefaultPreset('');
        }
        closeModal();
      }
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

{#if studio.savePresetModalOpen && studio.activeModel}
  <!-- Lightweight zero-transition overlay to avoid WebKitGTK/Wayland software-compositing lag over WebGL -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
    onclick={closeModal}
  >
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="save-preset-modal-title"
      tabindex="-1"
      class="bg-base-100 border border-base-300 rounded-box shadow-md w-full max-w-lg max-h-[90vh] overflow-y-auto p-6 space-y-4"
      onclick={(e) => e.stopPropagation()}
    >
      <div class="flex items-center justify-between">
        <div>
          <h3 id="save-preset-modal-title" class="font-bold text-base">Save Preset</h3>
          <p class="text-[11px] opacity-60">{studio.activeModel.name}</p>
        </div>
        <button
          type="button"
          class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
          title="Close"
          aria-label="Close save preset dialog"
          onclick={closeModal}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-4 w-4"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <line x1="6" y1="6" x2="18" y2="18" />
            <line x1="18" y1="6" x2="6" y2="18" />
          </svg>
        </button>
      </div>

      <form class="space-y-3" onsubmit={handleSubmit}>
        <div class="form-control">
          <label class="label py-1" for="preset-name-input">
            <span class="label-text text-xs font-semibold">Preset Name</span>
          </label>
          <input
            id="preset-name-input"
            type="text"
            bind:value={label}
            placeholder="e.g. Gridfinity Bin - 2x2x6u"
            class="input input-bordered input-sm w-full"
          />
          {#if matchingExisting}
            <p class="text-[11px] text-warning mt-1">
              A preset with this name already exists and will be overwritten.
            </p>
          {/if}
        </div>

        <div class="form-control">
          <label class="label py-1" for="preset-desc-input">
            <span class="label-text text-xs font-semibold">Description</span>
          </label>
          <input
            id="preset-desc-input"
            type="text"
            bind:value={description}
            placeholder="Optional short note shown on hover"
            class="input input-bordered input-sm w-full"
          />
        </div>

        <fieldset class="space-y-1.5">
          <legend class="text-xs font-semibold">Preset Type</legend>
          <div class="grid grid-cols-2 gap-2">
            <label
              class={`flex items-start gap-2 p-2.5 rounded-box border cursor-pointer text-xs ${
                scope === 'user' ? 'border-primary bg-primary/5' : 'border-base-300'
              }`}
            >
              <input
                type="radio"
                name="preset-scope"
                value="user"
                bind:group={scope}
                class="radio radio-xs radio-primary mt-0.5"
              />
              <span class="leading-tight">
                <span class="font-medium block">User</span>
                <span class="text-[11px] opacity-60">Saved only for you on this device.</span>
              </span>
            </label>

            <label
              class={`flex items-start gap-2 p-2.5 rounded-box border cursor-pointer text-xs ${
                scope === 'project' ? 'border-primary bg-primary/5' : 'border-base-300'
              }`}
            >
              <input
                type="radio"
                name="preset-scope"
                value="project"
                bind:group={scope}
                class="radio radio-xs radio-primary mt-0.5"
              />
              <span class="leading-tight">
                <span class="font-medium block">Project</span>
                <span class="text-[11px] opacity-60">Saved with the project so it can be shared.</span>
              </span>
            </label>
          </div>
        </fieldset>

        <div class="rounded-box bg-base-200/70 border border-base-300 p-2.5 space-y-2">
          <label class="flex items-start gap-2 cursor-pointer text-xs">
            <input
              type="checkbox"
              bind:checked={sparseOnly}
              class="checkbox checkbox-xs checkbox-primary mt-0.5"
            />
            <span class="leading-tight">
              <span class="font-medium block">Save only changed parameters</span>
              <span class="text-[11px] opacity-60">
                Uncheck to save every setting, preventing future model updates from affecting this
                preset.
              </span>
            </span>
          </label>

          <label class="flex items-center gap-2 cursor-pointer text-xs">
            <input
              type="checkbox"
              bind:checked={setAsDefault}
              class="checkbox checkbox-xs checkbox-primary"
            />
            <span class="font-medium">Set preset as model default</span>
          </label>
        </div>

        {#if formError}
          <div class="alert alert-error alert-soft text-xs py-2">
            <span>{formError}</span>
          </div>
        {/if}

        <div class="flex justify-end gap-2 pt-2">
          <button type="button" class="btn btn-sm btn-ghost" onclick={closeModal}>Cancel</button>
          <button type="submit" class="btn btn-sm btn-primary" disabled={saving}>
            {#if saving}
              <span class="loading loading-spinner loading-xs"></span>
            {:else if matchingExisting}
              Overwrite Preset
            {:else}
              Save Preset
            {/if}
          </button>
        </div>
      </form>
    </div>
  </div>
{/if}
