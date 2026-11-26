<script lang="ts">
  import { untrack } from 'svelte';
  import { pathBasename } from '../lib/paths';
  import { studio } from '../lib/studioState.svelte';
  import type { ModelInfo } from '../lib/types';
  import Alert from './Alert.svelte';

  interface Props {
    open: boolean;
    model: ModelInfo | null;
    onClose: () => void;
  }

  let { open, model, onClose }: Props = $props();

  let dialogEl: HTMLDialogElement | undefined = $state();
  let nameInputEl: HTMLInputElement | undefined = $state();

  let modelName = $state('');
  let fileStem = $state('');
  let saving = $state(false);
  let formError = $state('');
  let pressedOutside = false;

  const VALID_STEM_RE = /^[a-zA-Z_][a-zA-Z0-9_]*$/;

  let initialStem = $derived.by(() => {
    if (!model) return '';
    const base = pathBasename(model.rel_path || model.path);
    return base.toLowerCase().endsWith('.py') ? base.slice(0, -3) : base;
  });

  let cleanName = $derived(modelName.trim());
  let cleanStem = $derived.by(() => {
    const trimmed = fileStem.trim();
    return trimmed.toLowerCase().endsWith('.py') ? trimmed.slice(0, -3).trim() : trimmed;
  });

  let nameChanged = $derived(Boolean(model && cleanName !== model.name));
  let stemChanged = $derived(Boolean(model && cleanStem !== initialStem));

  let stemError = $derived.by(() => {
    if (!cleanStem) return 'File name is required.';
    if (!VALID_STEM_RE.test(cleanStem) || cleanStem === '__init__' || cleanStem === 'params') {
      return 'Use letters, digits, and underscores (cannot start with a digit).';
    }
    return '';
  });

  let previewRelPath = $derived.by(() => {
    if (!model) return '';
    const rel = model.rel_path || '';
    const stem = cleanStem || initialStem || '<name>';
    const parts = rel.split('/').filter(Boolean);
    if (parts.length >= 2 && parts[parts.length - 2] === initialStem) {
      // Canonical <name>/<name>.py folder layout
      parts[parts.length - 2] = stem;
      parts[parts.length - 1] = `${stem}.py`;
      return parts.join('/');
    }
    if (parts.length >= 1) {
      parts[parts.length - 1] = `${stem}.py`;
      return parts.join('/');
    }
    return `${stem}.py`;
  });

  let canSubmit = $derived(
    Boolean(
      model &&
        !saving &&
        cleanName.length > 0 &&
        !stemError &&
        (nameChanged || stemChanged)
    )
  );

  function initializeForm() {
    modelName = model?.name ?? '';
    fileStem = initialStem;
    formError = '';
  }

  let wasOpen = false;
  $effect(() => {
    const el = dialogEl;
    if (!el) return;
    if (open && !wasOpen) {
      wasOpen = true;
      untrack(() => initializeForm());
      if (!el.open) {
        el.showModal();
      }
      queueMicrotask(() => {
        nameInputEl?.focus();
        nameInputEl?.select();
      });
    } else if (!open && wasOpen) {
      wasOpen = false;
      if (el.open) {
        el.close();
      }
    }
  });

  function closeModal() {
    if (saving) return;
    onClose();
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
    if (open) {
      onClose();
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
    if (!model || !canSubmit) return;

    saving = true;
    formError = '';
    try {
      await studio.renameModel(model.id, {
        ...(nameChanged ? { name: cleanName } : {}),
        ...(stemChanged ? { file_stem: cleanStem } : {}),
      });
      onClose();
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
  aria-labelledby="rename-model-modal-title"
  {...({ closedby: saving ? 'none' : 'any' } as Record<string, unknown>)}
  oncancel={handleDialogCancel}
  onclose={handleDialogClose}
  onpointerdown={handleDialogPointerDown}
  onpointerup={handleDialogPointerUp}
>
  <div
    class="bg-base-100 border border-base-300 rounded-box shadow-md w-[28rem] max-w-[calc(100vw-2rem)] p-6 space-y-4"
  >
    <div class="flex items-center justify-between">
      <h2 id="rename-model-modal-title" class="font-bold text-base">Rename Model</h2>
      <button
        type="button"
        class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
        title="Close"
        aria-label="Close"
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

    <form class="space-y-4" onsubmit={handleSubmit}>
      <div class="form-control">
        <label class="label py-1" for="rename-model-display-name">
          <span class="label-text text-xs font-semibold">Model Name</span>
        </label>
        <input
          id="rename-model-display-name"
          bind:this={nameInputEl}
          type="text"
          bind:value={modelName}
          disabled={saving}
          maxlength="120"
          class="input input-bordered input-sm w-full text-xs"
          placeholder="e.g. RC Battery Caddy"
        />
        <span class="text-[11px] opacity-60 mt-1">
          Stored as <code>MODEL_NAME</code> in the source script.
        </span>
      </div>

      <div class="form-control">
        <label class="label py-1" for="rename-model-file-stem">
          <span class="label-text text-xs font-semibold">File Name</span>
        </label>
        <div class="join w-full">
          <input
            id="rename-model-file-stem"
            type="text"
            bind:value={fileStem}
            disabled={saving}
            class="input input-bordered input-sm join-item w-full font-mono text-xs"
            placeholder="e.g. rc_battery_caddy"
          />
          <span class="btn btn-sm btn-ghost join-item border border-base-300 pointer-events-none font-mono text-xs opacity-70">
            .py
          </span>
        </div>
        {#if stemError}
          <span class="text-[11px] text-error mt-1">{stemError}</span>
        {:else if previewRelPath}
          <span class="text-[11px] opacity-60 mt-1">
            Path: <code class="font-mono break-all">{previewRelPath}</code>
          </span>
        {/if}
      </div>

      {#if formError}
        <Alert variant="error">{formError}</Alert>
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
        <button type="submit" class="btn btn-sm btn-primary" disabled={!canSubmit}>
          {saving ? 'Renaming…' : 'Rename'}
        </button>
      </div>
    </form>
  </div>
</dialog>
