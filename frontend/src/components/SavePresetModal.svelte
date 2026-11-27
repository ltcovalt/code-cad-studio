<script lang="ts">
  import { untrack } from 'svelte';
  import {
    collectComponentFields,
    extractComponentPresetSelections,
    normalizeComponentPresetsForSave,
    paramsEqual,
    resolvePresetParams,
  } from '../lib/params';
  import {
    normalizePresetLabel,
    slugifyPresetLabel,
    suggestNewPresetName,
  } from '../lib/presetNaming';
  import { pathBasename } from '../lib/paths';
  import { studio } from '../lib/studioState.svelte';
  import type { Preset, PresetVerification, UpdateDefaultsResponse } from '../lib/types';
  import { groupUnsupportedDefaults } from '../lib/updateDefaults';
  import { getPresetVerification } from '../lib/verification';
  import Alert from './Alert.svelte';

  type SaveTab = 'preset' | 'source';

  let dialogEl: HTMLDialogElement | undefined = $state();
  let nameInputEl: HTMLInputElement | undefined = $state();

  let activeTab = $state<SaveTab>('preset');

  let label = $state('');
  let description = $state('');
  let scope = $state<'user' | 'project'>('user');
  let sparseOnly = $state(true);
  let setAsDefault = $state(false);

  let saving = $state(false);
  let formError = $state('');

  // Update source file state
  let previewLoading = $state(false);
  let previewData = $state<UpdateDefaultsResponse | null>(null);
  let previewError = $state('');
  let applyingDefaults = $state(false);
  let defaultsSuccess = $state('');

  let pressedOutside = false;

  const savedPresets = $derived(
    studio.presets.filter(
      (p) => p.key !== 'default' && (p.source === 'user' || p.source === 'project')
    )
  );

  const matchingExisting = $derived.by<Preset | null>(() => {
    const trimmed = label.trim();
    if (!trimmed) return null;
    const norm = normalizePresetLabel(trimmed);
    const slug = slugifyPresetLabel(trimmed);

    const sameScope = savedPresets.filter((p) => p.source === scope);
    const otherScope = savedPresets.filter((p) => p.source !== scope);

    return (
      sameScope.find((p) => normalizePresetLabel(p.label) === norm) ??
      sameScope.find((p) => p.key === slug) ??
      otherScope.find((p) => normalizePresetLabel(p.label) === norm) ??
      otherScope.find((p) => p.key === slug) ??
      null
    );
  });

  const isSavingFromBase = $derived(studio.selectedPresetKey === 'default');

  const canonicalDefault = $derived(
    studio.presets.find((p) => p.key === 'default') ?? null
  );

  const canonicalDefaultVer = $derived(getPresetVerification(canonicalDefault));

  const unsupportedGroups = $derived(
    previewData ? groupUnsupportedDefaults(previewData.unsupported) : []
  );

  const hasPartialChanges = $derived(
    Boolean(previewData && previewData.changes.length > 0 && previewData.unsupported.length > 0)
  );

  const onlyUnsupportedChanges = $derived(
    Boolean(previewData && previewData.changes.length === 0 && previewData.unsupported.length > 0)
  );

  const hasSourceDiff = $derived(!paramsEqual(studio.params, studio.defaults));

  type MatchKind = 'overwrite' | 'override-local' | 'replace-project' | null;

  const matchKind = $derived.by<MatchKind>(() => {
    if (!matchingExisting) return null;
    if (matchingExisting.source === scope) return 'overwrite';
    if (matchingExisting.source === 'project' && scope === 'user') return 'override-local';
    return 'replace-project';
  });

  const matchedByKeyOnly = $derived(
    Boolean(
      matchingExisting &&
        normalizePresetLabel(matchingExisting.label) !== normalizePresetLabel(label)
    )
  );

  const matchingParamsChanged = $derived.by(() => {
    if (!matchingExisting) return false;
    const existingParams = resolvePresetParams(
      studio.defaults,
      studio.fields,
      matchingExisting,
      studio.components
    );
    const existingCompPresets = normalizeComponentPresetsForSave(
      extractComponentPresetSelections(matchingExisting, studio.fields),
      existingParams,
      studio.defaults,
      studio.fields
    );
    return (
      !paramsEqual(studio.params, existingParams) ||
      !paramsEqual(studio.effectiveComponentPresets, existingCompPresets)
    );
  });

  const userScopedComponentWarnings = $derived.by<string[]>(() => {
    if (scope !== 'project') return [];
    const warnings: string[] = [];
    const activeRefs = studio.effectiveComponentPresets;
    for (const f of collectComponentFields(studio.fields)) {
      const refKey = activeRefs[f.path];
      if (!refKey || refKey === 'default' || !f.component_id) continue;
      const bundle = studio.components[f.component_id];
      const matched = bundle?.presets.find((p) => p.key === refKey);
      if (matched && matched.source === 'user') {
        warnings.push(
          `${f.label} references a user-only component preset ("${matched.label}") that won't be shared in presets.json.`
        );
      }
    }
    return warnings;
  });

  const isAlreadyDefault = $derived(
    Boolean(matchingExisting && studio.defaultPresetKey === matchingExisting.key)
  );

  const overwriteNotice = $derived.by<{ title: string; detail: string } | null>(() => {
    if (!matchingExisting || !matchKind) return null;
    const targetName = matchingExisting.label;
    if (matchKind === 'overwrite') {
      const scopeLabel = scope === 'project' ? 'project' : 'user';
      return {
        title: `Existing ${scopeLabel} preset will be overwritten`,
        detail: matchedByKeyOnly
          ? `Maps to the same preset key as "${targetName}". Change the name to save as a new preset.`
          : 'Change the name to save as a new preset.',
      };
    }
    if (matchKind === 'override-local') {
      return {
        title: 'Existing project preset will be overridden',
        detail: matchedByKeyOnly
          ? `Maps to the same preset key as "${targetName}" on this device. Change the name to save as a new preset.`
          : 'Saves a local override on this device without modifying presets.json. Change the name to save as a new preset.',
      };
    }
    return {
      title: 'Existing user preset will be replaced',
      detail: matchedByKeyOnly
        ? `Maps to the same preset key as "${targetName}" and will remove your local user copy. Change the name to save as a new preset.`
        : 'Removes your local user copy when saved as a Project preset. Change the name to save as a new preset.',
    };
  });

  const submitLabel = $derived.by(() => {
    if (matchKind === 'override-local') return 'Save Local Override';
    if (matchKind === 'overwrite' || matchKind === 'replace-project') return 'Overwrite Preset';
    return 'Save New Preset';
  });

  const nameDescribedBy = $derived(
    [overwriteNotice ? 'preset-name-match-notice' : '', formError ? 'save-preset-form-error' : '']
      .filter(Boolean)
      .join(' ')
  );

  function initializeForm() {
    activeTab = 'preset';
    formError = '';
    sparseOnly = true;
    setAsDefault = false;
    previewLoading = false;
    previewData = null;
    previewError = '';
    applyingDefaults = false;
    defaultsSuccess = '';

    const cur = studio.presets.find((p) => p.key === studio.selectedPresetKey);
    if (cur && cur.key !== 'default' && (cur.source === 'user' || cur.source === 'project')) {
      label = cur.label;
      description = cur.description;
      scope = cur.source === 'project' ? 'project' : 'user';
    } else {
      label = suggestNewPresetName({
        exportFilename: studio.defaultExportFilename,
        modelName: studio.activeModel?.name,
        modelRelPath: studio.activeModel?.rel_path,
        basePresetLabel: cur?.label,
        existingPresets: studio.presets,
      });
      description = '';
      scope = studio.config.preset_save_scope === 'project' ? 'project' : 'user';
    }
  }

  let wasOpen = false;
  $effect(() => {
    const el = dialogEl;
    const isOpen = studio.savePresetModalOpen && Boolean(studio.activeModel);
    if (!el) return;
    if (isOpen && !wasOpen) {
      wasOpen = true;
      untrack(() => initializeForm());
      if (!el.open) {
        el.showModal();
      }
      queueMicrotask(() => {
        nameInputEl?.focus();
        if (label) {
          nameInputEl?.select();
        }
      });
    } else if (!isOpen && wasOpen) {
      wasOpen = false;
      if (el.open) {
        el.close();
      }
    }
  });

  function closeModal() {
    if (saving || applyingDefaults) return;
    studio.savePresetModalOpen = false;
  }

  function handleDialogCancel(e: Event) {
    if (saving || applyingDefaults) {
      e.preventDefault();
    }
  }

  function handleDialogClose() {
    wasOpen = false;
    pressedOutside = false;
    if (studio.savePresetModalOpen) {
      studio.savePresetModalOpen = false;
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

  function switchTab(tab: SaveTab) {
    activeTab = tab;
    if (tab === 'source') {
      void loadDefaultsPreview();
    }
  }

  async function handleApplyDefaults() {
    if (!previewData || previewData.changes.length === 0) {
      return;
    }
    applyingDefaults = true;
    previewError = '';
    defaultsSuccess = '';
    try {
      const resp = await studio.applyUpdateDefaults(previewData.sha256 ?? '');
      const updatedCount = resp.updated ?? previewData.changes.length;
      const targetRel = resp.rel_path || previewData.rel_path || 'source file';
      const skippedCount = resp.unsupported?.length ?? previewData.unsupported.length;
      defaultsSuccess =
        skippedCount > 0
          ? `Updated ${updatedCount} local default(s) in ${targetRel}. ${skippedCount} external/unsupported parameter(s) were skipped.`
          : `Updated ${updatedCount} default(s) in ${targetRel}.`;
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

  async function handleSubmit(e: SubmitEvent) {
    e.preventDefault();
    const trimmed = label.trim();
    if (!trimmed) {
      formError = 'Preset name is required.';
      nameInputEl?.focus();
      return;
    }

    let nextVerification: PresetVerification | null = null;
    if (matchingExisting) {
      const existingVer = getPresetVerification(matchingExisting);
      nextVerification = matchingParamsChanged
        ? { status: 'unverified', note: existingVer.note }
        : { status: existingVer.status, note: existingVer.note };
    }

    saving = true;
    formError = '';
    try {
      const saved = await studio.savePreset(
        trimmed,
        description.trim(),
        scope,
        !sparseOnly,
        nextVerification
      );
      if (saved) {
        if (setAsDefault && saved.key) {
          await studio.setDefaultPreset(saved.key);
        }
        saving = false;
        closeModal();
      }
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
  aria-labelledby="save-preset-modal-title"
  {...({ closedby: saving || applyingDefaults ? 'none' : 'any' } as Record<string, unknown>)}
  oncancel={handleDialogCancel}
  onclose={handleDialogClose}
  onpointerdown={handleDialogPointerDown}
  onpointerup={handleDialogPointerUp}
>
  {#if studio.activeModel}
    <div
      class="bg-base-100 border border-base-300 rounded-box shadow-md w-[32rem] max-w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto p-6 space-y-4"
    >
      <div class="flex items-center justify-between gap-3">
        <div>
          <h2 id="save-preset-modal-title" class="font-bold text-base">Save</h2>
          <p class="text-[11px] opacity-60">{studio.activeModel.name}</p>
        </div>
        <button
          type="button"
          class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
          title="Close"
          aria-label="Close save dialog"
          disabled={saving || applyingDefaults}
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

      <!-- Tabs -->
      <div role="tablist" class="tabs tabs-box tabs-sm">
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === 'preset'}
          class="tab flex-1"
          class:tab-active={activeTab === 'preset'}
          disabled={saving || applyingDefaults}
          onclick={() => switchTab('preset')}
        >
          Save Preset
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === 'source'}
          class="tab flex-1 gap-1.5"
          class:tab-active={activeTab === 'source'}
          disabled={saving || applyingDefaults}
          onclick={() => switchTab('source')}
        >
          <span>Update Source File</span>
          {#if hasSourceDiff}
            <span
              class="inline-block h-1.5 w-1.5 rounded-full bg-warning shrink-0"
              title="Current parameters differ from source file defaults"
              aria-hidden="true"
            ></span>
          {/if}
        </button>
      </div>

      {#if activeTab === 'preset'}
        <form
          class="space-y-3"
          aria-labelledby="save-preset-modal-title"
          onsubmit={handleSubmit}
          novalidate
        >
        <div class="form-control">
          <label class="label py-1" for="preset-name-input">
            <span class="label-text text-xs font-semibold">Preset Name</span>
          </label>
          <!-- svelte-ignore a11y_autofocus -->
          <input
            bind:this={nameInputEl}
            id="preset-name-input"
            name="preset-name"
            type="text"
            required
            autofocus
            bind:value={label}
            oninput={() => {
              if (formError) formError = '';
            }}
            aria-invalid={formError ? 'true' : undefined}
            aria-describedby={nameDescribedBy || undefined}
            placeholder="e.g. Gridfinity Bin - 2x2x6u"
            class="input input-bordered input-sm w-full"
            class:input-error={Boolean(formError)}
          />
          {#if overwriteNotice}
            <Alert
              id="preset-name-match-notice"
              variant="error"
              role="status"
              title={overwriteNotice.title}
              class="mt-2"
            >
              <p>{overwriteNotice.detail}</p>
            </Alert>
          {:else if isSavingFromBase}
            <p class="text-[11px] opacity-60 mt-1.5">
              Saves as a new preset. To edit the <span class="font-medium">Base</span> defaults in
              <code class="font-mono">{pathBasename(studio.activeModel.rel_path)}</code>, switch to
              <button
                type="button"
                class="link link-hover font-medium opacity-100"
                onclick={() => switchTab('source')}
              >
                Update Source File</button
              >.
            </p>
          {/if}
        </div>

        <div class="form-control">
          <label class="label py-1" for="preset-desc-input">
            <span class="label-text text-xs font-semibold">Description</span>
          </label>
          <input
            id="preset-desc-input"
            name="preset-description"
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
                scope === 'user'
                  ? 'border-primary bg-primary/5'
                  : 'border-base-300 bg-base-100 hover:border-base-content/25'
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
                scope === 'project'
                  ? 'border-primary bg-primary/5'
                  : 'border-base-300 bg-base-100 hover:border-base-content/25'
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
          {#if userScopedComponentWarnings.length > 0}
            <Alert variant="warning" title="Local component preset referenced">
              {#each userScopedComponentWarnings as msg (msg)}
                <p>{msg}</p>
              {/each}
            </Alert>
          {/if}
        </fieldset>

        <div class="rounded-box bg-base-200/60 border border-base-300 p-2.5 space-y-2">
          <label class="flex items-start gap-2 cursor-pointer text-xs">
            <input
              type="checkbox"
              name="sparse-only"
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
              name="set-as-default"
              bind:checked={setAsDefault}
              class="checkbox checkbox-xs checkbox-primary"
            />
            <span class="font-medium">
              Set preset as model default
              {#if isAlreadyDefault}
                <span class="font-normal opacity-60">(currently default)</span>
              {/if}
            </span>
          </label>
        </div>

        {#if formError}
          <Alert id="save-preset-form-error" variant="error">
            {formError}
          </Alert>
        {/if}

        <div class="flex justify-end gap-2 pt-2">
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            disabled={saving}
            onclick={closeModal}
          >
            Cancel
          </button>
          <button
            type="submit"
            class="btn btn-sm"
            class:btn-error={Boolean(overwriteNotice)}
            class:btn-primary={!overwriteNotice}
            disabled={saving}
          >
            {#if saving}
              <span class="loading loading-spinner loading-xs"></span>
            {:else}
              {submitLabel}
            {/if}
          </button>
        </div>
      </form>
      {:else}
        <!-- Update Source File Tab -->
        <div class="space-y-3">
          <p class="text-[11px] text-error">
            For advanced users only. Updates the base model values in
            <code class="font-mono" title={studio.activeModel.rel_path}>{pathBasename(studio.activeModel.rel_path)}</code>.
          </p>

          {#if previewLoading}
            <div class="flex items-center gap-2 text-xs opacity-70 py-4 justify-center">
              <span class="loading loading-spinner loading-xs"></span>
              <span>Checking for changes...</span>
            </div>
          {:else if previewData}
            {#if previewData.unsupported.length > 0}
              <Alert
                variant={onlyUnsupportedChanges ? 'error' : 'warning'}
                title={onlyUnsupportedChanges
                  ? "Changed parameters can't be updated in this model file"
                  : 'Some changed parameters will be skipped'}
              >
                <div class="space-y-2 text-[11px]">
                  {#each unsupportedGroups as group (group.key)}
                    {#if group.kind === 'external'}
                      <div class="space-y-1">
                        <div class="flex items-center justify-between gap-2 flex-wrap">
                          <div class="font-medium">
                            <code class="font-mono">{group.ownerClass}</code>
                            {#if group.sourceRelPath}
                              <span class="opacity-75">in</span>
                              <code class="font-mono">{group.sourceRelPath}</code>
                            {/if}
                          </div>
                          {#if studio.hasDesktop && group.inLibrary && group.sourceFile}
                            <button
                              type="button"
                              class="btn btn-xs btn-outline"
                              onclick={() => void studio.openModelInEditor(group.sourceFile)}
                            >
                              Open in Editor
                            </button>
                          {/if}
                        </div>
                        <p class="opacity-80">
                          Defined in a shared component outside this model file; skipped to avoid
                          changing defaults for other models.
                        </p>
                        <ul class="list-disc pl-4 space-y-0.5">
                          {#each group.items as item (item.path)}
                            <li><code class="font-mono">{item.path}</code></li>
                          {/each}
                        </ul>
                      </div>
                    {:else}
                      <ul class="list-disc pl-4 space-y-0.5">
                        {#each group.items as item (item.path)}
                          <li>
                            <code class="font-mono">{item.path}</code>: {item.reason}
                          </li>
                        {/each}
                      </ul>
                    {/if}
                  {/each}
                </div>
              </Alert>
            {/if}

            {#if previewData.changes.length > 0}
              {#if canonicalDefaultVer.status !== 'unverified'}
                <Alert variant="info">
                  Applying these changes will reset the base preset's verification status to
                  <strong>Unverified</strong> (any print note will be preserved).
                </Alert>
              {/if}
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
            <Alert variant="error">{previewError}</Alert>
          {/if}

          {#if defaultsSuccess}
            <Alert variant="success">{defaultsSuccess}</Alert>
          {/if}

          <div class="flex justify-end gap-2 pt-2">
            <button
              type="button"
              class="btn btn-sm btn-ghost"
              disabled={applyingDefaults}
              onclick={closeModal}
            >
              Close
            </button>
            <button
              type="button"
              class="btn btn-sm btn-primary"
              disabled={previewLoading ||
                applyingDefaults ||
                !previewData ||
                previewData.changes.length === 0}
              onclick={handleApplyDefaults}
            >
              {#if applyingDefaults}
                <span class="loading loading-spinner loading-xs"></span>
              {:else if hasPartialChanges && previewData}
                Update {previewData.changes.length} Local Default{previewData.changes.length === 1
                  ? ''
                  : 's'}
              {:else}
                Update Source File
              {/if}
            </button>
          </div>
        </div>
      {/if}
    </div>
  {/if}
</dialog>
