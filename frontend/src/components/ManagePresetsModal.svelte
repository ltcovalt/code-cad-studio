<script lang="ts">
  import { untrack } from 'svelte';
  import { studio } from '../lib/studioState.svelte';
  import type { Preset, VerificationStatus } from '../lib/types';
  import {
    VERIFICATION_STATUSES,
    formatVerificationTooltip,
    getPresetVerification,
    getVerificationMeta,
  } from '../lib/verification';
  import Alert from './Alert.svelte';
  import VerificationIcon from './VerificationIcon.svelte';

  let dialogEl: HTMLDialogElement | undefined = $state();
  let pressedOutside = false;

  let actionBusy = $state(false);
  let manageError = $state('');
  let manageSuccess = $state('');

  // Inline rename state
  let editingKey = $state<string | null>(null);
  let editLabel = $state('');
  let editDescription = $state('');

  // Inline verification state
  let verifyingKey = $state<string | null>(null);
  let verifyStatus = $state<VerificationStatus>('unverified');
  let verifyNote = $state('');

  // Inline delete confirm state
  let deletingKey = $state<string | null>(null);

  const savedPresets = $derived(
    studio.presets.filter((p) => p.key !== 'default' && (p.source === 'user' || p.source === 'project'))
  );

  const canonicalDefault = $derived(
    studio.presets.find((p) => p.key === 'default') ?? null
  );

  function initializeState() {
    editingKey = null;
    verifyingKey = null;
    deletingKey = null;
    manageError = '';
    manageSuccess = '';
  }

  let dialogBodyEl: HTMLDivElement | undefined = $state();
  let wasOpen = false;
  $effect(() => {
    const el = dialogEl;
    const isOpen = studio.managePresetsModalOpen && Boolean(studio.activeModel);
    if (!el) return;
    if (isOpen && !wasOpen) {
      wasOpen = true;
      untrack(() => initializeState());
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

  function closeModal() {
    if (actionBusy) return;
    studio.managePresetsModalOpen = false;
  }

  function handleDialogKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      if (editingKey) {
        e.preventDefault();
        e.stopPropagation();
        editingKey = null;
        return;
      }
      if (verifyingKey) {
        e.preventDefault();
        e.stopPropagation();
        verifyingKey = null;
        return;
      }
      if (deletingKey) {
        e.preventDefault();
        e.stopPropagation();
        deletingKey = null;
      }
    }
  }

  function handleDialogCancel(e: Event) {
    if (actionBusy) {
      e.preventDefault();
      return;
    }
    if (editingKey) {
      e.preventDefault();
      editingKey = null;
      return;
    }
    if (verifyingKey) {
      e.preventDefault();
      verifyingKey = null;
      return;
    }
    if (deletingKey) {
      e.preventDefault();
      deletingKey = null;
    }
  }

  function handleDialogClose() {
    if (dialogEl) {
      dialogEl.returnValue = '';
    }
    wasOpen = false;
    pressedOutside = false;
    if (studio.managePresetsModalOpen) {
      studio.managePresetsModalOpen = false;
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

  function startRename(preset: Preset) {
    deletingKey = null;
    verifyingKey = null;
    manageError = '';
    manageSuccess = '';
    editingKey = preset.key;
    editLabel = preset.label;
    editDescription = preset.description;
  }

  function startVerify(preset: Preset) {
    editingKey = null;
    deletingKey = null;
    manageError = '';
    manageSuccess = '';
    const ver = getPresetVerification(preset);
    verifyingKey = preset.key;
    verifyStatus = ver.status;
    verifyNote = ver.note;
  }

  async function submitVerify(preset: Preset) {
    actionBusy = true;
    manageError = '';
    manageSuccess = '';
    try {
      await studio.updatePresetVerification(preset.key, preset.source, verifyStatus, verifyNote);
      verifyingKey = null;
      const meta = getVerificationMeta(verifyStatus);
      manageSuccess = `Updated verification for "${preset.label}" (${meta.label}).`;
    } catch (err) {
      manageError = err instanceof Error ? err.message : String(err);
    } finally {
      actionBusy = false;
    }
  }

  async function submitRename(preset: Preset) {
    const trimmed = editLabel.trim();
    if (!trimmed) {
      manageError = 'Preset name is required.';
      return;
    }
    actionBusy = true;
    manageError = '';
    manageSuccess = '';
    try {
      await studio.renamePreset(preset.key, preset.source, trimmed, editDescription.trim());
      editingKey = null;
      manageSuccess = `Renamed preset to "${trimmed}".`;
    } catch (err) {
      manageError = err instanceof Error ? err.message : String(err);
    } finally {
      actionBusy = false;
    }
  }

  async function handleToggleDefault(preset: Preset) {
    actionBusy = true;
    manageError = '';
    manageSuccess = '';
    try {
      if (preset.key === 'default') {
        await studio.setDefaultPreset('');
        manageSuccess = 'Model default reset to base preset.';
      } else if (studio.defaultPresetKey === preset.key) {
        await studio.setDefaultPreset('');
        manageSuccess = 'Model default reset to base preset.';
      } else {
        await studio.setDefaultPreset(preset.key);
        manageSuccess = `"${preset.label}" set as model default.`;
      }
    } catch (err) {
      manageError = err instanceof Error ? err.message : String(err);
    } finally {
      actionBusy = false;
    }
  }

  async function handleConfirmDelete(preset: Preset) {
    actionBusy = true;
    manageError = '';
    manageSuccess = '';
    try {
      await studio.deletePreset(preset.key, preset.source);
      deletingKey = null;
      if (editingKey === preset.key) {
        editingKey = null;
      }
      if (verifyingKey === preset.key) {
        verifyingKey = null;
      }
      manageSuccess = `Deleted "${preset.label}".`;
    } catch (err) {
      manageError = err instanceof Error ? err.message : String(err);
    } finally {
      actionBusy = false;
    }
  }
</script>

<dialog
  bind:this={dialogEl}
  class="studio-dialog"
  aria-labelledby="manage-presets-modal-title"
  {...({ closedby: actionBusy ? 'none' : 'any' } as Record<string, unknown>)}
  onkeydown={handleDialogKeydown}
  oncancel={handleDialogCancel}
  onclose={handleDialogClose}
  onpointerdown={handleDialogPointerDown}
  onpointerup={handleDialogPointerUp}
>
  {#if studio.activeModel}
    <div
      bind:this={dialogBodyEl}
      tabindex="-1"
      class="bg-base-100 border border-base-300 rounded-box shadow-md w-[32rem] max-w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto p-6 space-y-4 outline-none"
    >
      <div class="flex items-center justify-between">
        <div>
          <h2 id="manage-presets-modal-title" class="font-bold text-base">Manage Presets</h2>
          <p class="text-[11px] opacity-60">{studio.activeModel.name}</p>
        </div>
        <button
          type="button"
          class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
          title="Close"
          aria-label="Close manage presets dialog"
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

      <div class="space-y-2.5">
        {#if canonicalDefault}
            {@const isDefaultActive = studio.defaultPresetKey === ''}
            {@const defaultVer = getPresetVerification(canonicalDefault)}
            {@const defaultVerLabel = getVerificationMeta(defaultVer.status).label}
            <div
              class="rounded-box bg-base-200/60 border border-base-300 px-3 py-2 space-y-2"
            >
              {#if verifyingKey === canonicalDefault.key}
                <form
                  class="space-y-2"
                  onsubmit={(e) => {
                    e.preventDefault();
                    void submitVerify(canonicalDefault);
                  }}
                >
                  <div class="flex items-center gap-1.5">
                    <span class="text-xs font-semibold truncate">{canonicalDefault.label}</span>
                    <span
                      class="badge badge-xs badge-secondary badge-soft shrink-0"
                      title="Base defaults defined in the model's Python source file"
                    >
                      Base
                    </span>
                  </div>
                  <div class="grid grid-cols-2 gap-1.5">
                    {#each VERIFICATION_STATUSES as opt (opt.value)}
                      {@const active = verifyStatus === opt.value}
                      <button
                        type="button"
                        aria-pressed={active}
                        title={opt.hint}
                        class="flex items-center gap-1.5 px-2 py-1 rounded-field border text-left text-[11px] cursor-pointer focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary {active
                          ? `${opt.selectedButtonClass} font-semibold`
                          : 'border-base-300 hover:border-base-content/30 bg-base-100'}"
                        onclick={() => {
                          verifyStatus = opt.value;
                        }}
                      >
                        <VerificationIcon status={opt.value} class="h-3.5 w-3.5" />
                        <span class="truncate">{opt.label}</span>
                      </button>
                    {/each}
                  </div>
                  <textarea
                    bind:value={verifyNote}
                    rows="2"
                    placeholder="Optional print notes (material, tolerances, fit feedback...)"
                    class="textarea textarea-bordered textarea-xs w-full text-xs leading-snug bg-base-100"
                  ></textarea>
                  <div class="flex justify-end gap-1.5">
                    <button
                      type="button"
                      class="btn btn-xs btn-ghost"
                      disabled={actionBusy}
                      onclick={() => {
                        verifyingKey = null;
                      }}
                    >
                      Cancel
                    </button>
                    <button type="submit" class="btn btn-xs btn-primary" disabled={actionBusy}>
                      Save
                    </button>
                  </div>
                </form>
              {:else}
                <div class="flex items-center justify-between gap-2">
                  <div class="min-w-0">
                    <div class="flex items-center gap-1.5 min-w-0">
                      <span class="text-xs font-medium truncate">{canonicalDefault.label}</span>
                      <span
                        class="badge badge-xs badge-secondary badge-soft shrink-0"
                        title="Base defaults defined in the model's Python source file"
                      >
                        Base
                      </span>
                      {#if isDefaultActive}
                        <span class="badge badge-xs badge-primary badge-soft shrink-0">Default</span>
                      {/if}
                    </div>
                    {#if canonicalDefault.description}
                      <p class="text-[11px] opacity-60 truncate">{canonicalDefault.description}</p>
                    {/if}
                    {#if defaultVer.note}
                      <p class="text-[11px] opacity-70 italic truncate" title={defaultVer.note}>
                        {defaultVer.note}
                      </p>
                    {/if}
                  </div>
                  <div class="flex items-center gap-0.5 shrink-0">
                    <button
                      type="button"
                      class="btn btn-ghost btn-xs btn-square {defaultVer.status === 'unverified' && !defaultVer.note
                        ? 'opacity-60 hover:opacity-100'
                        : 'opacity-90 hover:opacity-100'}"
                      disabled={actionBusy}
                      title={formatVerificationTooltip(defaultVer.status, defaultVer.note)}
                      aria-label={`Verification (${defaultVerLabel}): edit for ${canonicalDefault.label}`}
                      onclick={() => startVerify(canonicalDefault)}
                    >
                      <VerificationIcon status={defaultVer.status} class="h-3.5 w-3.5" />
                    </button>
                    <button
                      type="button"
                      class={`btn btn-ghost btn-xs btn-square shrink-0 ${
                        isDefaultActive ? 'text-primary' : 'opacity-60 hover:opacity-100 hover:text-primary'
                      }`}
                      disabled={actionBusy || isDefaultActive}
                      title={isDefaultActive
                        ? 'Active model default'
                        : 'Set base preset as model default'}
                      aria-label={isDefaultActive
                        ? 'Active model default'
                        : 'Set base preset as model default'}
                      onclick={() => handleToggleDefault(canonicalDefault)}
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
                        {#if isDefaultActive}
                          <path d="M12 17v5" />
                          <path
                            d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z"
                            fill="currentColor"
                          />
                        {:else}
                          <path d="M12 17v5" />
                          <path
                            d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z"
                          />
                        {/if}
                      </svg>
                    </button>
                  </div>
                </div>
              {/if}
            </div>
          {/if}

          {#if savedPresets.length === 0}
            <div class="rounded-box bg-base-200/40 border border-base-300/70 p-3 text-xs opacity-65 text-center">
              No saved presets yet. Use the Save button in the toolbar to create one.
            </div>
          {:else}
            <div class="space-y-1.5">
              {#each savedPresets as preset (preset.key)}
                {@const isPinned = studio.defaultPresetKey === preset.key}
                {@const ver = getPresetVerification(preset)}
                {@const verLabel = getVerificationMeta(ver.status).label}
                <div class="rounded-box bg-base-200/40 border border-base-300 px-3 py-2 space-y-2">
                  {#if editingKey === preset.key}
                    <form
                      class="space-y-2"
                      onsubmit={(e) => {
                        e.preventDefault();
                        void submitRename(preset);
                      }}
                    >
                      <input
                        type="text"
                        bind:value={editLabel}
                        placeholder="Preset name"
                        class="input input-bordered input-xs w-full"
                      />
                      <input
                        type="text"
                        bind:value={editDescription}
                        placeholder="Optional short note shown on hover"
                        class="input input-bordered input-xs w-full"
                      />
                      <div class="flex justify-end gap-1.5">
                        <button
                          type="button"
                          class="btn btn-xs btn-ghost"
                          disabled={actionBusy}
                          onclick={() => {
                            editingKey = null;
                          }}
                        >
                          Cancel
                        </button>
                        <button type="submit" class="btn btn-xs btn-primary" disabled={actionBusy}>
                          Save
                        </button>
                      </div>
                    </form>
                  {:else if verifyingKey === preset.key}
                    <form
                      class="space-y-2"
                      onsubmit={(e) => {
                        e.preventDefault();
                        void submitVerify(preset);
                      }}
                    >
                      <div class="flex items-center gap-1.5">
                        <span class="text-xs font-semibold truncate">{preset.label}</span>
                        <span class="badge badge-xs badge-soft shrink-0">
                          {preset.source === 'project' ? 'Project' : 'User'}
                        </span>
                      </div>
                      <div class="grid grid-cols-2 gap-1.5">
                        {#each VERIFICATION_STATUSES as opt (opt.value)}
                          {@const active = verifyStatus === opt.value}
                          <button
                            type="button"
                            aria-pressed={active}
                            title={opt.hint}
                            class="flex items-center gap-1.5 px-2 py-1 rounded-field border text-left text-[11px] cursor-pointer focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary {active
                              ? `${opt.selectedButtonClass} font-semibold`
                              : 'border-base-300 hover:border-base-content/30 bg-base-100'}"
                            onclick={() => {
                              verifyStatus = opt.value;
                            }}
                          >
                            <VerificationIcon status={opt.value} class="h-3.5 w-3.5" />
                            <span class="truncate">{opt.label}</span>
                          </button>
                        {/each}
                      </div>
                      <textarea
                        bind:value={verifyNote}
                        rows="2"
                        placeholder="Optional print notes (material, tolerances, fit feedback...)"
                        class="textarea textarea-bordered textarea-xs w-full text-xs leading-snug bg-base-100"
                      ></textarea>
                      <div class="flex justify-end gap-1.5">
                        <button
                          type="button"
                          class="btn btn-xs btn-ghost"
                          disabled={actionBusy}
                          onclick={() => {
                            verifyingKey = null;
                          }}
                        >
                          Cancel
                        </button>
                        <button type="submit" class="btn btn-xs btn-primary" disabled={actionBusy}>
                          Save
                        </button>
                      </div>
                    </form>
                  {:else}
                    <div class="flex items-center justify-between gap-2">
                      <div class="min-w-0">
                        <div class="flex items-center gap-1.5 min-w-0">
                          <span class="text-xs font-medium truncate">{preset.label}</span>
                          <span class="badge badge-xs badge-soft shrink-0">
                            {preset.source === 'project' ? 'Project' : 'User'}
                          </span>
                          {#if isPinned}
                            <span class="badge badge-xs badge-primary badge-soft shrink-0">
                              Default
                            </span>
                          {/if}
                        </div>
                        {#if preset.description}
                          <p class="text-[11px] opacity-60 truncate">{preset.description}</p>
                        {/if}
                        {#if ver.note}
                          <p class="text-[11px] opacity-70 italic truncate" title={ver.note}>
                            {ver.note}
                          </p>
                        {/if}
                      </div>

                      <div class="flex items-center gap-0.5 shrink-0">
                        {#if deletingKey === preset.key}
                          <button
                            type="button"
                            class="btn btn-xs btn-error"
                            disabled={actionBusy}
                            onclick={() => handleConfirmDelete(preset)}
                          >
                            Delete
                          </button>
                          <button
                            type="button"
                            class="btn btn-xs btn-ghost"
                            disabled={actionBusy}
                            onclick={() => {
                              deletingKey = null;
                            }}
                          >
                            Cancel
                          </button>
                        {:else}
                          <button
                            type="button"
                            class="btn btn-ghost btn-xs btn-square {ver.status === 'unverified' && !ver.note
                              ? 'opacity-60 hover:opacity-100'
                              : 'opacity-90 hover:opacity-100'}"
                            disabled={actionBusy}
                            title={formatVerificationTooltip(ver.status, ver.note)}
                            aria-label={`Verification (${verLabel}): edit for ${preset.label}`}
                            onclick={() => startVerify(preset)}
                          >
                            <VerificationIcon status={ver.status} class="h-3.5 w-3.5" />
                          </button>

                          <button
                            type="button"
                            class={`btn btn-ghost btn-xs btn-square ${
                              isPinned
                                ? 'text-primary'
                                : 'opacity-60 hover:opacity-100 hover:text-primary'
                            }`}
                            disabled={actionBusy}
                            title={isPinned
                              ? 'Active model default (click to reset to base)'
                              : 'Set preset as model default'}
                            aria-label={isPinned
                              ? 'Active model default'
                              : `Set ${preset.label} as model default`}
                            onclick={() => handleToggleDefault(preset)}
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
                              {#if isPinned}
                                <path d="M12 17v5" />
                                <path
                                  d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z"
                                  fill="currentColor"
                                />
                              {:else}
                                <path d="M12 17v5" />
                                <path
                                  d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z"
                                />
                              {/if}
                            </svg>
                          </button>

                          <button
                            type="button"
                            class="btn btn-ghost btn-xs btn-square opacity-75 hover:opacity-100"
                            disabled={actionBusy}
                            title="Rename preset"
                            aria-label={`Rename ${preset.label}`}
                            onclick={() => startRename(preset)}
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
                              <path d="M12 20h9" />
                              <path
                                d="M16.376 3.622a1 1 0 0 1 3.002 3.002L7.368 18.635a2 2 0 0 1-.855.506l-2.872.838a.5.5 0 0 1-.62-.62l.838-2.872a2 2 0 0 1 .506-.854z"
                              />
                            </svg>
                          </button>

                          <button
                            type="button"
                            class="btn btn-ghost btn-xs btn-square text-error/80 hover:text-error"
                            disabled={actionBusy}
                            title="Delete preset"
                            aria-label={`Delete ${preset.label}`}
                            onclick={() => {
                              editingKey = null;
                              verifyingKey = null;
                              deletingKey = preset.key;
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
                              <path d="M3 6h18" />
                              <path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6" />
                              <path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" />
                            </svg>
                          </button>
                        {/if}
                      </div>
                    </div>
                  {/if}
                </div>
              {/each}
            </div>
          {/if}

          {#if manageError}
            <Alert variant="error">{manageError}</Alert>
          {/if}

          {#if manageSuccess}
            <Alert variant="success">{manageSuccess}</Alert>
          {/if}

          <div class="flex justify-end pt-2">
            <button type="button" class="btn btn-sm btn-ghost" onclick={closeModal}>Close</button>
          </div>
        </div>
    </div>
  {/if}
</dialog>
