<script lang="ts">
  import { untrack } from 'svelte';
  import * as api from '../lib/api';
  import { studio } from '../lib/studioState.svelte';
  import type { ExportQuality } from '../lib/types';
  import Alert from './Alert.svelte';
  import PythonEnvPanel from './PythonEnvPanel.svelte';

  let dialogEl: HTMLDialogElement | undefined = $state();
  let pressedOutside = false;

  let pythonPanel: PythonEnvPanel | undefined = $state();
  let pythonPath = $state('');
  let installingPython = $state(false);
  let editorCommand = $state('');
  let slicerCommand = $state('');
  let exportQuality = $state<ExportQuality>('standard');
  let saving = $state(false);
  let formError = $state('');

  let libraryRoots = $derived((studio.config.libraries ?? []).map((l) => l.path));
  let busy = $derived(saving || installingPython);

  function initializeSettings() {
    pythonPath = studio.config.python_path || '';
    editorCommand = studio.config.editor_command ?? '';
    slicerCommand = studio.config.slicer_command ?? '';
    exportQuality = studio.config.export_quality ?? 'standard';
    formError = '';
    void pythonPanel?.runProbe(pythonPath);
  }

  let dialogBodyEl: HTMLDivElement | undefined = $state();
  let wasOpen = false;
  $effect(() => {
    const el = dialogEl;
    const isOpen = studio.settingsOpen;
    if (!el || !pythonPanel) return;
    if (isOpen && !wasOpen) {
      wasOpen = true;
      untrack(() => initializeSettings());
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

  async function persistCurrentConfig(): Promise<void> {
    const resp = await api.saveConfig(
      studio.config.libraries ?? [],
      pythonPath.trim(),
      studio.config.export_dir ?? '',
      {
        editor_command: editorCommand.trim(),
        slicer_command: slicerCommand.trim(),
        export_quality: exportQuality,
      }
    );
    pythonPath = resp.config.python_path;
    editorCommand = resp.config.editor_command ?? '';
    slicerCommand = resp.config.slicer_command ?? '';
    exportQuality = resp.config.export_quality ?? 'standard';
    await studio.applySavedConfig(resp);
  }

  function hasUnsavedDraftChanges(): boolean {
    return (
      pythonPath.trim() !== (studio.config.python_path ?? '').trim() ||
      editorCommand.trim() !== (studio.config.editor_command ?? '') ||
      slicerCommand.trim() !== (studio.config.slicer_command ?? '') ||
      exportQuality !== (studio.config.export_quality ?? 'standard')
    );
  }

  async function handleSubmit(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    formError = '';
    try {
      await persistCurrentConfig();
      studio.settingsOpen = false;
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  async function openOnboardingPreservingDrafts() {
    if (busy) return;
    formError = '';
    if (hasUnsavedDraftChanges()) {
      saving = true;
      try {
        await persistCurrentConfig();
      } catch (err) {
        formError = err instanceof Error ? err.message : String(err);
        return;
      } finally {
        saving = false;
      }
    }
    studio.openOnboarding();
  }

  function closeModal() {
    if (busy) return;
    studio.settingsOpen = false;
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
    if (studio.settingsOpen) {
      studio.settingsOpen = false;
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
  aria-labelledby="settings-modal-title"
  {...({ closedby: busy ? 'none' : 'any' } as Record<string, unknown>)}
  oncancel={handleDialogCancel}
  onclose={handleDialogClose}
  onpointerdown={handleDialogPointerDown}
  onpointerup={handleDialogPointerUp}
>
  <div
    bind:this={dialogBodyEl}
    tabindex="-1"
    class="bg-base-100 border border-base-300 rounded-box shadow-md w-[39rem] max-w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto p-6 space-y-4 outline-none"
  >
    <div class="flex items-center justify-between">
      <h2 id="settings-modal-title" class="font-bold text-base">Workspace Settings</h2>
      <button
        type="button"
        class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
        title="Close"
        aria-label="Close settings"
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
        <!-- Python Environment -->
        <PythonEnvPanel
          bind:this={pythonPanel}
          bind:selectedPythonPath={pythonPath}
          bind:installing={installingPython}
          {libraryRoots}
          disabled={saving}
        />

        <!-- Default Export Quality -->
        <div class="form-control">
          <label class="label py-1" for="settings-export-quality">
            <span class="label-text text-xs font-semibold">Default STL / 3MF Quality</span>
          </label>
          <select
            id="settings-export-quality"
            bind:value={exportQuality}
            class="select select-bordered select-sm w-full text-xs"
          >
            <option value="draft">Draft (fast, coarser mesh)</option>
            <option value="standard">Standard (balanced)</option>
            <option value="fine">Fine (high detail, larger files)</option>
            {#if exportQuality === 'custom'}
              <option value="custom">Custom</option>
            {/if}
          </select>
        </div>

        {#if studio.hasDesktop}
          <!-- External Editor & Slicer Commands -->
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div class="form-control">
              <label class="label py-1" for="settings-editor-command">
                <span class="label-text text-xs font-semibold">Editor Command (optional)</span>
              </label>
              <input
                id="settings-editor-command"
                type="text"
                bind:value={editorCommand}
                class="input input-bordered input-sm w-full font-mono text-xs"
                placeholder="Auto-detect (e.g. code, zed)"
              />
            </div>
            <div class="form-control">
              <label class="label py-1" for="settings-slicer-command">
                <span class="label-text text-xs font-semibold">Slicer Command (optional)</span>
              </label>
              <input
                id="settings-slicer-command"
                type="text"
                bind:value={slicerCommand}
                class="input input-bordered input-sm w-full font-mono text-xs"
                placeholder="Auto-detect (e.g. orca-slicer)"
              />
            </div>
          </div>
        {/if}

        {#if formError}
          <Alert variant="error">{formError}</Alert>
        {/if}

        <div class="flex items-center justify-between gap-2 pt-2">
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            disabled={busy}
            onclick={() => void openOnboardingPreservingDrafts()}
          >
            Run Setup Wizard…
          </button>
          <div class="flex justify-end gap-2">
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
        </div>
      </form>
  </div>
</dialog>
