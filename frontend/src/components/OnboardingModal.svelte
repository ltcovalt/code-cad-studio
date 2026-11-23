<script lang="ts">
  import * as api from '../lib/api';
  import { studio } from '../lib/studioState.svelte';
  import type {
    DetectedProject,
    Library,
    ProjectDirInfo,
    PythonCandidate,
    SetupDefaultsResponse,
    SetupProbeResponse,
  } from '../lib/types';
  import Alert from './Alert.svelte';

  type Step = 1 | 2;

  interface LibraryItem {
    uid: string;
    id: string;
    name: string;
    path: string;
    modelCount?: number;
  }

  let uidCounter = 0;
  function nextUid(): string {
    uidCounter += 1;
    return `onb-lib-${uidCounter}`;
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

  function suggestName(path: string, existing: LibraryItem[]): string {
    const base = basenameOf(path) || 'Library';
    if (existing.some((l) => l.name.trim().toLowerCase() === base.toLowerCase())) {
      const parent = parentOf(path);
      if (parent) return `${base} (${parent})`;
    }
    return base;
  }

  let dialogEl: HTMLDialogElement | undefined = $state();
  let dialogBodyEl: HTMLDivElement | undefined = $state();

  let step = $state<Step>(1);
  let loadingDefaults = $state(false);
  let defaults = $state<SetupDefaultsResponse | null>(null);

  // Step 1: libraries + optional starter library download + optional global export override
  let libraries = $state<LibraryItem[]>([]);
  let manualPathInput = $state('');
  let includeStarter = $state(false);
  let starterDir = $state('');
  let starterDirInfo = $state<ProjectDirInfo | null>(null);
  let inspectingStarter = $state(false);
  let starterDownloadedPath = $state('');
  let allowOverwriteStarter = $state(false);
  let downloading = $state(false);
  let step1Error = $state('');

  let exportDir = $state('');

  // Step 2: shared Python environment
  let probing = $state(false);
  let probe = $state<SetupProbeResponse | null>(null);
  let probeError = $state('');
  let selectedPythonPath = $state('');

  let installing = $state(false);
  let installError = $state('');
  let showInstallLogs = $state(true);

  let completing = $state(false);
  let completeError = $state('');
  let busyNotice = $state('');

  let inspectTimer: ReturnType<typeof setTimeout> | null = null;
  let logContainerEl: HTMLPreElement | undefined = $state();

  let busy = $derived(downloading || installing || completing);
  let detectedProjects = $derived<DetectedProject[]>(defaults?.detected_projects ?? []);
  let unaddedDetectedProjects = $derived(
    detectedProjects.filter((proj) => !libraries.some((l) => l.path.trim() === proj.path))
  );

  let selectedCandidate = $derived.by<PythonCandidate | null>(() => {
    if (!probe || probe.candidates.length === 0) return null;
    if (selectedPythonPath) {
      const exact = probe.candidates.find((c) => c.path === selectedPythonPath);
      if (exact) return exact;
    }
    return probe.recommended ?? probe.candidates[0] ?? null;
  });

  let envReady = $derived(
    Boolean(
      selectedCandidate &&
        !selectedCandidate.error &&
        selectedCandidate.version_ok &&
        selectedCandidate.has_build123d
    )
  );

  let canInstallEnv = $derived(
    Boolean(
      !envReady &&
        ((selectedCandidate &&
          !selectedCandidate.error &&
          selectedCandidate.version_ok &&
          (selectedCandidate.has_venv || probe?.has_uv)) ||
          probe?.has_uv)
    )
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
      starterDownloadedPath !== starterDir.trim()
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

  function sourceLabel(cand: PythonCandidate): string {
    switch (cand.source) {
      case 'studio_venv':
        return 'Studio venv';
      case 'library_venv':
        return cand.source_detail ? `Library .venv (${cand.source_detail})` : 'Library .venv';
      case 'configured':
        return 'Selected';
      case 'path':
        return 'System PATH';
      case 'conda':
        return 'Conda';
      case 'pyenv':
        return 'pyenv';
      case 'system':
        return 'System';
      default:
        return cand.source || 'Python';
    }
  }

  function effectiveLibraryRoots(): string[] {
    const out: string[] = [];
    const seen = new Set<string>();
    for (const lib of libraries) {
      const p = lib.path.trim();
      if (p && !seen.has(p)) {
        seen.add(p);
        out.push(p);
      }
    }
    if (includeStarter) {
      const sp = (starterDownloadedPath || starterDir).trim();
      if (sp && !seen.has(sp)) {
        seen.add(sp);
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
      if (!p || seen.has(p)) continue;
      seen.add(p);
      out.push({ id: lib.id, name: lib.name.trim(), path: p });
    }
    if (includeStarter) {
      const sp = (starterDownloadedPath || starterDir).trim();
      if (sp && !seen.has(sp)) {
        seen.add(sp);
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

  // Auto-scroll live install log box as new SSE lines arrive
  $effect(() => {
    const _len = studio.setupLogs.length;
    if (logContainerEl && showInstallLogs && _len > 0) {
      logContainerEl.scrollTop = logContainerEl.scrollHeight;
    }
  });

  async function initializeWizard() {
    step = studio.onboardingInitialStep;
    step1Error = '';
    starterDownloadedPath = '';
    allowOverwriteStarter = false;
    probeError = '';
    installError = '';
    completeError = '';
    busyNotice = '';
    probe = null;
    starterDirInfo = null;
    manualPathInput = '';
    studio.clearSetupStream();

    loadingDefaults = true;
    try {
      const def = await api.getSetupDefaults();
      defaults = def;
      starterDir = def.default_download_dir;
      exportDir = studio.config.export_dir ?? '';
      selectedPythonPath = studio.config.python_path || '';

      if ((studio.config.libraries?.length ?? 0) > 0) {
        libraries = studio.config.libraries.map((lib) => ({
          uid: nextUid(),
          id: lib.id,
          name: lib.name,
          path: lib.path,
        }));
        includeStarter = false;
      } else if (def.detected_projects.length > 0) {
        libraries = def.detected_projects.map((p) => ({
          uid: nextUid(),
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

      if (step === 2) {
        await runProbe(selectedPythonPath);
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
    if (starterDownloadedPath !== val.trim()) {
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
    if (libraries.some((l) => l.path.trim() === proj.path)) return;
    libraries = [
      ...libraries,
      {
        uid: nextUid(),
        id: '',
        name: suggestName(proj.path, libraries),
        path: proj.path,
        modelCount: proj.model_count,
      },
    ];
  }

  async function handleAddFolder() {
    step1Error = '';
    try {
      const start = libraries[libraries.length - 1]?.path ?? '';
      const resp = await api.browseProject(start);
      if (!resp.cancelled && resp.path) {
        const clean = resp.path.trim();
        if (libraries.some((l) => l.path.trim() === clean)) {
          step1Error = 'That folder is already selected.';
          return;
        }
        const inspect = await api.inspectSetupDir(clean).catch(() => null);
        libraries = [
          ...libraries,
          {
            uid: nextUid(),
            id: '',
            name: suggestName(clean, libraries),
            path: clean,
            modelCount: inspect?.ok ? inspect.info.model_count : undefined,
          },
        ];
      }
    } catch (err) {
      step1Error = err instanceof Error ? err.message : String(err);
    }
  }

  async function handleAddManualPath() {
    const clean = manualPathInput.trim();
    if (!clean) return;
    step1Error = '';
    if (libraries.some((l) => l.path.trim() === clean)) {
      step1Error = 'That folder is already selected.';
      return;
    }
    const inspect = await api.inspectSetupDir(clean).catch(() => null);
    libraries = [
      ...libraries,
      {
        uid: nextUid(),
        id: '',
        name: suggestName(clean, libraries),
        path: inspect?.ok && inspect.info.path ? inspect.info.path : clean,
        modelCount: inspect?.ok ? inspect.info.model_count : undefined,
      },
    ];
    manualPathInput = '';
  }

  function removeLibrary(uid: string) {
    libraries = libraries.filter((l) => l.uid !== uid);
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
    await runProbe(selectedPythonPath);
  }

  async function runProbe(explicitPython = '') {
    probing = true;
    probeError = '';
    try {
      const resp = await api.probeSetup(effectiveLibraryRoots(), explicitPython);
      probe = resp;
      if (resp.recommended) {
        selectedPythonPath = resp.recommended.path;
      } else if (resp.candidates.length > 0) {
        selectedPythonPath = resp.candidates[0].path;
      }
    } catch (err) {
      probeError = err instanceof Error ? err.message : String(err);
    } finally {
      probing = false;
    }
  }

  async function handleBrowsePython() {
    try {
      const resp = await api.browsePython(selectedPythonPath || effectiveLibraryRoots()[0] || '');
      if (!resp.cancelled && resp.path) {
        selectedPythonPath = resp.path;
        await runProbe(resp.path);
      }
    } catch (err) {
      probeError = err instanceof Error ? err.message : String(err);
    }
  }

  async function handleInstallEnv() {
    installError = '';
    completeError = '';
    installing = true;
    showInstallLogs = true;
    studio.clearSetupStream();

    const basePython =
      selectedCandidate && !selectedCandidate.error && selectedCandidate.version_ok
        ? selectedCandidate.path
        : '';

    try {
      const resp = await api.installSetupEnv({
        library_roots: effectiveLibraryRoots(),
        base_python: basePython,
        use_uv: Boolean(probe?.has_uv),
      });
      if (!resp.ok) {
        if (!resp.cancelled) {
          installError = resp.error || 'Failed to set up Python environment.';
        }
        return;
      }
      if (resp.python_path) {
        selectedPythonPath = resp.python_path;
      }
      await runProbe(resp.python_path || '');
    } catch (err) {
      installError = err instanceof Error ? err.message : String(err);
    } finally {
      installing = false;
    }
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
        python_path: selectedCandidate?.path ?? selectedPythonPath.trim(),
      });
      await studio.finishOnboarding(resp);
    } catch (err) {
      completeError = err instanceof Error ? err.message : String(err);
    } finally {
      completing = false;
    }
  }

  function handleSkip() {
    if (busy) {
      busyNotice = 'Please wait for the active setup step to finish or click Cancel first.';
      return;
    }
    studio.clearSetupStream();
    studio.onboardingOpen = false;
  }

  function handleDialogCancel(e: Event) {
    if (busy) {
      e.preventDefault();
      busyNotice = 'Please cancel or wait for the current operation before closing.';
      return;
    }
    e.preventDefault();
    handleSkip();
  }

  function handleDialogClose() {
    if (dialogEl) {
      dialogEl.returnValue = '';
    }
    if (inspectTimer !== null) {
      clearTimeout(inspectTimer);
      inspectTimer = null;
    }
    if (studio.onboardingOpen) {
      studio.onboardingOpen = false;
    }
  }
</script>

<dialog
  bind:this={dialogEl}
  class="studio-dialog"
  aria-labelledby="onboarding-modal-title"
  oncancel={handleDialogCancel}
  onclose={handleDialogClose}
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
          Choose your build123d model libraries and configure the shared Python CAD environment.
        </p>
      </div>
      <button
        type="button"
        class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
        title="Close"
        aria-label="Close setup wizard"
        disabled={busy}
        onclick={handleSkip}
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
        <span class="font-semibold">Shared Python & build123d</span>
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
            <span>Detecting local model repositories…</span>
          </div>
        {:else}
          <!-- Unadded detected local projects -->
          {#if unaddedDetectedProjects.length > 0}
            <div class="space-y-1.5">
              <div class="text-[11px] font-semibold opacity-75">
                Detected local model folder{unaddedDetectedProjects.length > 1 ? 's' : ''}
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

          <!-- Starter Library Option (can be added alongside existing folders!) -->
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
                  Download the official starter models from GitHub into their own dedicated library folder (no git required, never modifies your own repos).
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
                  {:else if starterDownloadedPath && starterDownloadedPath === starterDir.trim()}
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
          <div class="space-y-2">
            <div class="flex items-center justify-between gap-2">
              <span class="text-xs font-semibold">
                Your Local Libraries ({libraries.length})
              </span>
              {#if studio.hasDesktop}
                <button
                  type="button"
                  class="btn btn-xs btn-neutral"
                  disabled={busy}
                  onclick={handleAddFolder}
                >
                  + Add Folder…
                </button>
              {/if}
            </div>

            {#if libraries.length > 0}
              <div class="space-y-1.5">
                {#each libraries as lib (lib.uid)}
                  <div class="rounded-field border border-base-300 bg-base-200/40 px-2.5 py-2 flex items-center gap-2">
                    <input
                      type="text"
                      bind:value={lib.name}
                      disabled={busy}
                      aria-label="Library name"
                      class="input input-bordered input-xs w-36 font-medium text-xs shrink-0"
                    />
                    <span class="font-mono text-[11px] opacity-75 truncate flex-1" title={lib.path}>
                      {lib.path}
                    </span>
                    {#if lib.modelCount !== undefined}
                      <span class="badge badge-xs badge-ghost shrink-0">
                        {lib.modelCount}
                      </span>
                    {/if}
                    <button
                      type="button"
                      class="btn btn-ghost btn-xs btn-square hover:text-error shrink-0"
                      title="Remove library"
                      aria-label="Remove library"
                      disabled={busy}
                      onclick={() => removeLibrary(lib.uid)}
                    >
                      <svg xmlns="http://www.w3.org/2000/svg" class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <line x1="6" y1="6" x2="18" y2="18" />
                        <line x1="18" y1="6" x2="6" y2="18" />
                      </svg>
                    </button>
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
                  placeholder="Enter folder location (e.g. ~/repos/my-cad-models)..."
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
            <span class="text-[11px] opacity-60 mt-1">
              Leave blank to default each export to its library's <code>exports/</code> folder.
            </span>
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
        <button
          type="button"
          class="btn btn-sm btn-ghost"
          disabled={busy}
          onclick={handleSkip}
        >
          Skip for now
        </button>
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
      <!-- STEP 2: Shared Python & build123d Environment -->
      <div class="space-y-4">
        <div class="rounded-box bg-base-200 border border-base-300 p-4 space-y-3">
          <div class="flex items-center justify-between gap-2">
            <span class="text-xs font-semibold uppercase tracking-wider opacity-75">
              Shared Python Environment Check
            </span>
            <div class="flex items-center gap-1.5">
              <button
                type="button"
                class="btn btn-xs btn-ghost"
                disabled={probing || installing}
                onclick={() => void runProbe(selectedPythonPath)}
              >
                {probing ? 'Checking…' : 'Re-check'}
              </button>
              {#if studio.hasDesktop}
                <button
                  type="button"
                  class="btn btn-xs btn-neutral"
                  title="Choose a specific Python executable file on your computer"
                  disabled={probing || installing}
                  onclick={handleBrowsePython}
                >
                  Select Python…
                </button>
              {/if}
            </div>
          </div>

          {#if probing && !probe}
            <div class="flex items-center gap-2 text-xs opacity-75 py-2">
              <span class="loading loading-spinner loading-xs"></span>
              <span>Scanning for Python and build123d…</span>
            </div>
          {:else}
            <!-- Check 1: Python interpreter -->
            {@const pythonOk = Boolean(selectedCandidate && !selectedCandidate.error && selectedCandidate.version_ok)}
            <div class="flex items-start gap-2.5 text-xs">
              <span
                class="w-4 h-4 rounded-full flex items-center justify-center shrink-0 mt-0.5"
                class:bg-success={pythonOk}
                class:text-success-content={pythonOk}
                class:bg-error={!pythonOk}
                class:text-error-content={!pythonOk}
                aria-hidden="true"
              >
                {#if pythonOk}
                  <svg xmlns="http://www.w3.org/2000/svg" class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                {:else}
                  <svg xmlns="http://www.w3.org/2000/svg" class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.2">
                    <line x1="6" y1="6" x2="18" y2="18" />
                    <line x1="18" y1="6" x2="6" y2="18" />
                  </svg>
                {/if}
              </span>
              <div class="min-w-0 flex-1 space-y-0.5">
                <div class="flex flex-wrap items-center gap-2">
                  {#if selectedCandidate && !selectedCandidate.error}
                    <span class="font-semibold">Python {selectedCandidate.version}</span>
                    <span class="badge badge-xs badge-outline">
                      {sourceLabel(selectedCandidate)}
                    </span>
                  {:else}
                    <span class="font-semibold">Python</span>
                    <span class="text-error">Not found (3.10+ required)</span>
                  {/if}
                </div>
                {#if selectedCandidate && !selectedCandidate.error}
                  <div class="font-mono text-[11px] opacity-70 break-all">
                    {selectedCandidate.path}
                  </div>
                  {#if !selectedCandidate.version_ok}
                    <div class="text-error text-[11px]">
                      Python 3.10 or newer is required by build123d.
                    </div>
                  {/if}
                {:else if selectedCandidate?.error}
                  <div class="text-error text-[11px] font-mono break-all">
                    {selectedCandidate.error}
                  </div>
                {/if}
              </div>
            </div>

            <!-- Check 2: build123d -->
            <div class="flex items-start gap-2.5 text-xs">
              <span
                class="w-4 h-4 rounded-full flex items-center justify-center shrink-0 mt-0.5"
                class:bg-success={envReady}
                class:text-success-content={envReady}
                class:bg-warning={!envReady && pythonOk}
                class:text-warning-content={!envReady && pythonOk}
                class:bg-base-300={!envReady && !pythonOk}
                class:text-base-content={!envReady && !pythonOk}
                aria-hidden="true"
              >
                {#if envReady}
                  <svg xmlns="http://www.w3.org/2000/svg" class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                {:else if pythonOk}
                  <span class="text-[10px] font-bold leading-none">!</span>
                {:else}
                  <span class="w-1.5 h-1.5 rounded-full bg-current opacity-50"></span>
                {/if}
              </span>
              <div class="min-w-0 flex-1 space-y-0.5">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-semibold">build123d CAD Engine</span>
                  {#if envReady && selectedCandidate}
                    <span class="badge badge-xs badge-success badge-soft font-mono">
                      {selectedCandidate.build123d_version
                        ? `v${selectedCandidate.build123d_version}`
                        : 'Installed'}
                    </span>
                  {:else if pythonOk}
                    <span class="text-warning">Not installed in this Python environment</span>
                  {:else}
                    <span class="opacity-60">Waiting for Python 3.10+</span>
                  {/if}
                </div>
                {#if envReady}
                  <div class="text-[11px] opacity-75">
                    Ready — this single Python environment will be shared across all of your libraries.
                  </div>
                {:else if pythonOk}
                  <div class="text-[11px] opacity-75">
                    Studio can create a shared isolated virtual environment and install
                    <code>build123d</code> + <code>ocp_tessellate</code> automatically.
                  </div>
                {/if}
              </div>
            </div>

            <!-- Check 3: Fast 3D Preview (ocp_tessellate — optional) -->
            {@const hasTessellate = Boolean(selectedCandidate && !selectedCandidate.error && selectedCandidate.has_ocp_tessellate)}
            <div class="flex items-start gap-2.5 text-xs">
              <span
                class="w-4 h-4 rounded-full flex items-center justify-center shrink-0 mt-0.5"
                class:bg-success={hasTessellate}
                class:text-success-content={hasTessellate}
                class:bg-base-300={!hasTessellate}
                class:text-base-content={!hasTessellate}
                aria-hidden="true"
              >
                {#if hasTessellate}
                  <svg xmlns="http://www.w3.org/2000/svg" class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                {:else}
                  <span class="w-1.5 h-1.5 rounded-full bg-current opacity-50"></span>
                {/if}
              </span>
              <div class="min-w-0 flex-1 space-y-0.5">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-semibold">Fast 3D Tessellation</span>
                  <code class="text-[11px] opacity-75">ocp_tessellate</code>
                  <span class="badge badge-xs badge-ghost">Optional</span>
                </div>
                {#if hasTessellate}
                  <div class="text-[11px] opacity-75">
                    Installed — speeds up 3D viewport mesh generation using NumPy.
                  </div>
                {:else if envReady}
                  <div class="text-[11px] opacity-75">
                    Not installed — Studio will use the built-in OpenCASCADE fallback. You can optionally run
                    <code>pip install ocp_tessellate</code> later for faster previews.
                  </div>
                {:else if pythonOk}
                  <div class="text-[11px] opacity-75">
                    Included automatically when Studio installs <code>build123d</code>.
                  </div>
                {:else}
                  <div class="text-[11px] opacity-60">
                    Optional speedup for 3D viewport rendering.
                  </div>
                {/if}
              </div>
            </div>

            <!-- Interpreter switcher if multiple candidates discovered -->
            {#if probe && probe.candidates.length > 1}
              <div class="pt-2 border-t border-base-300/70 form-control">
                <label class="label py-0.5" for="onboarding-python-select">
                  <span class="label-text text-[11px] opacity-75">Detected Python environments</span>
                </label>
                <select
                  id="onboarding-python-select"
                  class="select select-bordered select-xs w-full font-mono text-[11px]"
                  disabled={installing}
                  bind:value={selectedPythonPath}
                >
                  {#each probe.candidates as cand (cand.path)}
                    <option value={cand.path}>
                      {sourceLabel(cand)} — Python {cand.version || '?'}
                      {cand.has_build123d ? ' ✓ build123d' : ''} ({cand.path})
                    </option>
                  {/each}
                </select>
              </div>
            {/if}
          {/if}
        </div>

        <!-- Ready Banner -->
        {#if envReady && !installing}
          <Alert variant="success">
            Everything is ready! Click <strong>Finish &amp; Launch Studio</strong> to load your libraries.
          </Alert>
        {/if}

        <!-- One-click Shared Studio Venv Installer -->
        {#if !envReady && canInstallEnv}
          <div class="rounded-box border border-primary/40 bg-base-200/50 p-3.5 space-y-2.5">
            <div class="flex items-start justify-between gap-3">
              <div class="space-y-0.5 text-xs">
                <div class="font-semibold">Automatic Shared Python Setup</div>
                <p class="text-[11px] opacity-75">
                  Creates <code class="font-mono break-all">{probe?.studio_venv_dir || defaults?.studio_venv_dir || 'Studio venv'}</code>
                  {#if selectedCandidate?.version}
                    using <strong>Python {selectedCandidate.version}</strong>
                  {/if}
                  and installs <code>build123d</code> &amp; <code>ocp_tessellate</code>
                  {#if probe?.has_uv}
                    via <code>uv</code> (fast).
                  {:else}
                    via <code>venv</code> + <code>pip</code>.
                  {/if}
                  {#if (probe?.requirements_files?.length ?? 0) > 0}
                    Also installs dependencies from {probe?.requirements_files.length} <code>requirements.txt</code> file{(probe?.requirements_files.length ?? 0) === 1 ? '' : 's'}.
                  {/if}
                </p>
              </div>
              {#if installing}
                <button
                  type="button"
                  class="btn btn-xs btn-ghost text-error shrink-0"
                  onclick={handleCancelActiveTask}
                >
                  Cancel
                </button>
              {:else}
                <button
                  type="button"
                  class="btn btn-sm btn-primary shrink-0"
                  onclick={handleInstallEnv}
                >
                  Install build123d
                </button>
              {/if}
            </div>

            {#if installing}
              <div class="space-y-1.5">
                <div class="flex items-center gap-2 text-xs">
                  <span class="loading loading-spinner loading-xs"></span>
                  <span class="truncate">
                    {studio.setupProgress?.message || 'Setting up Python environment…'}
                  </span>
                </div>
                <progress class="progress progress-primary w-full"></progress>
              </div>
            {/if}

            {#if studio.setupLogs.length > 0}
              <div class="space-y-1">
                <button
                  type="button"
                  class="link text-[11px] opacity-75"
                  onclick={() => (showInstallLogs = !showInstallLogs)}
                >
                  {showInstallLogs ? 'Hide installer logs' : `Show installer logs (${studio.setupLogs.length} lines)`}
                </button>
                {#if showInstallLogs}
                  <pre
                    bind:this={logContainerEl}
                    class="bg-base-300/70 rounded p-2 text-[10px] font-mono max-h-36 overflow-y-auto whitespace-pre-wrap break-all leading-relaxed"
                  >{studio.setupLogs.join('\n')}</pre>
                {/if}
              </div>
            {/if}
          </div>
        {:else if !probing && !envReady && selectedCandidate?.version_ok && !selectedCandidate.has_venv && !probe?.has_uv}
          <div class="rounded-box border border-warning/40 bg-base-200/50 p-3.5 space-y-2 text-xs">
            <div class="font-semibold">Python <code>venv</code> module is missing</div>
            <p class="text-[11px] opacity-80 leading-relaxed">
              Python {selectedCandidate.version} was found, but its built-in <code>venv</code> module is not installed.
              Install it (for example <code>sudo apt install python3-venv</code>) or install <code>uv</code>, then click
              <strong>Re-check</strong> above.
            </p>
          </div>
        {:else if !probing && !envReady && (!selectedCandidate || !selectedCandidate.version_ok)}
          <div class="rounded-box border border-warning/40 bg-base-200/50 p-3.5 space-y-2 text-xs">
            <div class="font-semibold">Python 3.10+ is required</div>
            <p class="text-[11px] opacity-80 leading-relaxed">
              {#if (probe?.os ?? defaults?.os) === 'windows'}
                Download and install <strong>Python 3.12</strong> from <code>python.org/downloads</code>
                (make sure to check <em>"Add python.exe to PATH"</em>), then click <strong>Re-check</strong> above.
              {:else if (probe?.os ?? defaults?.os) === 'darwin'}
                Install Python 3.12 via <code>brew install python@3.12</code> or from <code>python.org/downloads</code>,
                then click <strong>Re-check</strong> above.
              {:else}
                Install Python 3.10+ and its <code>venv</code> module via your system package manager
                (for example <code>sudo apt install python3 python3-venv</code> or <code>sudo dnf install python3</code>),
                then click <strong>Re-check</strong> above.
              {/if}
            </p>
          </div>
        {/if}

        {#if probeError}
          <Alert variant="error">{probeError}</Alert>
        {/if}

        {#if installError}
          <Alert variant="error">{installError}</Alert>
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
              installError = '';
              completeError = '';
            }}
          >
            Back
          </button>
          <button
            type="button"
            class="btn btn-sm btn-ghost opacity-75"
            disabled={busy}
            onclick={handleSkip}
          >
            Skip for now
          </button>
        </div>

        <button
          type="button"
          class="btn btn-sm btn-primary"
          disabled={busy || !envReady}
          onclick={handleFinish}
        >
          {#if completing}
            <span class="loading loading-spinner loading-xs"></span>
            Launching…
          {:else}
            Finish &amp; Launch Studio
          {/if}
        </button>
      </div>
    {/if}
  </div>
</dialog>
