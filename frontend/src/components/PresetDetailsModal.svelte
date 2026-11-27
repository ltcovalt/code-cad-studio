<script lang="ts">
  import { untrack } from 'svelte';
  import { studio } from '../lib/studioState.svelte';
  import type { VerificationStatus } from '../lib/types';
  import {
    VERIFICATION_STATUSES,
    getPresetVerification,
  } from '../lib/verification';
  import Alert from './Alert.svelte';
  import VerificationIcon from './VerificationIcon.svelte';

  let dialogEl: HTMLDialogElement | undefined = $state();
  let dialogBodyEl: HTMLDivElement | undefined = $state();
  let pressedOutside = false;

  let draftLabel = $state('');
  let draftDescription = $state('');
  let draftStatus = $state<VerificationStatus>('unverified');
  let draftNote = $state('');
  let draftIsDefault = $state(false);
  let saving = $state(false);
  let formError = $state('');

  const model = $derived(studio.activeModel);
  const preset = $derived(studio.selectedPreset);
  const current = $derived(getPresetVerification(preset));
  const modified = $derived(studio.paramsModified);

  const canEditMeta = $derived(
    Boolean(preset && preset.key !== 'default' && (preset.source === 'project' || preset.source === 'user'))
  );

  const isStartupDefault = $derived(
    Boolean(
      preset &&
        (studio.defaultPresetKey !== '' ? preset.key === studio.defaultPresetKey : preset.key === 'default')
    )
  );
  const isBaseActiveDefault = $derived(
    Boolean(preset && preset.key === 'default' && isStartupDefault)
  );

  const metaDirty = $derived(
    Boolean(
      preset &&
        canEditMeta &&
        (draftLabel.trim() !== preset.label || draftDescription.trim() !== preset.description)
    )
  );
  const verDirty = $derived(draftStatus !== current.status || draftNote.trim() !== current.note);
  const defaultDirty = $derived(draftIsDefault !== isStartupDefault);
  const isDirty = $derived(metaDirty || verDirty || defaultDirty);

  function initializeForm() {
    draftLabel = preset?.label ?? '';
    draftDescription = preset?.description ?? '';
    draftStatus = current.status;
    draftNote = current.note;
    draftIsDefault = isStartupDefault;
    formError = '';
  }

  let wasOpen = false;
  $effect(() => {
    const el = dialogEl;
    const isOpen = studio.presetDetailsModalOpen && Boolean(model && preset);
    if (!el) return;
    if (isOpen && !wasOpen) {
      wasOpen = true;
      untrack(() => initializeForm());
      if (!el.open) {
        el.showModal();
      }
      dialogBodyEl?.focus({ preventScroll: true });
    } else if (!isOpen && wasOpen) {
      wasOpen = false;
      if (el.open) {
        el.close();
      }
    }
  });

  // Close if active model or selected preset changes underneath while open
  let prevModelId = studio.activeModelId;
  let prevPresetKey = studio.selectedPresetKey;
  $effect(() => {
    const nextModelId = studio.activeModelId;
    const nextPresetKey = studio.selectedPresetKey;
    if (nextModelId !== prevModelId || nextPresetKey !== prevPresetKey) {
      prevModelId = nextModelId;
      prevPresetKey = nextPresetKey;
      if (!saving && studio.presetDetailsModalOpen) {
        studio.presetDetailsModalOpen = false;
      }
    }
  });

  function closeModal() {
    if (saving) return;
    studio.presetDetailsModalOpen = false;
  }

  function handleDialogCancel(e: Event) {
    if (saving) {
      e.preventDefault();
    }
  }

  function handleDialogClose() {
    if (dialogEl) {
      dialogEl.returnValue = '';
    }
    wasOpen = false;
    pressedOutside = false;
    if (studio.presetDetailsModalOpen) {
      studio.presetDetailsModalOpen = false;
    }
  }

  function isOutsideDialog(e: PointerEvent, el: HTMLDialogElement): boolean {
    const r = el.getBoundingClientRect();
    return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
  }

  function handleDialogPointerDown(e: PointerEvent) {
    if (!dialogEl || 'closedBy' in HTMLDialogElement.prototype) return;
    pressedOutside = isOutsideDialog(e, dialogEl);
  }

  function handleDialogPointerUp(e: PointerEvent) {
    if (!dialogEl || 'closedBy' in HTMLDialogElement.prototype) return;
    if (pressedOutside && isOutsideDialog(e, dialogEl)) {
      closeModal();
    }
    pressedOutside = false;
  }

  async function handleSave(e: SubmitEvent) {
    e.preventDefault();
    if (!preset) return;
    if (canEditMeta && !draftLabel.trim()) {
      formError = 'Preset name is required.';
      return;
    }

    saving = true;
    formError = '';
    try {
      let targetKey = preset.key;
      if (metaDirty) {
        targetKey = await studio.renamePreset(
          targetKey,
          preset.source,
          draftLabel.trim(),
          draftDescription.trim()
        );
        prevPresetKey = targetKey;
      }
      if (verDirty) {
        await studio.updatePresetVerification(targetKey, preset.source, draftStatus, draftNote);
      }
      if (defaultDirty) {
        if (draftIsDefault) {
          await studio.setDefaultPreset(targetKey === 'default' ? '' : targetKey);
        } else {
          await studio.setDefaultPreset('');
        }
      }
      studio.presetDetailsModalOpen = false;
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }
</script>

<dialog
  bind:this={dialogEl}
  class="studio-dialog"
  aria-labelledby="preset-details-modal-title"
  {...({ closedby: saving ? 'none' : 'any' } as Record<string, unknown>)}
  oncancel={handleDialogCancel}
  onclose={handleDialogClose}
  onpointerdown={handleDialogPointerDown}
  onpointerup={handleDialogPointerUp}
>
  {#if model && preset}
    <div
      bind:this={dialogBodyEl}
      tabindex="-1"
      class="bg-base-100 border border-base-300 rounded-box shadow-md w-[32rem] max-w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto p-6 space-y-4 outline-none"
    >
      <!-- Header with subdued model context -->
      <div class="pb-3 border-b border-base-300/70 space-y-1">
        <div class="flex items-center justify-between gap-3">
          <h2 id="preset-details-modal-title" class="font-bold text-base">Preset Details</h2>
          <button
            type="button"
            class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
            title="Close"
            aria-label="Close preset details dialog"
            disabled={saving}
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

        <div class="text-[11px] opacity-60 space-y-0.5">
          <div class="font-medium text-base-content/90">{model.name}</div>
          {#if model.rel_path}
            <div class="font-mono break-all">
              {model.library_name ? `${model.library_name}/${model.rel_path}` : model.rel_path}
            </div>
          {/if}
        </div>
      </div>

      {#if modified}
        <Alert variant="warning" compact>
          Live parameters differ from this preset. Reset or save your changes before verifying them.
        </Alert>
      {/if}

      <form class="space-y-3.5" onsubmit={handleSave} novalidate>
        {#if canEditMeta}
          <div>
            <div class="flex items-center justify-between gap-2 mb-1">
              <label for="preset-details-name" class="text-xs font-semibold">Preset Name</label>
              <div class="flex items-center gap-1 shrink-0">
                {#if preset.source === 'project'}
                  <span class="badge badge-xs badge-soft">Project</span>
                {:else}
                  <span class="badge badge-xs badge-soft">User</span>
                {/if}
                {#if isStartupDefault}
                  <span class="badge badge-xs badge-primary badge-soft">Default</span>
                {/if}
                {#if modified}
                  <span class="badge badge-xs badge-warning badge-soft">Modified</span>
                {/if}
              </div>
            </div>
            <input
              id="preset-details-name"
              type="text"
              required
              bind:value={draftLabel}
              oninput={() => {
                if (formError) formError = '';
              }}
              placeholder="Preset name"
              class="input input-bordered input-sm w-full text-xs"
              class:input-error={Boolean(formError && !draftLabel.trim())}
            />
          </div>

          <div>
            <label for="preset-details-description" class="block text-xs font-semibold mb-1">
              Description
            </label>
            <textarea
              id="preset-details-description"
              bind:value={draftDescription}
              rows="2"
              placeholder="Optional description for this preset..."
              class="textarea textarea-bordered w-full text-xs leading-relaxed py-1.5"
            ></textarea>
          </div>
        {:else}
          <div class="space-y-1">
            <div class="flex flex-wrap items-center gap-1.5">
              <h3 class="text-sm font-semibold break-words leading-snug">{preset.label}</h3>
              <span
                class="badge badge-xs badge-secondary badge-soft shrink-0"
                title="Base defaults defined in the model's Python source file"
              >
                Base
              </span>
              {#if isStartupDefault}
                <span class="badge badge-xs badge-primary badge-soft shrink-0">Default</span>
              {/if}
              {#if modified}
                <span class="badge badge-xs badge-warning badge-soft shrink-0">Modified</span>
              {/if}
            </div>
            {#if preset.description}
              <p class="text-xs opacity-80 whitespace-pre-wrap break-words leading-relaxed">
                {preset.description}
              </p>
            {/if}
            <p class="text-[11px] opacity-50">
              Name and description are defined in the model's Python source (@default_preset).
            </p>
          </div>
        {/if}

        <fieldset class="space-y-1.5">
          <legend class="text-xs font-semibold">Print Status</legend>
          <div class="grid grid-cols-2 gap-2">
            {#each VERIFICATION_STATUSES as opt (opt.value)}
              {@const active = draftStatus === opt.value}
              <button
                type="button"
                aria-pressed={active}
                class="flex items-start gap-2 px-2.5 py-2 rounded-field border text-left cursor-pointer focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary {active
                  ? opt.selectedButtonClass
                  : 'border-base-300 hover:border-base-content/30 bg-base-100'}"
                onclick={() => {
                  draftStatus = opt.value;
                }}
              >
                <VerificationIcon status={opt.value} class="h-3.5 w-3.5 mt-0.5" />
                <div class="min-w-0 space-y-0.5">
                  <div class="text-xs leading-tight {active ? 'font-semibold' : 'font-medium'}">
                    {opt.label}
                  </div>
                  <div class="text-[10px] opacity-65 leading-tight">{opt.hint}</div>
                </div>
              </button>
            {/each}
          </div>
        </fieldset>

        <div>
          <label for="preset-details-verification-note" class="block text-xs font-semibold mb-1">
            Print Notes
          </label>
          <textarea
            id="preset-details-verification-note"
            bind:value={draftNote}
            rows="3"
            placeholder="Material, layer height, tolerances, fit feedback, or known issues..."
            class="textarea textarea-bordered w-full text-xs leading-relaxed py-1.5"
          ></textarea>
        </div>

        <label
          class="flex items-center gap-2 text-xs {isBaseActiveDefault
            ? 'opacity-70 cursor-default'
            : 'cursor-pointer'}"
        >
          <input
            type="checkbox"
            class="checkbox checkbox-xs checkbox-primary"
            bind:checked={draftIsDefault}
            disabled={saving || isBaseActiveDefault}
          />
          <span class="font-medium">
            Set as model default
            {#if isBaseActiveDefault}
              <span class="font-normal opacity-65">(active default)</span>
            {:else if !isStartupDefault && preset.key === 'default'}
              <span class="font-normal opacity-65">(resets default to Base)</span>
            {:else if isStartupDefault}
              <span class="font-normal opacity-65">(uncheck to reset to Base)</span>
            {/if}
          </span>
        </label>

        {#if formError}
          <Alert variant="error" compact>{formError}</Alert>
        {/if}

        <div class="flex items-center justify-end gap-2 pt-1">
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            disabled={saving}
            onclick={closeModal}
          >
            Close
          </button>
          <button
            type="submit"
            class="btn btn-sm btn-primary"
            disabled={saving || !isDirty}
          >
            {#if saving}
              <span class="loading loading-spinner loading-xs"></span>
            {:else}
              Save
            {/if}
          </button>
        </div>
      </form>
    </div>
  {/if}
</dialog>
