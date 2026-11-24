<script lang="ts">
  import { untrack } from 'svelte';
  import { studio } from '../lib/studioState.svelte';
  import Alert from './Alert.svelte';

  interface Props {
    open: boolean;
    initialLibraryId?: string;
    onClose: () => void;
  }

  let { open, initialLibraryId, onClose }: Props = $props();

  let dialogEl: HTMLDialogElement | undefined = $state();
  let nameInputEl: HTMLInputElement | undefined = $state();

  let modelName = $state('');
  let libraryId = $state('');
  let template = $state<'parametric' | 'script'>('parametric');
  let creating = $state(false);
  let formError = $state('');
  let pressedOutside = false;

  let previewName = $derived.by(() => {
    const trimmed = modelName.trim();
    if (!trimmed) return '<name>';
    return trimmed.toLowerCase().endsWith('.py') ? trimmed.slice(0, -3).trim() || '<name>' : trimmed;
  });

  function initializeForm() {
    modelName = '';
    template = 'parametric';
    formError = '';
    const libs = studio.config.libraries ?? [];
    const activeLib = studio.activeModel?.library_id;
    if (initialLibraryId && libs.some((l) => l.id === initialLibraryId)) {
      libraryId = initialLibraryId;
    } else if (activeLib && libs.some((l) => l.id === activeLib)) {
      libraryId = activeLib;
    } else {
      libraryId = libs[0]?.id ?? '';
    }
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
      });
    } else if (!open && wasOpen) {
      wasOpen = false;
      if (el.open) {
        el.close();
      }
    }
  });

  function closeModal() {
    if (creating) return;
    onClose();
  }

  function handleDialogCancel(e: Event) {
    if (creating) {
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
    const trimmed = modelName.trim();
    if (!trimmed) {
      formError = 'Model name is required.';
      nameInputEl?.focus();
      return;
    }

    creating = true;
    formError = '';
    try {
      await studio.scaffoldNewModel({
        name: trimmed,
        template,
        ...(libraryId ? { library_id: libraryId } : {}),
      });
      creating = false;
      closeModal();
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    } finally {
      creating = false;
    }
  }
</script>

<dialog
  bind:this={dialogEl}
  class="studio-dialog"
  aria-labelledby="new-model-modal-title"
  {...({ closedby: creating ? 'none' : 'any' } as Record<string, unknown>)}
  oncancel={handleDialogCancel}
  onclose={handleDialogClose}
  onpointerdown={handleDialogPointerDown}
  onpointerup={handleDialogPointerUp}
>
  <div
    class="bg-base-100 border border-base-300 rounded-box shadow-md w-[28rem] max-w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto p-6 space-y-4"
  >
    <div class="flex items-center justify-between gap-3">
      <h2 id="new-model-modal-title" class="font-bold text-base">New CAD Model</h2>
      <button
        type="button"
        class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
        title="Close"
        aria-label="Close new model dialog"
        disabled={creating}
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

    <form class="space-y-3" onsubmit={handleSubmit} novalidate>
      <div class="form-control">
        <label class="label py-1" for="new-model-name-input">
          <span class="label-text text-xs font-semibold">Model Name</span>
        </label>
        <!-- svelte-ignore a11y_autofocus -->
        <input
          bind:this={nameInputEl}
          id="new-model-name-input"
          type="text"
          required
          autofocus
          bind:value={modelName}
          disabled={creating}
          oninput={() => {
            if (formError) formError = '';
          }}
          aria-invalid={formError ? 'true' : undefined}
          aria-describedby={formError ? 'new-model-form-error' : 'new-model-name-hint'}
          placeholder="e.g. cable_clip or CableClip"
          class="input input-bordered input-sm w-full font-mono text-xs"
          class:input-error={Boolean(formError)}
        />
        <span id="new-model-name-hint" class="text-[11px] opacity-60 mt-1 break-all">
          Creates <code>models/{previewName}/{previewName}.py</code> (and <code>utils/params.py</code> if needed).
        </span>
      </div>

      {#if (studio.config.libraries?.length ?? 0) > 1}
        <div class="form-control">
          <label class="label py-1" for="new-model-library-select">
            <span class="label-text text-xs font-semibold">Library</span>
          </label>
          <select
            id="new-model-library-select"
            bind:value={libraryId}
            disabled={creating}
            class="select select-bordered select-sm w-full text-xs"
          >
            {#each studio.config.libraries as lib (lib.id)}
              <option value={lib.id}>{lib.name || lib.id}</option>
            {/each}
          </select>
        </div>
      {/if}

      <fieldset class="space-y-1.5">
        <legend class="text-xs font-semibold">Template</legend>
        <div class="grid grid-cols-2 gap-2">
          <label
            class={`flex items-start gap-2 p-2.5 rounded-box border cursor-pointer text-xs ${
              template === 'parametric'
                ? 'border-primary bg-primary/5'
                : 'border-base-300 bg-base-100 hover:border-base-content/25'
            }`}
          >
            <input
              type="radio"
              name="new-model-template"
              value="parametric"
              disabled={creating}
              bind:group={template}
              class="radio radio-xs radio-primary mt-0.5"
            />
            <span class="leading-tight">
              <span class="font-medium block">Parametric</span>
              <span class="text-[11px] opacity-60">Uses <code>@dataclass</code> UI parameters.</span>
            </span>
          </label>

          <label
            class={`flex items-start gap-2 p-2.5 rounded-box border cursor-pointer text-xs ${
              template === 'script'
                ? 'border-primary bg-primary/5'
                : 'border-base-300 bg-base-100 hover:border-base-content/25'
            }`}
          >
            <input
              type="radio"
              name="new-model-template"
              value="script"
              disabled={creating}
              bind:group={template}
              class="radio radio-xs radio-primary mt-0.5"
            />
            <span class="leading-tight">
              <span class="font-medium block">Simple Script</span>
              <span class="text-[11px] opacity-60">Direct <code>build123d</code> script.</span>
            </span>
          </label>
        </div>
      </fieldset>

      {#if formError}
        <Alert id="new-model-form-error" variant="error">
          {formError}
        </Alert>
      {/if}

      <div class="flex justify-end gap-2 pt-2">
        <button
          type="button"
          class="btn btn-sm btn-ghost"
          disabled={creating}
          onclick={closeModal}
        >
          Cancel
        </button>
        <button type="submit" class="btn btn-sm btn-primary" disabled={creating}>
          {#if creating}
            <span class="loading loading-spinner loading-xs"></span>
            Creating…
          {:else}
            Create Model
          {/if}
        </button>
      </div>
    </form>
  </div>
</dialog>
