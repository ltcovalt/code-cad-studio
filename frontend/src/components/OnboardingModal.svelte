<script lang="ts">
  import * as api from '../lib/api';
  import {
    normalizePathKey,
    pathBasename,
    pathsEqual,
    suggestLibraryName,
  } from '../lib/paths';
  import { studio } from '../lib/studioState.svelte';
  import type {
    DetectedProject,
    Library,
    ProjectDirInfo,
    SetupDefaultsResponse,
  } from '../lib/types';
  import Alert from './Alert.svelte';
  import LibraryListEditor, {
    nextLibraryDraftUid,
    type LibraryDraft,
  } from './LibraryListEditor.svelte';
  import PythonEnvPanel from './PythonEnvPanel.svelte';

  type Step = 1 | 2;

  let dialogEl: HTMLDialogElement | undefined = $state();
  let dialogBodyEl: HTMLDivElement | undefined = $state();

  let step = $state<Step>(1);
  let loadingDefaults = $state(false);
  let defaults = $state<SetupDefaultsResponse | null>(null);

  // Step 1: libraries + optional starter library download + optional global export override
  let libraries = $state<LibraryDraft[]>([]);
  let includeStarter = $state(false);
  let starterDir = $state('');
  let starterDirInfo = $state<ProjectDirInfo | null>(null);
  let inspectingStarter = $state(false);
  let starterDownloadedPath = $state('');
  let allowOverwriteStarter = $state(false);
  let downloading = $state(false);
  let demoTargetDir = $state('');
  let step1Error = $state('');

  let exportDir = $state('');

  // Step 2: Python environment
  let selectedPythonPath = $state('');
  let envReady = $state(false);
  let installing = $state(false);

  let completing = $state(false);
  let scaffoldingDemo = $state(false);
  let completeError = $state('');
  let busyNotice = $state('');

  let inspectTimer: ReturnType<typeof setTimeout> | null = null;
  let pressedOutside = false;

  let currentOs = $derived(studio.os || defaults?.os || '');
  let busy = $derived(downloading || installing || completing || scaffoldingDemo);
  let showSkipButton = $derived(studio.needsOnboarding);
  let detectedProjects = $derived<DetectedProject[]>(defaults?.detected_projects ?? []);
  let unaddedDetectedProjects = $derived(
    detectedProjects.filter(
      (proj) => !libraries.some((l) => pathsEqual(l.path, proj.path, currentOs))
    )
  );

  let defaultDemoDir = $derived.by(() => {
    const dl = defaults?.default_download_dir?.trim() ?? '';
    if (!dl) return '';
    const sep = dl.includes('\\') ? '\\' : '/';
    const clean = dl.replace(/[\\/]+$/, '');
    const idx = Math.max(clean.lastIndexOf('/'), clean.lastIndexOf('\\'));
    if (idx <= 0) return '';
    return `${clean.slice(0, idx)}${sep}demo`;
  });

  let demoAdded = $derived(
    (Boolean(demoTargetDir) &&
      libraries.some((l) => pathsEqual(l.path, demoTargetDir, currentOs))) ||
      (Boolean(defaultDemoDir) &&
        libraries.some((l) => pathsEqual(l.path, defaultDemoDir, currentOs)))
  );

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

  let needsStarterDownload = $derived(
    includeStarter &&
      starterDir.trim() !== '' &&
      !pathsEqual(starterDownloadedPath, starterDir, currentOs)
  );

  let step1Blocked = $derived.by(() => {
    if (busy) return true;
    const validManualLibs = libraries.filter((l) => l.path.trim() !== '').length;
    const hasStarter = includeStarter && starterDir.trim() !== '';
    if (validManualLibs === 0 && !hasStarter) return true;
    if (needsStarterDownload) {
      if (starterDirInfo?.exists && !starterDirInfo.is_dir) return true;
      if (starterDirInfo?.exists && !starterDirInfo.is_empty && !allowOverwriteStarter) {
        return true;
      }
    }
    return false;
  });

  function formatBytes(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  function effectiveLibraryRoots(): string[] {
    const out: string[] = [];
    const seen = new Set<string>();
    for (const lib of libraries) {
      const p = lib.path.trim();
      const key = normalizePathKey(p, currentOs);
      if (key && !seen.has(key)) {
        seen.add(key);
        out.push(p);
      }
    }
    if (includeStarter) {
      const sp = (starterDownloadedPath || starterDir).trim();
      const key = normalizePathKey(sp, currentOs);
      if (key && !seen.has(key)) {
        seen.add(key);
        out.push(sp);
      }
    }
    return out;
  }

  function finalLibrariesPayload(): Library[] {
    const out: Library[] = [];
    const seen = new Set<string>();
    for (const lib of libraries) {
      const p = lib.path.trim();
      const key = normalizePathKey(p, currentOs);
      if (!key || seen.has(key)) continue;
      seen.add(key);
      out.push({ id: lib.id, name: lib.name.trim(), path: p });
    }
    if (includeStarter) {
      const sp = (starterDownloadedPath || starterDir).trim();
      const key = normalizePathKey(sp, currentOs);
      if (key && !seen.has(key)) {
        seen.add(key);
        const hasCodeCadName = out.some((l) => l.name.trim().toLowerCase() === 'code-cad');
        out.push({
          id: '',
          name: hasCodeCadName ? 'Starter Library' : 'code-cad',
          path: sp,
        });
      }
    }
    return out;
  }

  // Sync native <dialog> top-layer modal open state with studio.onboardingOpen
  let wasOpen = false;
  $effect(() => {
    const el = dialogEl;
    const isOpen = studio.onboardingOpen;
    if (!el) return;
    if (isOpen && !wasOpen) {
      wasOpen = true;
      if (!el.open) {
        el.showModal();
      }
      dialogBodyEl?.focus({ preventScroll: true });
      void initializeWizard();
    } else if (!isOpen && wasOpen) {
      wasOpen = false;
      if (el.open) {
        el.close();
      }
    }
  });

  async function initializeWizard() {
    step = 1;
    step1Error = '';
    starterDownloadedPath = '';
    allowOverwriteStarter = false;
    demoTargetDir = '';
    completeError = '';
    busyNotice = '';
    starterDirInfo = null;
    studio.clearSetupStream();

    loadingDefaults = true;
    try {
      const def = await api.getSetupDefaults();
      defaults = def;
      starterDir = def.default_download_dir;
      exportDir = studio.config.export_dir ?? '';
      selectedPythonPath = studio.config.python_path || '';

      if ((studio.config.libraries?.length ?? 0) > 0) {
        const persisted = studio.config.libraries;
        const inspected = await Promise.all(
          persisted.map((lib) => api.inspectSetupDir(lib.path).catch(() => null))
        );
        libraries = persisted.map((lib, idx) => ({
          uid: nextLibraryDraftUid(),
          id: lib.id,
          name: lib.name,
          path: lib.path,
          modelCount: inspected[idx]?.ok ? inspected[idx].info.model_count : undefined,
        }));
        includeStarter = false;
      } else if (def.detected_projects.length > 0) {
        libraries = def.detected_projects.map((p) => ({
          uid: nextLibraryDraftUid(),
          id: '',
          name: p.name,
          path: p.path,
          modelCount: p.model_count,
        }));
        includeStarter = false;
      } else {
        libraries = [];
        includeStarter = true;
      }

      if (includeStarter && starterDir) {
        void runInspectStarterDir(starterDir);
      }
    } catch (err) {
      step1Error = err instanceof Error ? err.message : String(err);
    } finally {
      loadingDefaults = false;
    }
  }

  function scheduleInspectStarter(path: string) {
    if (inspectTimer !== null) {
      clearTimeout(inspectTimer);
      inspectTimer = null;
    }
    inspectTimer = setTimeout(() => {
      void runInspectStarterDir(path);
    }, 180);
  }

  async function runInspectStarterDir(path: string) {
    const trimmed = path.trim();
    if (!trimmed) {
      starterDirInfo = null;
      return;
    }
    inspectingStarter = true;
    try {
      const resp = await api.inspectSetupDir(trimmed);
      if (resp.ok) {
        starterDirInfo = resp.info;
      }
    } catch {
      starterDirInfo = null;
    } finally {
      inspectingStarter = false;
    }
  }

  function handleStarterDirInput(val: string) {
    starterDir = val;
    step1Error = '';
    allowOverwriteStarter = false;
    if (!pathsEqual(starterDownloadedPath, val, currentOs)) {
      starterDownloadedPath = '';
    }
    scheduleInspectStarter(val);
  }

  function toggleIncludeStarter() {
    includeStarter = !includeStarter;
    step1Error = '';
    allowOverwriteStarter = false;
    if (includeStarter && starterDir) {
      void runInspectStarterDir(starterDir);
    }
  }

  function addDetectedProject(proj: DetectedProject) {
    step1Error = '';
    if (libraries.some((l) => pathsEqual(l.path, proj.path, currentOs))) return;
    libraries = [
      ...libraries,
      {
        uid: nextLibraryDraftUid(),
        id: '',
        name: suggestLibraryName(
          proj.path,
          libraries.map((l) => l.name)
        ),
        path: proj.path,
        modelCount: proj.model_count,
      },
    ];
  }

  async function handleBrowseStarterDir() {
    try {
      const resp = await api.browseProject(starterDir);
      if (!resp.cancelled && resp.path) {
        handleStarterDirInput(resp.path);
      }
    } catch (err) {
      step1Error = err instanceof Error ? err.message : String(err);
    }
  }

  async function handleBrowseExportDir() {
    try {
      const resp = await api.browseExportDir(exportDir || libraries[0]?.path || starterDir);
      if (!resp.cancelled && resp.path) {
        exportDir = resp.path;
      }
    } catch (err) {
      step1Error = err instanceof Error ? err.message : String(err);
    }
  }

  async function handleStep1Next() {
    step1Error = '';
    if (step1Blocked) return;

    if (needsStarterDownload) {
      downloading = true;
      studio.clearSetupStream();
      try {
        const resp = await api.downloadStarterProject(
          starterDir.trim(),
          defaults?.default_archive_url ?? '',
          allowOverwriteStarter
        );
        if (!resp.ok) {
          if (!resp.cancelled) {
            step1Error = resp.error || 'Failed to download code-cad starter library.';
          }
          return;
        }
        if (resp.project_info) {
          starterDirInfo = resp.project_info;
          starterDir = resp.project_info.path;
          starterDownloadedPath = resp.project_info.path;
        } else {
          starterDownloadedPath = starterDir.trim();
        }
      } catch (err) {
        step1Error = err instanceof Error ? err.message : String(err);
        return;
      } finally {
        downloading = false;
      }
    }

    step = 2;
  }

  async function handleCancelActiveTask() {
    try {
      await api.cancelSetupTask();
    } catch {
      // ignore
    }
  }

  async function handleFinish() {
    completeError = '';
    completing = true;
    try {
      const resp = await api.completeSetup({
        libraries: finalLibrariesPayload(),
        export_dir: exportDir.trim(),
        python_path: selectedPythonPath.trim(),
      });
      await studio.finishOnboarding(resp);
    } catch (err) {
      completeError = err instanceof Error ? err.message : String(err);
    } finally {
      completing = false;
    }
  }

  async function handleTryOfflineDemo() {
    if (busy || demoAdded) return;
    step1Error = '';
    scaffoldingDemo = true;
    try {
      const resp = await studio.scaffoldDemo(undefined, { selectModel: false });
      const targetDir = resp.target_dir?.trim() ?? '';
      if (targetDir) {
        demoTargetDir = targetDir;
        if (!libraries.some((l) => pathsEqual(l.path, targetDir, currentOs))) {
          libraries = [
            ...libraries,
            {
              uid: nextLibraryDraftUid(),
              id: resp.library_id || '',
              name: pathBasename(targetDir) || 'demo',
              path: targetDir,
              modelCount: 1,
            },
          ];
        }
      }
    } catch (err) {
      step1Error = err instanceof Error ? err.message : String(err);
    } finally {
      scaffoldingDemo = false;
    }
  }

  function handleCloseWizard() {
    if (busy) {
      busyNotice = 'Please wait for the active setup step to finish or click Cancel first.';
      return;
    }
    if (showSkipButton) {
      void studio.skipOnboarding();
      return;
    }
    studio.clearSetupStream();
    studio.onboardingOpen = false;
  }

  async function handleSkip() {
    if (busy) {
      busyNotice = 'Please wait for the active setup step to finish or click Cancel first.';
      return;
    }
    await studio.skipOnboarding();
  }

  function handleDialogCancel(e: Event) {
    if (busy) {
      e.preventDefault();
      busyNotice = 'Please cancel or wait for the current operation before closing.';
      return;
    }
    e.preventDefault();
    handleCloseWizard();
  }

  function handleDialogClose() {
    if (dialogEl) {
      dialogEl.returnValue = '';
    }
    wasOpen = false;
    pressedOutside = false;
    if (inspectTimer !== null) {
      clearTimeout(inspectTimer);
      inspectTimer = null;
    }
    if (studio.onboardingOpen) {
      studio.onboardingOpen = false;
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
      handleCloseWizard();
    }
    pressedOutside = false;
  }
</script>

<dialog
  bind:this={dialogEl}
  class="studio-dialog"
  aria-labelledby="onboarding-modal-title"
  {...({ closedby: busy ? 'none' : 'any' } as Record<string, unknown>)}
  oncancel={handleDialogCancel}
  onclose={handleDialogClose}
  onpointerdown={handleDialogPointerDown}
  onpointerup={handleDialogPointerUp}
>
  <div
    bind:this={dialogBodyEl}
    tabindex="-1"
    class="bg-base-100 border border-base-300 rounded-box shadow-lg w-[39rem] max-w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto p-6 space-y-5 focus:outline-none"
  >
    <!-- Header -->
    <div class="flex items-start justify-between gap-4">
      <div class="space-y-1">
        <h2 id="onboarding-modal-title" class="font-bold text-lg leading-tight">
          Welcome to Code CAD Studio
        </h2>
        <p class="text-xs opacity-70">
          Choose your build123d model libraries and configure the Python CAD environment.
        </p>
      </div>
      <button
        type="button"
        class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
        title="Close"
        aria-label="Close setup wizard"
        disabled={busy}
        onclick={handleCloseWizard}
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

    <!-- Step Indicator -->
    <ol class="grid grid-cols-2 gap-2 text-xs">
      <li
        class="flex items-center gap-2 rounded-box border px-3 py-2"
        class:border-primary={step === 1}
        class:border-base-300={step !== 1}
        class:opacity-70={step !== 1}
      >
        <span
          class="w-4 h-4 rounded-full flex items-center justify-center text-[10px] font-bold shrink-0"
          class:bg-primary={step === 1}
          class:text-primary-content={step === 1}
          class:bg-success={step > 1}
          class:text-success-content={step > 1}
        >
          {#if step > 1}
            <svg xmlns="http://www.w3.org/2000/svg" class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.2" aria-hidden="true">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
            </svg>
          {:else}
            1
          {/if}
        </span>
        <span class="font-semibold">Model Libraries</span>
      </li>
      <li
        class="flex items-center gap-2 rounded-box border px-3 py-2"
        class:border-primary={step === 2}
        class:border-base-300={step !== 2}
        class:opacity-60={step !== 2}
      >
        <span
          class="w-4 h-4 rounded-full flex items-center justify-center text-[10px] font-bold shrink-0"
          class:bg-primary={step === 2 && !envReady}
          class:text-primary-content={step === 2 && !envReady}
          class:bg-success={step === 2 && envReady}
          class:text-success-content={step === 2 && envReady}
          class:bg-base-300={step !== 2}
          class:text-base-content={step !== 2}
        >
          {#if step === 2 && envReady}
            <svg xmlns="http://www.w3.org/2000/svg" class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.2" aria-hidden="true">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
            </svg>
          {:else}
            2
          {/if}
        </span>
        <span class="font-semibold">Python &amp; build123d</span>
      </li>
    </ol>

    {#if busyNotice}
      <Alert variant="warning">{busyNotice}</Alert>
    {/if}

    {#if step === 1}
      <!-- STEP 1: Multi-Library Selection -->
      <div class="space-y-4">
        {#if loadingDefaults}
          <div class="flex items-center gap-2 text-xs opacity-70 py-4">
            <span class="loading loading-spinner loading-xs"></span>
            <span>Detecting local libraries…</span>
          </div>
        {:else}
          <!-- Unadded detected local libraries -->
          {#if unaddedDetectedProjects.length > 0}
            <div class="space-y-1.5">
              <div class="text-[11px] font-semibold opacity-75">
                Detected local {unaddedDetectedProjects.length === 1 ? 'library' : 'libraries'}
              </div>
              <div class="space-y-1.5">
                {#each unaddedDetectedProjects as proj (proj.path)}
                  <div
                    class="w-full rounded-field border border-base-300 bg-base-100 px-3 py-2 flex items-center justify-between gap-2"
                  >
                    <div class="flex items-center gap-2 min-w-0">
                      <span class="font-mono text-xs truncate">{proj.path}</span>
                      <span class="badge badge-xs badge-neutral shrink-0">
                        {proj.model_count} model{proj.model_count === 1 ? '' : 's'}
                      </span>
                    </div>
                    <button
                      type="button"
                      class="btn btn-xs btn-ghost shrink-0"
                      disabled={busy}
                      onclick={() => addDetectedProject(proj)}
                    >
                      + Add
                    </button>
                  </div>
                {/each}
              </div>
            </div>
          {/if}

          <!-- Offline Built-in Demo Option -->
          <div class="rounded-box border border-base-300 bg-base-200/40 p-3 flex items-center justify-between gap-3">
            <div class="space-y-0.5 min-w-0">
              <div class="font-semibold text-xs">Try Offline Demo</div>
              <p class="text-[11px] opacity-70 leading-snug">
                Create the built-in parametric demo library locally (no internet required).
              </p>
            </div>
            {#if demoAdded}
              <span
                class="inline-flex items-center gap-1 rounded-field border border-success/30 bg-success/10 px-2.5 py-1 text-xs font-medium text-success shrink-0"
              >
                <svg xmlns="http://www.w3.org/2000/svg" class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                </svg>
                Demo Added
              </span>
            {:else}
              <button
                type="button"
                class="btn btn-xs btn-secondary shrink-0"
                disabled={busy}
                onclick={handleTryOfflineDemo}
              >
                {#if scaffoldingDemo}
                  <span class="loading loading-spinner loading-xs"></span>
                  Creating…
                {:else}
                  Try Offline Demo
                {/if}
              </button>
            {/if}
          </div>

          <!-- Starter Library Option (can be added alongside existing libraries!) -->
          <div
            class="rounded-box border p-3 space-y-2.5 transition-colors {includeStarter
              ? 'border-primary bg-base-200/50'
              : 'border-base-300'}"
          >
            <label class="flex items-start gap-2.5 cursor-pointer">
              <input
                type="checkbox"
                class="checkbox checkbox-xs checkbox-primary mt-0.5 shrink-0"
                checked={includeStarter}
                disabled={busy}
                onchange={toggleIncludeStarter}
              />
              <div class="space-y-0.5 min-w-0">
                <div class="font-semibold text-xs">
                  Download <code>code-cad</code> starter library
                </div>
                <p class="text-[11px] opacity-70 leading-snug">
                  Download the official starter models from GitHub into a new library folder.
                </p>
              </div>
            </label>

            {#if includeStarter}
              <div class="pl-6 space-y-1.5">
                <div class="join w-full">
                  <input
                    id="onboarding-starter-dir"
                    type="text"
                    value={starterDir}
                    oninput={(e) => handleStarterDirInput((e.currentTarget as HTMLInputElement).value)}
                    disabled={busy}
                    class="input input-bordered input-xs join-item w-full font-mono text-[11px]"
                    placeholder="/path/to/code-cad"
                  />
                  {#if studio.hasDesktop}
                    <button
                      type="button"
                      class="btn btn-xs btn-neutral join-item"
                      disabled={busy}
                      onclick={handleBrowseStarterDir}
                    >
                      Browse
                    </button>
                  {/if}
                </div>

                <div class="min-h-[1rem] text-[11px]">
                  {#if inspectingStarter}
                    <span class="opacity-60">Checking destination folder…</span>
                  {:else if starterDownloadedPath && pathsEqual(starterDownloadedPath, starterDir, currentOs)}
                    <span class="text-success font-medium">
                      ✓ Starter library downloaded ({starterDirInfo?.model_count ?? 0} models found).
                    </span>
                  {:else if starterDirInfo?.exists && !starterDirInfo.is_dir}
                    <span class="text-error">Path exists and is a file, not a folder.</span>
                  {:else if starterDirInfo?.exists && !starterDirInfo.is_empty}
                    <Alert variant="warning" title="Folder is not empty" class="mt-1">
                      <p>
                        {#if starterDirInfo.has_models}
                          This folder already has {starterDirInfo.model_count} model{starterDirInfo.model_count === 1 ? '' : 's'}.
                        {:else}
                          This folder is not empty.
                        {/if}
                        Choose an empty folder name (e.g., <code>code-cad-starter</code>) or confirm overwriting matching files in <code>models/</code> and <code>utils/</code>.
                      </p>
                      <label class="flex items-start gap-2 cursor-pointer pt-0.5 text-xs">
                        <input
                          type="checkbox"
                          class="checkbox checkbox-xs checkbox-warning mt-0.5"
                          disabled={busy}
                          bind:checked={allowOverwriteStarter}
                        />
                        <span>Allow overwriting matching files in this folder</span>
                      </label>
                    </Alert>
                  {:else if starterDir.trim()}
                    <span class="text-success/90">
                      ✓ Ready — Studio will download <code>code-cad</code> here.
                    </span>
                  {/if}
                </div>
              </div>
            {/if}
          </div>

          <!-- Selected Local Libraries List -->
          <LibraryListEditor
            bind:libraries
            title="Your Local Libraries"
            os={currentOs}
            {busy}
            onError={(msg) => (step1Error = msg)}
          />

          <!-- Optional Global Export Folder -->
          <div class="form-control">
            <label class="label py-1" for="onboarding-export-dir">
              <span class="label-text text-xs font-semibold">Global Export Folder (optional)</span>
            </label>
            <div class="join w-full">
              <input
                id="onboarding-export-dir"
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

          <!-- Download Progress -->
          {#if downloading}
            <div class="rounded-box bg-base-200 border border-base-300 p-3 space-y-2 text-xs">
              <div class="flex items-center justify-between gap-2">
                <span class="font-medium truncate">
                  {studio.setupProgress?.message || 'Downloading code-cad starter library…'}
                </span>
                <button
                  type="button"
                  class="btn btn-xs btn-ghost text-error"
                  onclick={handleCancelActiveTask}
                >
                  Cancel
                </button>
              </div>
              {#if downloadProgressPct !== null}
                <progress
                  class="progress progress-primary w-full"
                  value={downloadProgressPct}
                  max="100"
                ></progress>
              {:else}
                <progress class="progress progress-primary w-full"></progress>
              {/if}
              {#if studio.setupProgress?.bytes_downloaded}
                <div class="text-[11px] opacity-70 font-mono">
                  {formatBytes(studio.setupProgress.bytes_downloaded)}
                  {#if studio.setupProgress.total_bytes && studio.setupProgress.total_bytes > 0}
                    / {formatBytes(studio.setupProgress.total_bytes)}
                  {/if}
                </div>
              {/if}
            </div>
          {/if}

          {#if step1Error}
            <Alert variant="error">{step1Error}</Alert>
          {/if}
        {/if}
      </div>

      <!-- Step 1 Footer -->
      <div class="flex items-center justify-between gap-2 pt-2 border-t border-base-300/70">
        {#if showSkipButton}
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            disabled={busy}
            onclick={handleSkip}
          >
            Skip for now
          </button>
        {:else}
          <div></div>
        {/if}
        <button
          type="button"
          class="btn btn-sm btn-primary"
          disabled={step1Blocked || loadingDefaults}
          onclick={handleStep1Next}
        >
          {#if downloading}
            <span class="loading loading-spinner loading-xs"></span>
            Downloading…
          {:else if needsStarterDownload}
            Download & Continue
          {:else}
            Continue
          {/if}
        </button>
      </div>
    {:else}
      <!-- STEP 2: Python & build123d Environment -->
      <div class="space-y-4">
        <PythonEnvPanel
          bind:selectedPythonPath
          bind:envReady
          bind:installing
          libraryRoots={effectiveLibraryRoots()}
          os={currentOs}
          studioVenvDirFallback={defaults?.studio_venv_dir ?? ''}
          disabled={completing}
          autoProbe={true}
        />

        <!-- Ready Banner -->
        {#if envReady && !installing}
          <Alert variant="success">
            Everything is ready! Click <strong>Finish</strong> to save and apply.
          </Alert>
        {/if}

        {#if completeError}
          <Alert variant="error">{completeError}</Alert>
        {/if}
      </div>

      <!-- Step 2 Footer -->
      <div class="flex items-center justify-between gap-2 pt-2 border-t border-base-300/70">
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            disabled={busy}
            onclick={() => {
              step = 1;
              completeError = '';
            }}
          >
            Back
          </button>
          {#if showSkipButton}
            <button
              type="button"
              class="btn btn-sm btn-ghost opacity-75"
              disabled={busy}
              onclick={handleSkip}
            >
              Skip for now
            </button>
          {/if}
        </div>

        <button
          type="button"
          class="btn btn-sm btn-primary"
          disabled={busy || !envReady}
          onclick={handleFinish}
        >
          {#if completing}
            <span class="loading loading-spinner loading-xs"></span>
            Saving…
          {:else}
            Finish
          {/if}
        </button>
      </div>
    {/if}
  </div>
</dialog>
