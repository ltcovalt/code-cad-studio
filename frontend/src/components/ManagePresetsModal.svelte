<script lang="ts">
  import { studio } from '../lib/studioState.svelte';
  import type { Preset, UpdateDefaultsResponse } from '../lib/types';

  type ManageTab = 'presets' | 'source';

  let activeTab = $state<ManageTab>('presets');
  let actionBusy = $state(false);
  let manageError = $state('');
  let manageSuccess = $state('');

  // Inline rename state
  let editingKey = $state<string | null>(null);
  let editLabel = $state('');
  let editDescription = $state('');

  // Inline delete confirm state
  let deletingKey = $state<string | null>(null);

  // Update source file state
  let previewLoading = $state(false);
  let previewData = $state<UpdateDefaultsResponse | null>(null);
  let previewError = $state('');
  let applyingDefaults = $state(false);
  let defaultsSuccess = $state('');

  const savedPresets = $derived(
    studio.presets.filter((p) => p.key !== 'default' && (p.source === 'user' || p.source === 'project'))
  );

  const canonicalDefault = $derived(
    studio.presets.find((p) => p.key === 'default') ?? null
  );

  $effect(() => {
    if (studio.managePresetsModalOpen) {
      activeTab = 'presets';
      editingKey = null;
      deletingKey = null;
      manageError = '';
      manageSuccess = '';
      previewData = null;
      previewError = '';
      defaultsSuccess = '';
    }
  });

  function closeModal() {
    studio.managePresetsModalOpen = false;
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && studio.managePresetsModalOpen) {
      if (editingKey) {
        editingKey = null;
        return;
      }
      closeModal();
    }
  }

  async function loadDefaultsPreview() {
    previewLoading = true;
    previewError = '';
    defaultsSuccess = '';
    try {
      const resp = await studio.previewUpdateDefaults();
      if (!resp.ok) {
        previewData = null;
        previewError = resp.error || 'Failed to inspect model defaults';
      } else {
        previewData = resp;
      }
    } catch (err) {
      previewData = null;
      previewError = err instanceof Error ? err.message : String(err);
    } finally {
      previewLoading = false;
    }
  }

  function switchTab(tab: ManageTab) {
    activeTab = tab;
    if (tab === 'source') {
      void loadDefaultsPreview();
    }
  }

  function startRename(preset: Preset) {
    deletingKey = null;
    manageError = '';
    manageSuccess = '';
    editingKey = preset.key;
    editLabel = preset.label;
    editDescription = preset.description;
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
      manageSuccess = `Deleted "${preset.label}".`;
    } catch (err) {
      manageError = err instanceof Error ? err.message : String(err);
    } finally {
      actionBusy = false;
    }
  }

  async function handleApplyDefaults() {
    if (!previewData || previewData.unsupported.length > 0 || previewData.changes.length === 0) {
      return;
    }
    applyingDefaults = true;
    previewError = '';
    defaultsSuccess = '';
    try {
      const resp = await studio.applyUpdateDefaults(previewData.sha256 ?? '');
      defaultsSuccess = `Updated ${resp.updated ?? previewData.changes.length} default(s) in ${resp.rel_path || previewData.rel_path || 'source file'}.`;
      previewData = {
        ...resp,
        changes: [],
        unsupported: [],
      };
    } catch (err) {
      previewError = err instanceof Error ? err.message : String(err);
      void loadDefaultsPreview();
    } finally {
      applyingDefaults = false;
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

{#if studio.managePresetsModalOpen && studio.activeModel}
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
      aria-labelledby="manage-presets-modal-title"
      tabindex="-1"
      class="bg-base-100 border border-base-300 rounded-box shadow-md w-full max-w-lg max-h-[90vh] overflow-y-auto p-6 space-y-4"
      onclick={(e) => e.stopPropagation()}
    >
      <div class="flex items-center justify-between">
        <div>
          <h3 id="manage-presets-modal-title" class="font-bold text-base">Manage Presets</h3>
          <p class="text-[11px] opacity-60">{studio.activeModel.name}</p>
        </div>
        <button
          type="button"
          class="btn btn-sm btn-circle btn-ghost"
          aria-label="Close manage presets dialog"
          onclick={closeModal}
        >
          ✕
        </button>
      </div>

      <!-- Tabs -->
      <div role="tablist" class="tabs tabs-box tabs-sm">
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === 'presets'}
          class="tab flex-1"
          class:tab-active={activeTab === 'presets'}
          onclick={() => switchTab('presets')}
        >
          Presets
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === 'source'}
          class="tab flex-1"
          class:tab-active={activeTab === 'source'}
          onclick={() => switchTab('source')}
        >
          Update Source File
        </button>
      </div>

      {#if activeTab === 'presets'}
        <div class="space-y-2.5">
          {#if canonicalDefault}
            {@const isDefaultActive = studio.defaultPresetKey === ''}
            <div
              class="flex items-center justify-between gap-2 rounded-box bg-base-200/60 border border-base-300 px-3 py-2"
            >
              <div class="min-w-0">
                <div class="flex items-center gap-1.5 min-w-0">
                  <span class="text-xs font-medium truncate">{canonicalDefault.label}</span>
                  <span class="badge badge-xs badge-secondary badge-soft shrink-0">Model</span>
                  {#if isDefaultActive}
                    <span class="badge badge-xs badge-primary badge-soft shrink-0">Default</span>
                  {/if}
                </div>
                {#if canonicalDefault.description}
                  <p class="text-[11px] opacity-60 truncate">{canonicalDefault.description}</p>
                {/if}
              </div>
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
                    <circle cx="12" cy="12" r="10" fill="currentColor" stroke="none" />
                    <path d="m9 12 2 2 4-4" stroke="var(--color-primary-content, #fff)" stroke-width="2.25" />
                  {:else}
                    <circle cx="12" cy="12" r="10" />
                    <path d="m9 12 2 2 4-4" />
                  {/if}
                </svg>
              </button>
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
                                <circle cx="12" cy="12" r="10" fill="currentColor" stroke="none" />
                                <path
                                  d="m9 12 2 2 4-4"
                                  stroke="var(--color-primary-content, #fff)"
                                  stroke-width="2.25"
                                />
                              {:else}
                                <circle cx="12" cy="12" r="10" />
                                <path d="m9 12 2 2 4-4" />
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
            <div class="alert alert-error alert-soft text-xs py-2">
              <span>{manageError}</span>
            </div>
          {/if}

          {#if manageSuccess}
            <div class="alert alert-success alert-soft text-xs py-2">
              <span>{manageSuccess}</span>
            </div>
          {/if}

          <div class="flex justify-end pt-2">
            <button type="button" class="btn btn-sm btn-ghost" onclick={closeModal}>Close</button>
          </div>
        </div>
      {:else}
        <!-- Update Source File Tab -->
        <div class="space-y-3">
          <div class="alert alert-warning alert-soft text-xs py-2.5 block space-y-1">
            <div class="font-semibold">Advanced: modifies source script</div>
            <p class="opacity-90">
              Overwrites the default values in
              <code class="font-mono text-[11px]">{studio.activeModel.rel_path}</code>
              using your current parameters.
            </p>
          </div>

          {#if previewLoading}
            <div class="flex items-center gap-2 text-xs opacity-70 py-4 justify-center">
              <span class="loading loading-spinner loading-xs"></span>
              <span>Checking for changes...</span>
            </div>
          {:else if previewData}
            {#if previewData.unsupported.length > 0}
              <div class="alert alert-warning alert-soft text-xs py-2.5 block space-y-1">
                <div class="font-semibold">
                  Some changed parameters can't be updated automatically
                </div>
                <ul class="list-disc pl-4 space-y-0.5 text-[11px]">
                  {#each previewData.unsupported as item (item.path)}
                    <li>
                      <code class="font-mono">{item.path}</code>: {item.reason}
                    </li>
                  {/each}
                </ul>
              </div>
            {/if}

            {#if previewData.changes.length > 0}
              <div class="rounded-box border border-base-300 overflow-hidden">
                <table class="table table-xs w-full">
                  <thead>
                    <tr class="bg-base-200/70">
                      <th>Parameter</th>
                      <th>Line</th>
                      <th>Change</th>
                    </tr>
                  </thead>
                  <tbody class="font-mono text-[11px]">
                    {#each previewData.changes as ch (ch.path)}
                      <tr>
                        <td class="break-all">{ch.path}</td>
                        <td class="opacity-60">{ch.line}</td>
                        <td>
                          <span class="opacity-60 line-through">{ch.old_literal}</span>
                          <span class="mx-1 opacity-50">→</span>
                          <span class="text-primary font-semibold">{ch.new_literal}</span>
                        </td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {:else if previewData.unsupported.length === 0 && !defaultsSuccess}
              <div class="rounded-box bg-base-200/70 p-3 text-xs opacity-75">
                Current parameters already match the defaults in
                <code class="font-mono">{previewData.rel_path}</code>.
              </div>
            {/if}
          {/if}

          {#if previewError}
            <div class="alert alert-error alert-soft text-xs py-2">
              <span>{previewError}</span>
            </div>
          {/if}

          {#if defaultsSuccess}
            <div class="alert alert-success alert-soft text-xs py-2">
              <span>{defaultsSuccess}</span>
            </div>
          {/if}

          <div class="flex justify-end gap-2 pt-2">
            <button type="button" class="btn btn-sm btn-ghost" onclick={closeModal}>Close</button>
            <button
              type="button"
              class="btn btn-sm btn-primary"
              disabled={previewLoading ||
                applyingDefaults ||
                !previewData ||
                previewData.changes.length === 0 ||
                previewData.unsupported.length > 0}
              onclick={handleApplyDefaults}
            >
              {#if applyingDefaults}
                <span class="loading loading-spinner loading-xs"></span>
              {:else}
                Update Source File
              {/if}
            </button>
          </div>
        </div>
      {/if}
    </div>
  </div>
{/if}
