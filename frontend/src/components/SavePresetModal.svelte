<script lang="ts">
  import { untrack } from 'svelte';
  import { mergeWithDefaults, normalizeParamsWithFields, paramsEqual } from '../lib/params';
  import {
    normalizePresetLabel,
    slugifyPresetLabel,
    suggestNewPresetName,
  } from '../lib/presetNaming';
  import { studio } from '../lib/studioState.svelte';
  import type { Preset, PresetVerification } from '../lib/types';
  import { getPresetVerification } from '../lib/verification';
  import Alert from './Alert.svelte';

  let dialogEl: HTMLDialogElement | undefined = $state();
  let nameInputEl: HTMLInputElement | undefined = $state();

  let label = $state('');
  let description = $state('');
  let scope = $state<'user' | 'project'>('user');
  let sparseOnly = $state(true);
  let setAsDefault = $state(false);

  let saving = $state(false);
  let formError = $state('');

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
    const existingParams = normalizeParamsWithFields(
      mergeWithDefaults(studio.defaults, matchingExisting.params),
      studio.fields
    );
    return !paramsEqual(studio.params, existingParams);
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
    formError = '';
    sparseOnly = true;
    setAsDefault = false;

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
    if (saving) return;
    studio.savePresetModalOpen = false;
  }

  function handleDialogCancel(e: Event) {
    if (saving) {
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
  {...({ closedby: 'any' } as Record<string, unknown>)}
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
          <h2 id="save-preset-modal-title" class="font-bold text-base">Save Preset</h2>
          <p class="text-[11px] opacity-60">{studio.activeModel.name}</p>
        </div>
        <button
          type="button"
          class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
          title="Close"
          aria-label="Close save preset dialog"
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
            class:input-error={Boolean(formError || overwriteNotice)}
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
              the Python source file, use <span class="font-medium">Manage Presets → Update Source File</span>.
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
    </div>
  {/if}
</dialog>
