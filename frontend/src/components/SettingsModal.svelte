<script lang="ts">
  import { untrack } from 'svelte';
  import * as api from '../lib/api';
  import { pathBasename, pathsEqual, suggestLibraryName } from '../lib/paths';
  import { studio } from '../lib/studioState.svelte';
  import type {
    ExportQuality,
    HealthResponse,
    Library,
  } from '../lib/types';
  import Alert from './Alert.svelte';

  interface LibraryDraft {
    uid: string;
    id: string;
    name: string;
    path: string;
    modelCount?: number;
  }

  let uidCounter = 0;
  function nextUid(): string {
    uidCounter += 1;
    return `lib-draft-${uidCounter}`;
  }

  function countModelsForLibrary(libId: string): number | undefined {
    if (!libId || Boolean(studio.libraryErrors[libId])) return undefined;
    return studio.models.filter((m) => m.library_id === libId).length;
  }

  let dialogEl: HTMLDialogElement | undefined = $state();
  let pressedOutside = false;

  let libraries = $state<LibraryDraft[]>([]);
  let manualPathInput = $state('');
  let pythonPath = $state('');
  let exportDir = $state('');
  let editorCommand = $state('');
  let slicerCommand = $state('');
  let exportQuality = $state<ExportQuality>('standard');
  let resolvedPython = $state('');
  let resolvedExportDir = $state('');
  let workerRunning = $state(false);
  let workerStarting = $state(false);
  let workerError = $state('');
  let health = $state<HealthResponse | null>(null);
  let healthError = $state('');
  let healthLoading = $state(false);
  let saving = $state(false);
  let savedBanner = $state(false);
  let savedMessage = $state('');
  let formError = $state('');

  let pollTimer: ReturnType<typeof setTimeout> | null = null;

  let busy = $derived(saving);

  function clearPoll() {
    if (pollTimer !== null) {
      clearTimeout(pollTimer);
      pollTimer = null;
    }
  }

  function initializeSettings() {
    libraries = (studio.config.libraries ?? []).map((lib) => ({
      uid: nextUid(),
      id: lib.id,
      name: lib.name,
      path: lib.path,
      modelCount: countModelsForLibrary(lib.id),
    }));
    manualPathInput = '';
    pythonPath = studio.config.python_path;
    exportDir = studio.config.export_dir ?? '';
    editorCommand = studio.config.editor_command ?? '';
    slicerCommand = studio.config.slicer_command ?? '';
    exportQuality = studio.config.export_quality ?? 'standard';
    resolvedPython = studio.resolvedPython;
    resolvedExportDir = studio.resolvedExportDir;
    workerRunning = studio.workerRunning;
    workerError = studio.workerError;
    savedBanner = false;
    savedMessage = '';
    formError = '';
    void fetchHealth();
  }

  let dialogBodyEl: HTMLDivElement | undefined = $state();
  let wasOpen = false;
  $effect(() => {
    const el = dialogEl;
    const isOpen = studio.settingsOpen;
    if (!el) return;
    if (isOpen && !wasOpen) {
      wasOpen = true;
      untrack(() => initializeSettings());
      if (!el.open) {
        el.showModal();
      }
      dialogBodyEl?.focus({ preventScroll: true });
    } else if (!isOpen && wasOpen) {
      wasOpen = false;
      clearPoll();
      if (el.open) {
        el.close();
      }
    }
    return () => clearPoll();
  });

  async function fetchHealth() {
    clearPoll();
    healthLoading = true;
    healthError = '';
    try {
      const resp = await api.getHealth();
      workerRunning = resp.worker_running;
      workerStarting = Boolean(resp.worker_starting);
      workerError = resp.worker_error;
      health = resp.health ?? null;
      healthError = resp.health_error ?? '';
      if (workerStarting && studio.settingsOpen) {
        pollTimer = setTimeout(() => void fetchHealth(), 1200);
      }
    } catch (err) {
      healthError = err instanceof Error ? err.message : String(err);
    } finally {
      healthLoading = false;
    }
  }

  async function handleAddFolder() {
    formError = '';
    try {
      const lastPath = libraries[libraries.length - 1]?.path ?? '';
      const resp = await api.browseProject(lastPath);
      if (!resp.cancelled && resp.path) {
        const clean = resp.path.trim();
        const inspect = await api.inspectSetupDir(clean).catch(() => null);
        const resolvedPath = inspect?.ok && inspect.info.path ? inspect.info.path : clean;
        if (
          libraries.some(
            (l) =>
              pathsEqual(l.path, clean, studio.os) ||
              pathsEqual(l.path, resolvedPath, studio.os)
          )
        ) {
          formError = 'That library is already in your libraries list.';
          return;
        }
        libraries = [
          ...libraries,
          {
            uid: nextUid(),
            id: '',
            name: suggestLibraryName(
              resolvedPath,
              libraries.map((l) => l.name)
            ),
            path: resolvedPath,
            modelCount: inspect?.ok ? inspect.info.model_count : undefined,
          },
        ];
        savedBanner = false;
      }
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    }
  }

  async function handleAddManualPath() {
    const clean = manualPathInput.trim();
    if (!clean) return;
    formError = '';
    const inspect = await api.inspectSetupDir(clean).catch(() => null);
    const resolvedPath = inspect?.ok && inspect.info.path ? inspect.info.path : clean;
    if (
      libraries.some(
        (l) =>
          pathsEqual(l.path, clean, studio.os) ||
          pathsEqual(l.path, resolvedPath, studio.os)
      )
    ) {
      formError = 'That library is already in your libraries list.';
      return;
    }
    libraries = [
      ...libraries,
      {
        uid: nextUid(),
        id: '',
        name: suggestLibraryName(
          resolvedPath,
          libraries.map((l) => l.name)
        ),
        path: resolvedPath,
        modelCount: inspect?.ok ? inspect.info.model_count : undefined,
      },
    ];
    manualPathInput = '';
    savedBanner = false;
  }

  function moveLibrary(index: number, delta: -1 | 1) {
    const target = index + delta;
    if (target < 0 || target >= libraries.length) return;
    const next = [...libraries];
    const [item] = next.splice(index, 1);
    next.splice(target, 0, item);
    libraries = next;
    savedBanner = false;
  }

  function removeLibrary(index: number) {
    libraries = libraries.filter((_, i) => i !== index);
    savedBanner = false;
  }

  function buildPayloadLibraries(): Library[] {
    return libraries
      .filter((l) => l.path.trim() !== '')
      .map((l) => ({
        id: l.id,
        name: l.name.trim(),
        path: l.path.trim(),
      }));
  }

  async function persistCurrentConfig(): Promise<void> {
    const payloadLibs = buildPayloadLibraries();
    const resp = await api.saveConfig(payloadLibs, pythonPath, exportDir, {
      editor_command: editorCommand.trim(),
      slicer_command: slicerCommand.trim(),
      export_quality: exportQuality,
    });
    pythonPath = resp.config.python_path;
    exportDir = resp.config.export_dir ?? '';
    editorCommand = resp.config.editor_command ?? '';
    slicerCommand = resp.config.slicer_command ?? '';
    exportQuality = resp.config.export_quality ?? 'standard';
    resolvedPython = resp.resolved_python;
    resolvedExportDir = resp.resolved_export_dir;
    workerRunning = resp.worker_running;
    workerStarting = Boolean(resp.worker_starting);
    workerError = resp.worker_error;
    health = resp.health ?? null;
    healthError = resp.health_error ?? '';
    await studio.applySavedConfig(resp);
    libraries = (resp.config.libraries ?? []).map((lib) => ({
      uid: nextUid(),
      id: lib.id,
      name: lib.name,
      path: lib.path,
      modelCount: countModelsForLibrary(lib.id),
    }));
  }

  function hasUnsavedDraftChanges(): boolean {
    const draftLibs = buildPayloadLibraries();
    const curLibs = studio.config.libraries ?? [];
    if (draftLibs.length !== curLibs.length) return true;
    for (let i = 0; i < draftLibs.length; i++) {
      if (
        draftLibs[i].id !== curLibs[i].id ||
        draftLibs[i].name !== curLibs[i].name ||
        draftLibs[i].path !== curLibs[i].path
      ) {
        return true;
      }
    }
    return (
      pythonPath !== (studio.config.python_path ?? '') ||
      exportDir !== (studio.config.export_dir ?? '') ||
      editorCommand.trim() !== (studio.config.editor_command ?? '') ||
      slicerCommand.trim() !== (studio.config.slicer_command ?? '') ||
      exportQuality !== (studio.config.export_quality ?? 'standard')
    );
  }

  async function handleBrowsePython() {
    try {
      const resp = await api.browsePython(pythonPath || libraries[0]?.path || '');
      if (!resp.cancelled && resp.path) {
        pythonPath = resp.path;
      }
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    }
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
    savedBanner = false;
    formError = '';
    try {
      await persistCurrentConfig();
      savedMessage = 'Settings saved and worker reloaded.';
      savedBanner = true;
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  async function openOnboardingPreservingDrafts(step: 1 | 2) {
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
    studio.openOnboarding(step);
  }

  function closeModal() {
    if (busy) return;
    clearPoll();
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
    clearPoll();
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
    class="bg-base-100 border border-base-300 rounded-box shadow-md w-[36rem] max-w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto p-6 space-y-4 outline-none"
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
        <!-- Model Libraries Section -->
        <div class="space-y-2">
          <div class="flex items-start justify-between gap-2">
            <div>
              <span class="label-text text-xs font-semibold">
                Model Libraries ({libraries.length})
              </span>
              <p class="text-[11px] opacity-60">
                Folders containing <code>models/</code>, nested category folders, or standalone <code>.py</code> scripts.
              </p>
            </div>
            {#if studio.hasDesktop}
              <button
                type="button"
                class="btn btn-xs btn-ghost border border-base-300 h-6 min-h-6 px-2 gap-1 text-[11px] shrink-0"
                disabled={busy}
                onclick={handleAddFolder}
              >
                <svg
                  xmlns="http://www.w3.org/2000/svg"
                  class="h-3 w-3"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2.4"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  aria-hidden="true"
                >
                  <path d="M12 5v14" />
                  <path d="M5 12h14" />
                </svg>
                <span>Add Library</span>
              </button>
            {/if}
          </div>

          {#if libraries.length === 0}
            <div class="rounded-box border border-dashed border-base-300 p-3 text-xs opacity-70 text-center">
              No libraries configured yet. {studio.hasDesktop
                ? 'Click Add Library above'
                : 'Enter a folder path below'}, or run the Setup Wizard to try the offline demo or starter library.
            </div>
          {:else}
            <div class="space-y-1.5">
              {#each libraries as lib, idx (lib.uid)}
                {@const libErr = lib.id ? studio.libraryErrors[lib.id] : ''}
                <div class="rounded-field border border-base-300 bg-base-200/40 px-2.5 py-1.5 space-y-1">
                  <div class="flex items-center gap-2 min-w-0">
                    <input
                      type="text"
                      bind:value={lib.name}
                      disabled={busy}
                      placeholder={pathBasename(lib.path) || 'Library name'}
                      aria-label="Library display name"
                      class="input input-bordered input-xs w-36 font-medium text-xs shrink-0"
                    />
                    <span class="font-mono text-[11px] opacity-75 truncate flex-1 min-w-0" title={lib.path}>
                      {lib.path}
                    </span>
                    {#if lib.modelCount !== undefined && !libErr}
                      <span
                        class="rounded-full bg-base-content/10 text-base-content/70 px-1.5 py-0.5 text-[10px] font-medium tabular-nums shrink-0"
                        title="{lib.modelCount} model{lib.modelCount === 1 ? '' : 's'}"
                      >
                        {lib.modelCount}
                      </span>
                    {/if}
                    <div class="flex items-center gap-0.5 shrink-0">
                      {#if libraries.length > 1}
                        <button
                          type="button"
                          class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-0 opacity-60 hover:opacity-100 disabled:opacity-25"
                          title="Move up"
                          aria-label="Move library up"
                          disabled={busy || idx === 0}
                          onclick={() => moveLibrary(idx, -1)}
                        >
                          <svg xmlns="http://www.w3.org/2000/svg" class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                            <polyline points="18 15 12 9 6 15" />
                          </svg>
                        </button>
                        <button
                          type="button"
                          class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-0 opacity-60 hover:opacity-100 disabled:opacity-25"
                          title="Move down"
                          aria-label="Move library down"
                          disabled={busy || idx === libraries.length - 1}
                          onclick={() => moveLibrary(idx, 1)}
                        >
                          <svg xmlns="http://www.w3.org/2000/svg" class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                            <polyline points="6 9 12 15 18 9" />
                          </svg>
                        </button>
                      {/if}
                      <button
                        type="button"
                        class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-0 opacity-70 hover:opacity-100 hover:text-error"
                        title="Remove library"
                        aria-label="Remove library"
                        disabled={busy}
                        onclick={() => removeLibrary(idx)}
                      >
                        <svg xmlns="http://www.w3.org/2000/svg" class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                          <line x1="6" y1="6" x2="18" y2="18" />
                          <line x1="18" y1="6" x2="6" y2="18" />
                        </svg>
                      </button>
                    </div>
                  </div>
                  {#if libErr}
                    <div class="text-[11px] text-warning">{libErr}</div>
                  {/if}
                </div>
              {/each}
            </div>
          {/if}

          {#if !studio.hasDesktop}
            <!-- Fallback text entry for standalone browser mode without native folder dialogs -->
            <div class="join w-full">
              <input
                type="text"
                bind:value={manualPathInput}
                disabled={busy}
                onkeydown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault();
                    void handleAddManualPath();
                  }
                }}
                placeholder="Enter library folder path (e.g. ~/Documents/my-cad-models)..."
                aria-label="Library folder path"
                class="input input-bordered input-xs join-item w-full font-mono text-[11px]"
              />
              <button
                type="button"
                class="btn btn-xs btn-neutral join-item"
                disabled={busy || !manualPathInput.trim()}
                onclick={handleAddManualPath}
              >
                Add
              </button>
            </div>
          {/if}
        </div>

        <!-- Export Directory -->
        <div class="form-control">
          <label class="label py-1" for="settings-export-dir">
            <span class="label-text text-xs font-semibold">Global Export Folder (optional)</span>
          </label>
          <div class="join w-full">
            <input
              id="settings-export-dir"
              type="text"
              bind:value={exportDir}
              class="input input-bordered input-sm join-item w-full font-mono text-xs"
              placeholder="Per-library default (<library>/exports)"
            />
            {#if studio.hasDesktop}
              <button
                type="button"
                class="btn btn-sm btn-neutral join-item"
                onclick={handleBrowseExportDir}
              >
                Browse
              </button>
            {/if}
          </div>
          <span class="text-[11px] opacity-60 mt-1">
            Leave blank to export each model into its library's <code>exports/</code> folder, or set a shared folder for all exports.
            {#if exportDir.trim() && resolvedExportDir}
              Resolved: <code class="font-mono break-all">{resolvedExportDir}</code>
            {/if}
          </span>
        </div>

        <!-- Shared Python Interpreter -->
        <div class="form-control">
          <label class="label py-1" for="settings-python-path">
            <span class="label-text text-xs font-semibold">Shared Python Interpreter (optional)</span>
          </label>
          <div class="join w-full">
            <input
              id="settings-python-path"
              type="text"
              bind:value={pythonPath}
              class="input input-bordered input-sm join-item w-full font-mono text-xs"
              placeholder="Auto-detect (Studio venv, library .venv, or python3)"
            />
            {#if studio.hasDesktop}
              <button
                type="button"
                class="btn btn-sm btn-neutral join-item"
                onclick={handleBrowsePython}
              >
                Browse
              </button>
            {/if}
          </div>
          <span class="text-[11px] opacity-60 mt-1">
            Shared across all libraries. Resolved: <code class="font-mono break-all">{resolvedPython || 'python3'}</code>
          </span>
        </div>

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

        <!-- Worker Health Status -->
        <div class="rounded-box bg-base-200 p-3 text-xs space-y-1.5">
          <div class="flex items-center justify-between gap-2">
            <span class="font-semibold">Python Worker Status</span>
            <button
              type="button"
              class="btn btn-xs btn-ghost"
              disabled={busy}
              onclick={() => void openOnboardingPreservingDrafts(2)}
            >
              Configure Python…
            </button>
          </div>
          {#if workerStarting}
            <div class="opacity-70">Starting Python worker (importing build123d)...</div>
          {:else if healthLoading && !health}
            <div class="opacity-60">Checking Python worker...</div>
          {:else if workerError}
            <div class="text-error font-mono break-all">{workerError}</div>
          {:else if health}
            <div class="flex items-center gap-2">
              <span
                class="badge badge-xs"
                class:badge-success={health.build123d}
                class:badge-warning={!health.build123d}
              ></span>
              <span>
                {health.build123d
                  ? 'build123d available'
                  : 'build123d NOT found in this Python environment'}
              </span>
            </div>
            <div class="font-mono text-[11px] opacity-70 truncate">{health.python}</div>
            {#if !health.build123d}
              <div class="text-warning text-[11px]">
                Click <strong>Configure Python…</strong> to install the shared Studio environment or pick an existing <code>.venv</code>.
              </div>
            {/if}
          {:else if healthError}
            <div class="text-warning font-mono">{healthError}</div>
          {:else if !workerRunning}
            <div class="opacity-60">Not started</div>
          {:else}
            <div class="opacity-60">Running</div>
          {/if}
        </div>

        {#if formError}
          <Alert variant="error">{formError}</Alert>
        {/if}

        {#if savedBanner}
          <Alert variant="success">{savedMessage || 'Settings saved and worker reloaded.'}</Alert>
        {/if}

        <div class="flex items-center justify-between gap-2 pt-2">
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            disabled={busy}
            onclick={() => void openOnboardingPreservingDrafts(1)}
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
              Close
            </button>
            <button type="submit" class="btn btn-sm btn-primary" disabled={busy}>
              {saving ? 'Saving...' : 'Save & Reload'}
            </button>
          </div>
        </div>
      </form>
  </div>
</dialog>
