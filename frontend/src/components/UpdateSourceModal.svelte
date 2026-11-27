<script lang="ts">
  import { untrack } from 'svelte';
  import { pathBasename } from '../lib/paths';
  import { studio } from '../lib/studioState.svelte';
  import type { UpdateDefaultsResponse } from '../lib/types';
  import { groupUnsupportedDefaults } from '../lib/updateDefaults';
  import { getPresetVerification } from '../lib/verification';
  import Alert from './Alert.svelte';

  let dialogEl: HTMLDialogElement | undefined = $state();
  let nameInputEl: HTMLInputElement | undefined = $state();
  let pressedOutside = false;

  let previewLoading = $state(false);
  let previewData = $state<UpdateDefaultsResponse | null>(null);
  let previewError = $state('');

  let draftLabel = $state('');
  let draftDescription = $state('');
  let acknowledged = $state(false);

  let applying = $state(false);
  let applyError = $state('');
  let applySuccess = $state('');

  const model = $derived(studio.activeModel);

  const canonicalDefault = $derived(
    studio.presets.find((p) => p.key === 'default') ?? null
  );
  const canonicalDefaultVer = $derived(getPresetVerification(canonicalDefault));

  const dpInfo = $derived(previewData?.default_preset ?? null);
  const canEditMeta = $derived(Boolean(dpInfo?.can_edit));

  const origLabel = $derived(dpInfo?.label ?? '');
  const origDesc = $derived(dpInfo?.description ?? '');
  const trimmedLabel = $derived(draftLabel.trim());
  const trimmedDesc = $derived(draftDescription.trim());

  const metaValidationError = $derived(
    canEditMeta && trimmedDesc !== '' && trimmedLabel === ''
      ? 'Base model name is required when a description is set.'
      : ''
  );

  const metaDirty = $derived(
    Boolean(canEditMeta && (trimmedLabel !== origLabel || trimmedDesc !== origDesc))
  );

  const metaActionHint = $derived.by(() => {
    if (!metaDirty) return '';
    if (origLabel === '' && origDesc === '') {
      return 'Will add @default_preset to the root Params class.';
    }
    if (trimmedLabel === '' && trimmedDesc === '') {
      return 'Will remove @default_preset (reverts display name to "Base").';
    }
    return 'Will update @default_preset in the source file.';
  });

  const unsupportedGroups = $derived(
    previewData ? groupUnsupportedDefaults(previewData.unsupported) : []
  );

  const paramChangesCount = $derived(previewData?.changes.length ?? 0);
  const hasPartialChanges = $derived(
    Boolean(previewData && previewData.changes.length > 0 && previewData.unsupported.length > 0)
  );
  const onlyUnsupportedChanges = $derived(
    Boolean(previewData && previewData.changes.length === 0 && previewData.unsupported.length > 0)
  );

  const willApplyParams = $derived(paramChangesCount > 0);

  const canSubmit = $derived(
    Boolean(
      previewData &&
        !previewLoading &&
        !applying &&
        !metaValidationError &&
        (metaDirty || willApplyParams) &&
        acknowledged
    )
  );

  const footerSummary = $derived.by(() => {
    const parts: string[] = [];
    if (metaDirty) parts.push('Base metadata');
    if (willApplyParams) {
      parts.push(
        `${paramChangesCount} parameter default${paramChangesCount === 1 ? '' : 's'}`
      );
    }
    if (parts.length === 0) return 'No changes to apply';
    return `Updating ${parts.join(' + ')}`;
  });

  async function loadPreview(initForm: boolean): Promise<void> {
    previewLoading = true;
    previewError = '';
    if (initForm) {
      applyError = '';
      applySuccess = '';
    }
    try {
      const resp = await studio.previewUpdateDefaults();
      if (!resp.ok) {
        previewData = null;
        previewError = resp.error || 'Failed to inspect model source file';
      } else {
        previewData = resp;
        if (initForm) {
          draftLabel = resp.default_preset?.label ?? '';
          draftDescription = resp.default_preset?.description ?? '';
          acknowledged = false;
          if (studio.updateSourceIntent === 'metadata' && resp.default_preset?.can_edit) {
            queueMicrotask(() => {
              nameInputEl?.focus();
              if (draftLabel) nameInputEl?.select();
            });
          }
        }
      }
    } catch (err) {
      previewData = null;
      previewError = err instanceof Error ? err.message : String(err);
    } finally {
      previewLoading = false;
    }
  }

  let wasOpen = false;
  $effect(() => {
    const el = dialogEl;
    const isOpen = studio.updateSourceModalOpen && Boolean(model);
    if (!el) return;
    if (isOpen && !wasOpen) {
      wasOpen = true;
      untrack(() => {
        previewData = null;
        previewError = '';
        applyError = '';
        applySuccess = '';
        draftLabel = '';
        draftDescription = '';
        acknowledged = false;
        void loadPreview(true);
      });
      if (!el.open) {
        el.showModal();
      }
    } else if (!isOpen && wasOpen) {
      wasOpen = false;
      if (el.open) {
        el.close();
      }
    }
  });

  // Close if active model changes underneath while open
  let prevModelId = studio.activeModelId;
  $effect(() => {
    const nextModelId = studio.activeModelId;
    if (nextModelId !== prevModelId) {
      prevModelId = nextModelId;
      if (!applying && studio.updateSourceModalOpen) {
        studio.updateSourceModalOpen = false;
      }
    }
  });

  function closeModal() {
    if (applying) return;
    studio.updateSourceModalOpen = false;
  }

  function handleDialogCancel(e: Event) {
    if (applying) {
      e.preventDefault();
    }
  }

  function handleDialogClose() {
    wasOpen = false;
    pressedOutside = false;
    if (studio.updateSourceModalOpen) {
      studio.updateSourceModalOpen = false;
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

  async function handleApply(e: SubmitEvent) {
    e.preventDefault();
    if (!previewData || !canSubmit) return;

    applying = true;
    applyError = '';
    applySuccess = '';
    try {
      const resp = await studio.applyUpdateDefaults(previewData.sha256 ?? '', {
        includeParams: willApplyParams,
        defaultPreset: metaDirty ? { label: trimmedLabel, description: trimmedDesc } : undefined,
      });

      const targetRel = resp.rel_path || previewData.rel_path || 'source file';
      const updatedCount = resp.updated ?? (willApplyParams ? previewData.changes.length : 0);
      const skippedCount = willApplyParams
        ? (resp.unsupported?.length ?? previewData.unsupported.length)
        : 0;
      const didMeta = Boolean(resp.default_preset_updated ?? metaDirty);

      const summaryParts: string[] = [];
      if (didMeta) {
        summaryParts.push('base preset metadata');
      }
      if (updatedCount > 0) {
        summaryParts.push(`${updatedCount} parameter default${updatedCount === 1 ? '' : 's'}`);
      }
      let msg = `Updated ${summaryParts.join(' and ')} in ${targetRel}.`;
      if (skippedCount > 0) {
        msg += ` ${skippedCount} external/unsupported parameter(s) were skipped.`;
      }
      applySuccess = msg;

      const nextDp = resp.default_preset ?? {
        label: trimmedLabel,
        description: trimmedDesc,
        can_edit: canEditMeta,
      };
      draftLabel = nextDp.label;
      draftDescription = nextDp.description;
      acknowledged = false;
      previewData = {
        ...previewData,
        sha256: resp.sha256 ?? previewData.sha256,
        default_preset: nextDp,
        changes: willApplyParams ? [] : previewData.changes,
        unsupported: willApplyParams ? [] : previewData.unsupported,
      };
    } catch (err) {
      applyError = err instanceof Error ? err.message : String(err);
      void loadPreview(false);
    } finally {
      applying = false;
    }
  }
</script>

<dialog
  bind:this={dialogEl}
  class="studio-dialog"
  aria-labelledby="update-source-modal-title"
  {...({ closedby: applying ? 'none' : 'any' } as Record<string, unknown>)}
  oncancel={handleDialogCancel}
  onclose={handleDialogClose}
  onpointerdown={handleDialogPointerDown}
  onpointerup={handleDialogPointerUp}
>
  {#if model}
    <div
      class="bg-base-100 border border-base-300 rounded-box shadow-md w-[34rem] max-w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto p-6 space-y-4"
    >
      <!-- Header with subdued model context -->
      <div class="pb-3 border-b border-base-300/70 space-y-1">
        <div class="flex items-center justify-between gap-3">
          <h2 id="update-source-modal-title" class="font-bold text-base">Update Source File</h2>
          <button
            type="button"
            class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
            title="Close"
            aria-label="Close update source file dialog"
            disabled={applying}
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

      <div class="space-y-2">
        <Alert variant="warning" title="Modifies source file">
          <p>
            <strong>For advanced users only.</strong> Write parameter updates to
            <code class="font-mono">{pathBasename(model.rel_path)}</code>. Changing base parameter
            defaults may impact other presets that inherit from Base.
          </p>
        </Alert>
        <label class="flex items-center gap-2 cursor-pointer text-xs">
          <input
            type="checkbox"
            bind:checked={acknowledged}
            disabled={applying}
            class="checkbox checkbox-xs checkbox-warning"
          />
          <span class="font-medium">I understand the risks — update the source file</span>
        </label>
      </div>

      {#if previewLoading && !previewData}
        <div class="flex items-center gap-2 text-xs opacity-70 py-6 justify-center">
          <span class="loading loading-spinner loading-xs"></span>
          <span>Inspecting source file…</span>
        </div>
      {:else if previewError && !previewData}
        <Alert variant="error">{previewError}</Alert>
        <div class="flex justify-end pt-1">
          <button type="button" class="btn btn-sm btn-ghost" onclick={closeModal}>Close</button>
        </div>
      {:else if previewData}
        <form class="space-y-4" onsubmit={handleApply} novalidate>
          <!-- Base Model Metadata -->
          <section class="space-y-2.5">
            {#if !canEditMeta && dpInfo?.unsupported_reason}
              <p class="text-[11px] opacity-70">{dpInfo.unsupported_reason}.</p>
            {/if}

            <div class="space-y-2.5">
              <div class="form-control">
                <label class="label py-0.5" for="source-base-name-input">
                  <span class="label-text text-xs font-semibold">Base Model Name</span>
                </label>
                <input
                  bind:this={nameInputEl}
                  id="source-base-name-input"
                  type="text"
                  bind:value={draftLabel}
                  disabled={!canEditMeta || applying}
                  placeholder="Base"
                  aria-invalid={metaValidationError ? 'true' : undefined}
                  class="input input-bordered input-sm w-full text-xs"
                  class:input-error={Boolean(metaValidationError)}
                />
              </div>

              <div class="form-control">
                <label class="label py-0.5" for="source-base-desc-input">
                  <span class="label-text text-xs font-semibold">Description</span>
                </label>
                <textarea
                  id="source-base-desc-input"
                  bind:value={draftDescription}
                  disabled={!canEditMeta || applying}
                  rows="2"
                  placeholder="Optional description for the base defaults..."
                  class="textarea textarea-bordered w-full text-xs leading-relaxed py-1.5"
                ></textarea>
              </div>

              {#if metaValidationError}
                <p class="text-[11px] text-error">{metaValidationError}</p>
              {:else if metaActionHint}
                <p class="text-[11px] opacity-65">{metaActionHint}</p>
              {/if}
            </div>
          </section>

          <div class="border-t border-base-300/70"></div>

          <!-- Section 2: Parameter Defaults -->
          <section class="space-y-2.5">
            <div class="flex items-center justify-between gap-2">
              <h3 class="text-xs font-semibold">
                Parameter Defaults
              </h3>
              {#if previewLoading}
                <span class="loading loading-spinner loading-xs opacity-60"></span>
              {/if}
            </div>

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
                <Alert variant="info" title="Resets print verification">
                  <p>
                    Applying parameter changes will reset Base's print status to
                    <strong>Unverified</strong> (any print note will be preserved).
                  </p>
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
                          <span class="opacity-60">{ch.old_literal}</span>
                          <span class="mx-1 opacity-50">→</span>
                          <span class="text-primary font-semibold">{ch.new_literal}</span>
                        </td>
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {:else if previewData.unsupported.length === 0}
              <div class="rounded-box bg-base-200/70 px-3 py-2 text-xs opacity-75">
                Live parameters already match the defaults in
                <code class="font-mono">{previewData.rel_path}</code>.
              </div>
            {/if}
          </section>

          {#if applyError}
            <Alert variant="error" compact>{applyError}</Alert>
          {/if}

          {#if applySuccess}
            <Alert variant="success" compact>{applySuccess}</Alert>
          {/if}

          <div class="flex items-center justify-between gap-3 pt-2">
            <span class="text-[11px] opacity-60 truncate">{footerSummary}</span>
            <div class="flex items-center gap-2 shrink-0">
              <button
                type="button"
                class="btn btn-sm btn-ghost"
                disabled={applying}
                onclick={closeModal}
              >
                {applySuccess && !metaDirty && !willApplyParams ? 'Close' : 'Cancel'}
              </button>
              <button
                type="submit"
                class="btn btn-sm btn-primary"
                disabled={!canSubmit}
              >
                {#if applying}
                  <span class="loading loading-spinner loading-xs"></span>
                {:else if hasPartialChanges && willApplyParams && !metaDirty}
                  Update {previewData.changes.length} Local Default{previewData.changes.length === 1
                    ? ''
                    : 's'}
                {:else}
                  Update Source File
                {/if}
              </button>
            </div>
          </div>
        </form>
      {/if}
    </div>
  {/if}
</dialog>
