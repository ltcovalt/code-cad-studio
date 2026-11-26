<script lang="ts">
  import { untrack } from 'svelte';
  import * as api from '../lib/api';
  import { studio } from '../lib/studioState.svelte';
  import type { Library } from '../lib/types';
  import Alert from './Alert.svelte';
  import LibraryListEditor, {
    nextLibraryDraftUid,
    type LibraryDraft,
  } from './LibraryListEditor.svelte';

  function countModelsForLibrary(libId: string): number | undefined {
    if (!libId || Boolean(studio.libraryErrors[libId])) return undefined;
    return studio.models.filter((m) => m.library_id === libId).length;
  }

  let dialogEl: HTMLDialogElement | undefined = $state();
  let dialogBodyEl: HTMLDivElement | undefined = $state();
  let pressedOutside = false;

  let libraries = $state<LibraryDraft[]>([]);
  let exportDir = $state('');
  let resolvedExportDir = $state('');
  let saving = $state(false);
  let formError = $state('');

  let busy = $derived(saving);

  function initializeLibraries() {
    libraries = (studio.config.libraries ?? []).map((lib) => ({
      uid: nextLibraryDraftUid(),
      id: lib.id,
      name: lib.name,
      path: lib.path,
      modelCount: countModelsForLibrary(lib.id),
    }));
    exportDir = studio.config.export_dir ?? '';
    resolvedExportDir = studio.resolvedExportDir;
    formError = '';
  }

  let wasOpen = false;
  $effect(() => {
    const el = dialogEl;
    const isOpen = studio.librariesOpen;
    if (!el) return;
    if (isOpen && !wasOpen) {
      wasOpen = true;
      untrack(() => initializeLibraries());
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

  function buildPayloadLibraries(): Library[] {
    return libraries
      .filter((l) => l.path.trim() !== '')
      .map((l) => ({
        id: l.id,
        name: l.name.trim(),
        path: l.path.trim(),
      }));
  }

  async function handleBrowseExportDir() {
    try {
      const resp = await api.browseExportDir(
        exportDir || resolvedExportDir || libraries[0]?.path || ''
      );
      if (!resp.cancelled && resp.path) {
        exportDir = resp.path;
      }
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    }
  }

  async function handleSubmit(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    formError = '';
    try {
      const payloadLibs = buildPayloadLibraries();
      const resp = await api.saveConfig(
        payloadLibs,
        studio.config.python_path ?? '',
        exportDir.trim()
      );
      exportDir = resp.config.export_dir ?? '';
      resolvedExportDir = resp.resolved_export_dir;
      await studio.applySavedConfig(resp);
      studio.librariesOpen = false;
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  function closeModal() {
    if (busy) return;
    studio.librariesOpen = false;
  }

  function handleDialogCancel(e: Event) {
    if (busy) {
      e.preventDefault();
    }
  }

  function handleDialogClose() {
    if (dialogEl) {
      dialogEl.returnValue = '';
    }
    wasOpen = false;
    pressedOutside = false;
    if (studio.librariesOpen) {
      studio.librariesOpen = false;
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
</script>

<dialog
  bind:this={dialogEl}
  class="studio-dialog"
  aria-labelledby="manage-libraries-modal-title"
  {...({ closedby: busy ? 'none' : 'any' } as Record<string, unknown>)}
  oncancel={handleDialogCancel}
  onclose={handleDialogClose}
  onpointerdown={handleDialogPointerDown}
  onpointerup={handleDialogPointerUp}
>
  <div
    bind:this={dialogBodyEl}
    tabindex="-1"
    class="bg-base-100 border border-base-300 rounded-box shadow-md w-[36rem] max-w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto p-6 space-y-4 outline-none"
  >
    <div class="flex items-center justify-between">
      <h2 id="manage-libraries-modal-title" class="font-bold text-base">Manage Libraries</h2>
      <button
        type="button"
        class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
        title="Close"
        aria-label="Close manage libraries"
        disabled={busy}
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
      <LibraryListEditor
        bind:libraries
        title="Model Libraries"
        reorderable={true}
        libraryErrors={studio.libraryErrors}
        {busy}
        onError={(msg) => (formError = msg)}
      >
        {#snippet emptyState()}
          <div class="rounded-box border border-dashed border-base-300 p-3 text-xs opacity-70 text-center">
            No libraries configured yet. {studio.hasDesktop
              ? 'Click Add Library above'
              : 'Enter a folder path below'}, or run the Setup Wizard to try the offline demo or starter library.
          </div>
        {/snippet}
      </LibraryListEditor>

      <!-- Optional Global Export Folder -->
      <div class="form-control">
        <label class="label py-1" for="libraries-export-dir">
          <span class="label-text text-xs font-semibold">Global Export Folder (optional)</span>
        </label>
        <div class="join w-full">
          <input
            id="libraries-export-dir"
            type="text"
            bind:value={exportDir}
            disabled={busy}
            class="input input-bordered input-sm join-item w-full font-mono text-xs"
            placeholder="Per-library default (<library>/exports)"
          />
          {#if studio.hasDesktop}
            <button
              type="button"
              class="btn btn-sm btn-neutral join-item"
              disabled={busy}
              onclick={handleBrowseExportDir}
            >
              Browse
            </button>
          {/if}
        </div>
      </div>

      {#if formError}
        <Alert variant="error">{formError}</Alert>
      {/if}

      <div class="flex justify-end gap-2 pt-2">
        <button
          type="button"
          class="btn btn-sm btn-ghost"
          disabled={busy}
          onclick={closeModal}
        >
          Cancel
        </button>
        <button type="submit" class="btn btn-sm btn-primary" disabled={busy}>
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </form>
  </div>
</dialog>
