<script lang="ts">
  import { untrack } from 'svelte';
  import * as api from '../lib/api';
  import { studio } from '../lib/studioState.svelte';
  import type {
    ExportQuality,
    HealthResponse,
    Library,
    ProjectDirInfo,
    SetupDefaultsResponse,
  } from '../lib/types';
  import Alert from './Alert.svelte';

  interface LibraryDraft {
    uid: string;
    id: string;
    name: string;
    path: string;
  }

  let uidCounter = 0;
  function nextUid(): string {
    uidCounter += 1;
    return `lib-draft-${uidCounter}`;
  }

  function basenameOf(p: string): string {
    const clean = p.trim().replace(/[\\/]+$/, '');
    const parts = clean.split(/[\\/]/).filter(Boolean);
    return parts[parts.length - 1] ?? '';
  }

  function parentOf(p: string): string {
    const clean = p.trim().replace(/[\\/]+$/, '');
    const parts = clean.split(/[\\/]/).filter(Boolean);
    return parts.length >= 2 ? parts[parts.length - 2] : '';
  }

  function suggestLibraryName(path: string, existing: LibraryDraft[]): string {
    const base = basenameOf(path) || 'Library';
    const clash = existing.some((l) => l.name.trim().toLowerCase() === base.toLowerCase());
    if (clash) {
      const parent = parentOf(path);
      if (parent) {
        return `${base} (${parent})`;
      }
    }
    return base;
  }

  function formatBytes(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  let dialogEl: HTMLDialogElement | undefined = $state();
  let pressedOutside = false;

  let libraries = $state<LibraryDraft[]>([]);
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
  let formError = $state('');

  // Inline starter library downloader state
  let starterOpen = $state(false);
  let setupDefaults = $state<SetupDefaultsResponse | null>(null);
  let starterDir = $state('');
  let starterDirInfo = $state<ProjectDirInfo | null>(null);
  let starterAllowOverwrite = $state(false);
  let downloadingStarter = $state(false);
  let starterError = $state('');

  let pollTimer: ReturnType<typeof setTimeout> | null = null;
  let starterInspectTimer: ReturnType<typeof setTimeout> | null = null;

  let downloadProgressPct = $derived.by<number | null>(() => {
    const p = studio.setupProgress;
    if (!p || p.task !== 'download') return null;
    if (
      p.stage === 'downloading' &&
      p.total_bytes &&
      p.total_bytes > 0 &&
      p.bytes_downloaded !== undefined
    ) {
      return Math.min(100, Math.round((p.bytes_downloaded / p.total_bytes) * 100));
    }
    if (
      p.stage === 'extracting' &&
      p.total_files &&
      p.total_files > 0 &&
      p.files_extracted !== undefined
    ) {
      return Math.min(100, Math.round((p.files_extracted / p.total_files) * 100));
    }
    return null;
  });

  function clearPoll() {
    if (pollTimer !== null) {
      clearTimeout(pollTimer);
      pollTimer = null;
    }
    if (starterInspectTimer !== null) {
      clearTimeout(starterInspectTimer);
      starterInspectTimer = null;
    }
  }

  function initializeSettings() {
    libraries = (studio.config.libraries ?? []).map((lib) => ({
      uid: nextUid(),
      id: lib.id,
      name: lib.name,
      path: lib.path,
    }));
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
    formError = '';
    starterOpen = false;
    starterError = '';
    starterAllowOverwrite = false;
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

  function addManualLibraryRow() {
    libraries = [...libraries, { uid: nextUid(), id: '', name: '', path: '' }];
    savedBanner = false;
  }

  async function handleAddFolder() {
    formError = '';
    if (!studio.hasDesktop) {
      addManualLibraryRow();
      return;
    }
    try {
      const lastPath = libraries[libraries.length - 1]?.path ?? '';
      const resp = await api.browseProject(lastPath);
      if (!resp.cancelled && resp.path) {
        const clean = resp.path.trim();
        if (libraries.some((l) => l.path.trim() === clean)) {
          formError = 'That folder is already in your libraries list.';
          return;
        }
        libraries = [
          ...libraries,
          {
            uid: nextUid(),
            id: '',
            name: suggestLibraryName(clean, libraries),
            path: clean,
          },
        ];
        savedBanner = false;
      }
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    }
  }

  async function handleBrowseLibraryRow(index: number) {
    try {
      const current = libraries[index]?.path ?? '';
      const resp = await api.browseProject(current);
      if (!resp.cancelled && resp.path) {
        const next = [...libraries];
        const prev = next[index];
        const prevBase = basenameOf(prev.path);
        const shouldAutoName = !prev.name.trim() || prev.name.trim() === prevBase;
        next[index] = {
          ...prev,
          path: resp.path,
          name: shouldAutoName
            ? suggestLibraryName(resp.path, next.filter((_, idx) => idx !== index))
            : prev.name,
        };
        libraries = next;
        savedBanner = false;
      }
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    }
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

  async function toggleStarterPanel() {
    starterOpen = !starterOpen;
    starterError = '';
    starterAllowOverwrite = false;
    if (starterOpen) {
      if (!setupDefaults) {
        try {
          setupDefaults = await api.getSetupDefaults();
        } catch {
          // ignore
        }
      }
      if (!starterDir && setupDefaults?.default_download_dir) {
        starterDir = setupDefaults.default_download_dir;
      }
      if (starterDir) {
        void inspectStarterDir(starterDir);
      }
    }
  }

  async function inspectStarterDir(path: string) {
    const trimmed = path.trim();
    if (!trimmed) {
      starterDirInfo = null;
      return;
    }
    try {
      const resp = await api.inspectSetupDir(trimmed);
      if (resp.ok) {
        starterDirInfo = resp.info;
      }
    } catch {
      starterDirInfo = null;
    }
  }

  function handleStarterDirInput(val: string) {
    starterDir = val;
    starterError = '';
    starterAllowOverwrite = false;
    if (starterInspectTimer !== null) clearTimeout(starterInspectTimer);
    starterInspectTimer = setTimeout(() => void inspectStarterDir(val), 180);
  }

  async function handleBrowseStarterDir() {
    try {
      const resp = await api.browseProject(starterDir);
      if (!resp.cancelled && resp.path) {
        handleStarterDirInput(resp.path);
      }
    } catch (err) {
      starterError = err instanceof Error ? err.message : String(err);
    }
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
    libraries = (resp.config.libraries ?? []).map((lib) => ({
      uid: nextUid(),
      id: lib.id,
      name: lib.name,
      path: lib.path,
    }));
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

  async function handleDownloadStarter() {
    const target = starterDir.trim();
    if (!target) {
      starterError = 'Please choose a destination folder.';
      return;
    }
    downloadingStarter = true;
    starterError = '';
    studio.clearSetupStream();
    try {
      const resp = await api.downloadStarterProject(
        target,
        setupDefaults?.default_archive_url ?? '',
        starterAllowOverwrite
      );
      if (!resp.ok) {
        if (!resp.cancelled) {
          starterError = resp.error || 'Failed to download starter library.';
        }
        return;
      }
      const resolvedPath = resp.project_info?.path || target;
      if (!libraries.some((l) => l.path.trim() === resolvedPath)) {
        const defaultName = libraries.some((l) => l.name.trim().toLowerCase() === 'code-cad')
          ? 'Starter Library'
          : 'code-cad';
        libraries = [
          ...libraries,
          {
            uid: nextUid(),
            id: '',
            name: defaultName,
            path: resolvedPath,
          },
        ];
      }
      starterOpen = false;
      starterDirInfo = resp.project_info ?? null;
      await persistCurrentConfig();
      savedBanner = true;
    } catch (err) {
      starterError = err instanceof Error ? err.message : String(err);
    } finally {
      downloadingStarter = false;
    }
  }

  async function handleCancelStarterDownload() {
    try {
      await api.cancelSetupTask();
    } catch {
      // ignore
    }
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
      savedBanner = true;
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  async function openOnboardingPreservingDrafts(step: 1 | 2) {
    if (downloadingStarter || saving) return;
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
    if (downloadingStarter || saving) return;
    clearPoll();
    studio.settingsOpen = false;
  }

  function handleDialogCancel(e: Event) {
    if (downloadingStarter || saving) {
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

  let starterBlocked = $derived(
    downloadingStarter ||
      !starterDir.trim() ||
      Boolean(starterDirInfo?.exists && !starterDirInfo.is_dir) ||
      Boolean(starterDirInfo?.exists && !starterDirInfo.is_empty && !starterAllowOverwrite)
  );
</script>

<dialog
  bind:this={dialogEl}
  class="studio-dialog"
  aria-labelledby="settings-modal-title"
  {...({ closedby: downloadingStarter || saving ? 'none' : 'any' } as Record<string, unknown>)}
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
        disabled={downloadingStarter || saving}
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
          <div class="flex items-center justify-between gap-2">
            <div>
              <span class="label-text text-xs font-semibold">Model Libraries</span>
              <p class="text-[11px] opacity-60">
                Add one or more project folders containing <code>models/</code>, nested category folders, or standalone <code>.py</code> scripts.
              </p>
            </div>
          </div>

          {#if libraries.length === 0}
            <div class="rounded-box border border-dashed border-base-300 p-3 text-xs opacity-70 text-center">
              No libraries configured yet. Add a local folder or download the starter library below.
            </div>
          {:else}
            <div class="space-y-2">
              {#each libraries as lib, idx (lib.uid)}
                {@const libErr = lib.id ? studio.libraryErrors[lib.id] : ''}
                <div class="rounded-box border border-base-300 bg-base-200/40 p-2.5 space-y-1.5">
                  <div class="flex items-center gap-1.5">
                    <input
                      type="text"
                      bind:value={lib.name}
                      placeholder={basenameOf(lib.path) || 'Library name'}
                      aria-label="Library display name"
                      class="input input-bordered input-xs flex-1 font-medium text-xs"
                    />
                    <div class="flex items-center gap-0.5 shrink-0">
                      <button
                        type="button"
                        class="btn btn-ghost btn-xs btn-square"
                        title="Move up"
                        aria-label="Move library up"
                        disabled={idx === 0}
                        onclick={() => moveLibrary(idx, -1)}
                      >
                        <svg xmlns="http://www.w3.org/2000/svg" class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                          <polyline points="18 15 12 9 6 15" />
                        </svg>
                      </button>
                      <button
                        type="button"
                        class="btn btn-ghost btn-xs btn-square"
                        title="Move down"
                        aria-label="Move library down"
                        disabled={idx === libraries.length - 1}
                        onclick={() => moveLibrary(idx, 1)}
                      >
                        <svg xmlns="http://www.w3.org/2000/svg" class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                          <polyline points="6 9 12 15 18 9" />
                        </svg>
                      </button>
                      <button
                        type="button"
                        class="btn btn-ghost btn-xs btn-square hover:text-error"
                        title="Remove library"
                        aria-label="Remove library"
                        onclick={() => removeLibrary(idx)}
                      >
                        <svg xmlns="http://www.w3.org/2000/svg" class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                          <line x1="6" y1="6" x2="18" y2="18" />
                          <line x1="18" y1="6" x2="6" y2="18" />
                        </svg>
                      </button>
                    </div>
                  </div>
                  <div class="join w-full">
                    <input
                      type="text"
                      bind:value={lib.path}
                      placeholder="/path/to/code-cad"
                      aria-label="Library folder path"
                      class="input input-bordered input-xs join-item w-full font-mono text-[11px]"
                    />
                    {#if studio.hasDesktop}
                      <button
                        type="button"
                        class="btn btn-xs btn-neutral join-item"
                        onclick={() => handleBrowseLibraryRow(idx)}
                      >
                        Browse
                      </button>
                    {/if}
                  </div>
                  {#if libErr}
                    <div class="text-[11px] text-warning">{libErr}</div>
                  {/if}
                </div>
              {/each}
            </div>
          {/if}

          <div class="flex flex-wrap items-center gap-2 pt-0.5">
            <button
              type="button"
              class="btn btn-xs btn-neutral"
              disabled={downloadingStarter}
              onclick={handleAddFolder}
            >
              + Add Folder
            </button>
            <button
              type="button"
              class="btn btn-xs btn-ghost ml-auto"
              disabled={downloadingStarter}
              onclick={toggleStarterPanel}
            >
              {starterOpen ? 'Cancel Download' : 'Download Starter Library…'}
            </button>
          </div>

          <!-- Inline Starter Library Download Panel -->
          {#if starterOpen}
            <div class="rounded-box border border-primary/40 bg-base-200/60 p-3 space-y-2 text-xs">
              <div class="font-semibold">Download <code>code-cad</code> Starter Library</div>
              <p class="text-[11px] opacity-75">
                Downloads the starter models into a dedicated folder and adds it as its own library alongside your existing folders.
              </p>
              <div class="join w-full">
                <input
                  type="text"
                  value={starterDir}
                  oninput={(e) => handleStarterDirInput((e.currentTarget as HTMLInputElement).value)}
                  disabled={downloadingStarter}
                  placeholder="/path/to/code-cad-starter"
                  class="input input-bordered input-xs join-item w-full font-mono text-[11px]"
                />
                {#if studio.hasDesktop}
                  <button
                    type="button"
                    class="btn btn-xs btn-neutral join-item"
                    disabled={downloadingStarter}
                    onclick={handleBrowseStarterDir}
                  >
                    Browse
                  </button>
                {/if}
              </div>

              {#if starterDirInfo?.exists && !starterDirInfo.is_dir}
                <div class="text-[11px] text-error">Path exists and is a file, not a folder.</div>
              {:else if starterDirInfo?.exists && !starterDirInfo.is_empty}
                <Alert variant="warning" compact>
                  <div class="space-y-1">
                    <div>
                      This folder is not empty. Downloading here will overwrite matching files in <code>models/</code> and <code>utils/</code>.
                    </div>
                    <label class="flex items-center gap-1.5 cursor-pointer">
                      <input
                        type="checkbox"
                        class="checkbox checkbox-xs checkbox-warning"
                        disabled={downloadingStarter}
                        bind:checked={starterAllowOverwrite}
                      />
                      <span>Allow overwriting matching files</span>
                    </label>
                  </div>
                </Alert>
              {/if}

              {#if downloadingStarter}
                <div class="space-y-1.5">
                  <div class="flex items-center justify-between gap-2 text-[11px]">
                    <span class="truncate">
                      {studio.setupProgress?.message || 'Downloading starter library…'}
                    </span>
                    <button
                      type="button"
                      class="btn btn-xs btn-ghost text-error"
                      onclick={handleCancelStarterDownload}
                    >
                      Cancel
                    </button>
                  </div>
                  {#if downloadProgressPct !== null}
                    <progress class="progress progress-primary w-full" value={downloadProgressPct} max="100"></progress>
                  {:else}
                    <progress class="progress progress-primary w-full"></progress>
                  {/if}
                  {#if studio.setupProgress?.bytes_downloaded}
                    <div class="text-[10px] opacity-70 font-mono">
                      {formatBytes(studio.setupProgress.bytes_downloaded)}
                      {#if studio.setupProgress.total_bytes && studio.setupProgress.total_bytes > 0}
                        / {formatBytes(studio.setupProgress.total_bytes)}
                      {/if}
                    </div>
                  {/if}
                </div>
              {:else}
                <div class="flex justify-end">
                  <button
                    type="button"
                    class="btn btn-xs btn-primary"
                    disabled={starterBlocked}
                    onclick={handleDownloadStarter}
                  >
                    Download & Add Library
                  </button>
                </div>
              {/if}

              {#if starterError}
                <Alert variant="error" compact>{starterError}</Alert>
              {/if}
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
              disabled={downloadingStarter || saving}
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
          <Alert variant="success">Settings saved and worker reloaded.</Alert>
        {/if}

        <div class="flex items-center justify-between gap-2 pt-2">
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            disabled={downloadingStarter || saving}
            onclick={() => void openOnboardingPreservingDrafts(1)}
          >
            Run Setup Wizard…
          </button>
          <div class="flex justify-end gap-2">
            <button
              type="button"
              class="btn btn-sm btn-ghost"
              disabled={downloadingStarter || saving}
              onclick={closeModal}
            >
              Close
            </button>
            <button type="submit" class="btn btn-sm btn-primary" disabled={saving || downloadingStarter}>
              {saving ? 'Saving...' : 'Save & Reload'}
            </button>
          </div>
        </div>
      </form>
  </div>
</dialog>
