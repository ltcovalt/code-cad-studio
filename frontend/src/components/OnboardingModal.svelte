<script lang="ts">
  import * as api from '../lib/api';
  import { studio } from '../lib/studioState.svelte';
  import type {
    DetectedProject,
    ProjectDirInfo,
    PythonCandidate,
    SetupDefaultsResponse,
    SetupProbeResponse,
  } from '../lib/types';
  import Alert from './Alert.svelte';

  type Step = 1 | 2;
  type ProjectMode = 'download' | 'existing';

  let dialogEl: HTMLDialogElement | undefined = $state();
  let dialogBodyEl: HTMLDivElement | undefined = $state();

  let step = $state<Step>(1);
  let mode = $state<ProjectMode>('download');
  let loadingDefaults = $state(false);
  let defaults = $state<SetupDefaultsResponse | null>(null);

  let projectDir = $state('');
  let exportDir = $state('');
  let exportDirTouched = $state(false);
  let dirInfo = $state<ProjectDirInfo | null>(null);
  let inspectingDir = $state(false);

  let downloading = $state(false);
  let downloadCompleted = $state(false);
  let allowOverwriteExisting = $state(false);
  let downloadError = $state('');

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

  let selectedCandidate = $derived.by<PythonCandidate | null>(() => {
    if (!probe || probe.candidates.length === 0) return null;
    if (selectedPythonPath) {
      const exact = probe.candidates.find((c) => c.path === selectedPythonPath);
      if (exact) return exact;
    }
    return probe.recommended ?? probe.candidates[0] ?? null;
  });

  let envReady = $derived(
    Boolean(selectedCandidate && !selectedCandidate.error && selectedCandidate.version_ok && selectedCandidate.has_build123d)
  );

  let canInstallEnv = $derived(
    Boolean(
      !envReady &&
        projectDir.trim() &&
        ((selectedCandidate && !selectedCandidate.error && selectedCandidate.version_ok && (selectedCandidate.has_venv || probe?.has_uv)) ||
          probe?.has_uv)
    )
  );

  let downloadProgressPct = $derived.by<number | null>(() => {
    const p = studio.setupProgress;
    if (!p || p.task !== 'download') return null;
    if (p.stage === 'downloading' && p.total_bytes && p.total_bytes > 0 && p.bytes_downloaded !== undefined) {
      return Math.min(100, Math.round((p.bytes_downloaded / p.total_bytes) * 100));
    }
    if (p.stage === 'extracting' && p.total_files && p.total_files > 0 && p.files_extracted !== undefined) {
      return Math.min(100, Math.round((p.files_extracted / p.total_files) * 100));
    }
    return null;
  });

  function joinExportDefault(proj: string): string {
    const trimmed = proj.trim().replace(/[\\/]+$/, '');
    if (!trimmed) return '';
    const sep = defaults?.os === 'windows' || trimmed.includes('\\') ? '\\' : '/';
    return `${trimmed}${sep}exports`;
  }

  function formatBytes(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  function sourceLabel(source: string): string {
    switch (source) {
      case 'project_venv':
        return 'Project .venv';
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
        return source || 'Python';
    }
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
    step = 1;
    downloadError = '';
    downloadCompleted = false;
    allowOverwriteExisting = false;
    probeError = '';
    installError = '';
    completeError = '';
    busyNotice = '';
    probe = null;
    dirInfo = null;
    studio.clearSetupStream();

    loadingDefaults = true;
    try {
      const def = await api.getSetupDefaults();
      defaults = def;

      if (studio.config.project_root) {
        projectDir = studio.config.project_root;
        exportDir = studio.config.export_dir || joinExportDefault(projectDir);
        exportDirTouched = Boolean(studio.config.export_dir);
        mode = 'existing';
      } else if (def.detected_projects.length > 0) {
        projectDir = def.suggested_project_dir;
        exportDir = def.suggested_export_dir;
        exportDirTouched = false;
        mode = 'existing';
      } else {
        projectDir = def.suggested_project_dir;
        exportDir = def.suggested_export_dir;
        exportDirTouched = false;
        mode = 'download';
      }
      selectedPythonPath = studio.config.python_path || '';
      await runInspectDir(projectDir);
    } catch (err) {
      downloadError = err instanceof Error ? err.message : String(err);
    } finally {
      loadingDefaults = false;
    }
  }

  function scheduleInspectDir(path: string) {
    if (inspectTimer !== null) {
      clearTimeout(inspectTimer);
      inspectTimer = null;
    }
    inspectTimer = setTimeout(() => {
      void runInspectDir(path);
    }, 180);
  }

  async function runInspectDir(path: string) {
    const trimmed = path.trim();
    if (!trimmed) {
      dirInfo = null;
      return;
    }
    inspectingDir = true;
    try {
      const resp = await api.inspectSetupDir(trimmed);
      if (resp.ok) {
        dirInfo = resp.info;
      }
    } catch {
      dirInfo = null;
    } finally {
      inspectingDir = false;
    }
  }

  function handleProjectDirInput(value: string) {
    projectDir = value;
    downloadError = '';
    downloadCompleted = false;
    allowOverwriteExisting = false;
    if (!exportDirTouched) {
      exportDir = joinExportDefault(value);
    }
    scheduleInspectDir(value);
  }

  function switchProjectMode(nextMode: ProjectMode) {
    if (mode === nextMode) return;
    mode = nextMode;
    downloadError = '';
    downloadCompleted = false;
    allowOverwriteExisting = false;

    if (nextMode === 'download' && defaults?.default_download_dir) {
      const onDetected = detectedProjects.some((p) => p.path === projectDir);
      if (onDetected || (studio.config.project_root && projectDir === studio.config.project_root)) {
        projectDir = defaults.default_download_dir;
        if (!exportDirTouched) {
          exportDir = joinExportDefault(projectDir);
        }
        void runInspectDir(projectDir);
      }
    } else if (nextMode === 'existing' && defaults) {
      if (projectDir === defaults.default_download_dir) {
        const existingTarget = studio.config.project_root || detectedProjects[0]?.path || '';
        if (existingTarget) {
          projectDir = existingTarget;
          if (!exportDirTouched) {
            exportDir = joinExportDefault(projectDir);
          }
          void runInspectDir(projectDir);
        }
      }
    }
  }

  function handleExportDirInput(value: string) {
    exportDir = value;
    exportDirTouched = value.trim() !== '' && value.trim() !== joinExportDefault(projectDir);
  }

  function resetExportDir() {
    exportDirTouched = false;
    exportDir = joinExportDefault(projectDir);
  }

  function selectDetectedProject(proj: DetectedProject) {
    mode = 'existing';
    projectDir = proj.path;
    if (!exportDirTouched) {
      exportDir = joinExportDefault(proj.path);
    }
    downloadError = '';
    void runInspectDir(proj.path);
  }

  async function handleBrowseProject() {
    try {
      const resp = await api.browseProject(projectDir);
      if (!resp.cancelled && resp.path) {
        handleProjectDirInput(resp.path);
      }
    } catch (err) {
      downloadError = err instanceof Error ? err.message : String(err);
    }
  }

  async function handleBrowseExportDir() {
    try {
      const resp = await api.browseExportDir(exportDir || projectDir);
      if (!resp.cancelled && resp.path) {
        exportDir = resp.path;
        exportDirTouched = resp.path.trim() !== joinExportDefault(projectDir);
      }
    } catch (err) {
      downloadError = err instanceof Error ? err.message : String(err);
    }
  }

  async function handleStep1Next() {
    downloadError = '';
    const trimmed = projectDir.trim();
    if (!trimmed) {
      downloadError = 'Please choose a project folder first.';
      return;
    }

    if (mode === 'download' && !downloadCompleted) {
      downloading = true;
      studio.clearSetupStream();
      try {
        const resp = await api.downloadStarterProject(
          trimmed,
          defaults?.default_archive_url ?? '',
          allowOverwriteExisting
        );
        if (!resp.ok) {
          if (!resp.cancelled) {
            downloadError = resp.error || 'Failed to download code-cad starter project.';
          }
          return;
        }
        downloadCompleted = true;
        if (resp.project_info) {
          dirInfo = resp.project_info;
          projectDir = resp.project_info.path;
          if (!exportDirTouched) {
            exportDir = joinExportDefault(projectDir);
          }
        }
      } catch (err) {
        downloadError = err instanceof Error ? err.message : String(err);
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
      const resp = await api.probeSetup(projectDir, explicitPython);
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
      const resp = await api.browsePython(selectedPythonPath || projectDir);
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
        project_root: projectDir,
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
        project_root: projectDir.trim(),
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

  let step1Blocked = $derived.by(() => {
    if (busy || !projectDir.trim()) return true;
    if (mode === 'download' && !downloadCompleted) {
      if (dirInfo?.exists && !dirInfo.is_dir) {
        return true;
      }
      if (dirInfo?.exists && !dirInfo.is_empty && !allowOverwriteExisting) {
        return true;
      }
    }
    return false;
  });
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
    class="bg-base-100 border border-base-300 rounded-box shadow-lg w-[38rem] max-w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto p-6 space-y-5 focus:outline-none"
  >
    <!-- Header -->
    <div class="flex items-start justify-between gap-4">
      <div class="space-y-1">
        <h2 id="onboarding-modal-title" class="font-bold text-lg leading-tight">
          Welcome to Code CAD Studio
        </h2>
        <p class="text-xs opacity-70">
          Set up your build123d project folder, default exports, and Python CAD environment.
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
        <span class="font-semibold">Workspace & Exports</span>
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
        <span class="font-semibold">Python & build123d</span>
      </li>
    </ol>

    {#if busyNotice}
      <Alert variant="warning">{busyNotice}</Alert>
    {/if}

    {#if step === 1}
      <!-- STEP 1: Project & Export Directories -->
      <div class="space-y-4">
        {#if loadingDefaults}
          <div class="flex items-center gap-2 text-xs opacity-70 py-4">
            <span class="loading loading-spinner loading-xs"></span>
            <span>Detecting default directories…</span>
          </div>
        {:else}
          <!-- Starter vs Existing choice -->
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-2.5" role="radiogroup" aria-label="Project source">
            <button
              type="button"
              role="radio"
              aria-checked={mode === 'download'}
              class="text-left rounded-box border p-3 space-y-1 cursor-pointer transition-colors focus:outline-none focus-visible:outline-2 focus-visible:outline-primary focus-visible:outline-offset-1"
              class:border-primary={mode === 'download'}
              class:bg-base-200={mode === 'download'}
              class:border-base-300={mode !== 'download'}
              disabled={busy}
              onclick={() => switchProjectMode('download')}
            >
              <div class="flex items-center justify-between gap-2">
                <span class="font-semibold text-xs">Download starter library</span>
                <span
                  aria-hidden="true"
                  class="w-4 h-4 rounded-full border-2 flex items-center justify-center shrink-0"
                  class:border-primary={mode === 'download'}
                  class:border-base-content={mode !== 'download'}
                  class:opacity-40={mode !== 'download'}
                >
                  {#if mode === 'download'}
                    <span class="w-2 h-2 rounded-full bg-primary"></span>
                  {/if}
                </span>
              </div>
              <p class="text-[11px] opacity-70 leading-snug">
                Download the default <code>code-cad</code> models straight from GitHub (no git needed).
              </p>
            </button>

            <button
              type="button"
              role="radio"
              aria-checked={mode === 'existing'}
              class="text-left rounded-box border p-3 space-y-1 cursor-pointer transition-colors focus:outline-none focus-visible:outline-2 focus-visible:outline-primary focus-visible:outline-offset-1"
              class:border-primary={mode === 'existing'}
              class:bg-base-200={mode === 'existing'}
              class:border-base-300={mode !== 'existing'}
              disabled={busy}
              onclick={() => switchProjectMode('existing')}
            >
              <div class="flex items-center justify-between gap-2">
                <span class="font-semibold text-xs">Use existing project folder</span>
                <span
                  aria-hidden="true"
                  class="w-4 h-4 rounded-full border-2 flex items-center justify-center shrink-0"
                  class:border-primary={mode === 'existing'}
                  class:border-base-content={mode !== 'existing'}
                  class:opacity-40={mode !== 'existing'}
                >
                  {#if mode === 'existing'}
                    <span class="w-2 h-2 rounded-full bg-primary"></span>
                  {/if}
                </span>
              </div>
              <p class="text-[11px] opacity-70 leading-snug">
                Open a local folder containing <code>build123d</code> models or scripts.
              </p>
            </button>
          </div>

          {#if detectedProjects.length > 0 && mode === 'existing'}
            <div class="space-y-1.5">
              <div class="text-[11px] font-semibold opacity-75">
                Detected existing project{detectedProjects.length > 1 ? 's' : ''}
              </div>
              <div class="space-y-1.5">
                {#each detectedProjects as proj (proj.path)}
                  {@const isSelected = projectDir === proj.path}
                  <button
                    type="button"
                    class="w-full text-left rounded-field border px-3 py-2 flex items-center justify-between gap-2 cursor-pointer transition-colors focus:outline-none focus-visible:outline-2 focus-visible:outline-primary focus-visible:outline-offset-1"
                    class:border-primary={isSelected}
                    class:bg-base-200={isSelected}
                    class:border-base-300={!isSelected}
                    class:bg-base-100={!isSelected}
                    disabled={busy}
                    onclick={() => selectDetectedProject(proj)}
                  >
                    <div class="flex items-center gap-2 min-w-0">
                      <span class="font-mono text-xs truncate">{proj.path}</span>
                      <span class="badge badge-xs badge-neutral shrink-0">
                        {proj.model_count} model{proj.model_count === 1 ? '' : 's'}
                      </span>
                    </div>
                    <span
                      aria-hidden="true"
                      class="w-4 h-4 rounded-full border-2 flex items-center justify-center shrink-0"
                      class:border-primary={isSelected}
                      class:border-base-content={!isSelected}
                      class:opacity-40={!isSelected}
                    >
                      {#if isSelected}
                        <span class="w-2 h-2 rounded-full bg-primary"></span>
                      {/if}
                    </span>
                  </button>
                {/each}
              </div>
            </div>
          {/if}

          <!-- Project Directory -->
          <div class="form-control">
            <label class="label py-1" for="onboarding-project-dir">
              <span class="label-text text-xs font-semibold">
                {mode === 'download' ? 'Download Destination Folder' : 'Project Root Folder'}
              </span>
            </label>
            <div class="join w-full">
              <input
                id="onboarding-project-dir"
                type="text"
                value={projectDir}
                oninput={(e) => handleProjectDirInput((e.currentTarget as HTMLInputElement).value)}
                disabled={busy}
                class="input input-bordered input-sm join-item w-full font-mono text-xs"
                placeholder="/path/to/code-cad"
              />
              {#if studio.hasDesktop}
                <button
                  type="button"
                  class="btn btn-sm btn-neutral join-item"
                  disabled={busy}
                  onclick={handleBrowseProject}
                >
                  Browse
                </button>
              {/if}
            </div>

            <!-- Live Directory Feedback -->
            <div class="mt-1.5 min-h-[1.1rem] text-[11px]">
              {#if inspectingDir}
                <span class="opacity-60">Checking folder…</span>
              {:else if mode === 'download'}
                {#if downloadCompleted}
                  <span class="text-success font-medium">
                    ✓ Starter library downloaded ({dirInfo?.model_count ?? 0} models found).
                  </span>
                {:else if dirInfo?.exists && !dirInfo.is_dir}
                  <span class="text-error">Path exists and is a file, not a folder.</span>
                {:else if dirInfo?.exists && !dirInfo.is_empty}
                  <Alert variant="warning" title="Existing files will be overwritten" class="mt-1">
                    <p>
                      {#if dirInfo.has_models}
                        This folder already contains {dirInfo.model_count} model{dirInfo.model_count === 1 ? '' : 's'}.
                      {:else}
                        This folder is not empty.
                      {/if}
                      Downloading here will overwrite matching files in <code>models/</code> and <code>utils/</code>.
                      {#if dirInfo.has_models}
                        <button
                          type="button"
                          class="link font-semibold ml-1"
                          onclick={() => switchProjectMode('existing')}
                        >
                          Use existing folder instead
                        </button>
                      {/if}
                    </p>
                    <label class="flex items-start gap-2 cursor-pointer pt-0.5 text-xs">
                      <input
                        type="checkbox"
                        class="checkbox checkbox-xs checkbox-warning mt-0.5"
                        disabled={busy}
                        bind:checked={allowOverwriteExisting}
                      />
                      <span>I understand — download and overwrite matching files in this folder</span>
                    </label>
                  </Alert>
                {:else if projectDir.trim()}
                  <span class="text-success/90">
                    ✓ Ready — Studio will download <code>code-cad</code> into this folder.
                  </span>
                {/if}
              {:else if dirInfo?.exists && dirInfo.is_dir}
                {#if dirInfo.has_models}
                  <span class="text-success font-medium">
                    ✓ Found {dirInfo.model_count} model{dirInfo.model_count === 1 ? '' : 's'}{#if dirInfo.has_venv} and a project <code>.venv</code>{/if}.
                  </span>
                {:else}
                  <span class="opacity-70">
                    Folder exists (no model scripts detected yet — you can add <code>.py</code> models anytime).
                  </span>
                {/if}
              {:else if projectDir.trim()}
                <span class="text-warning">
                  Folder does not exist yet.
                  <button
                    type="button"
                    class="link link-primary ml-1"
                    onclick={() => (mode = 'download')}
                  >
                    Download starter library here
                  </button>
                </span>
              {/if}
            </div>
          </div>

          <!-- Export Directory -->
          <div class="form-control">
            <div class="flex items-center justify-between py-1">
              <label class="label-text text-xs font-semibold" for="onboarding-export-dir">
                Default Export Folder
              </label>
              {#if exportDirTouched}
                <button
                  type="button"
                  class="link link-primary text-[11px]"
                  disabled={busy}
                  onclick={resetExportDir}
                >
                  Reset to &lt;project&gt;/exports
                </button>
              {/if}
            </div>
            <div class="join w-full">
              <input
                id="onboarding-export-dir"
                type="text"
                value={exportDir}
                oninput={(e) => handleExportDirInput((e.currentTarget as HTMLInputElement).value)}
                disabled={busy}
                class="input input-bordered input-sm join-item w-full font-mono text-xs"
                placeholder="<project_root>/exports"
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
              Initial folder opened when exporting <code>.STEP</code>, <code>.STL</code>, or <code>.3MF</code> files.
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

          {#if downloadError}
            <Alert variant="error">{downloadError}</Alert>
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
          {:else if mode === 'download' && !downloadCompleted}
            Download & Continue
          {:else}
            Continue
          {/if}
        </button>
      </div>
    {:else}
      <!-- STEP 2: Python & build123d Environment -->
      <div class="space-y-4">
        <div class="rounded-box bg-base-200 border border-base-300 p-4 space-y-3">
          <div class="flex items-center justify-between gap-2">
            <span class="text-xs font-semibold uppercase tracking-wider opacity-75">
              Environment Check
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
                      {sourceLabel(selectedCandidate.source)}
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
                    Existing environment detected — no additional installation needed.
                  </div>
                {:else if pythonOk}
                  <div class="text-[11px] opacity-75">
                    Studio can create an isolated <code>.venv</code> inside your project folder and install
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
                      {sourceLabel(cand.source)} — Python {cand.version || '?'}
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
            Everything is ready! Click <strong>Finish &amp; Launch Studio</strong> to load your models.
          </Alert>
        {/if}

        <!-- One-click Venv Installer -->
        {#if !envReady && canInstallEnv}
          <div class="rounded-box border border-primary/40 bg-base-200/50 p-3.5 space-y-2.5">
            <div class="flex items-start justify-between gap-3">
              <div class="space-y-0.5 text-xs">
                <div class="font-semibold">Automatic Python Environment Setup</div>
                <p class="text-[11px] opacity-75">
                  Creates <code class="font-mono">{projectDir}/.venv</code>
                  {#if selectedCandidate?.version}
                    using <strong>Python {selectedCandidate.version}</strong>
                  {/if}
                  and installs <code>build123d</code> &amp; <code>ocp_tessellate</code>
                  {#if probe?.has_uv}
                    via <code>uv</code> (fast).
                  {:else}
                    via <code>venv</code> + <code>pip</code>.
                  {/if}
                  {#if probe?.project_info.has_requirements}
                    Also installs <code>requirements.txt</code>.
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
          <!-- Python found, but venv module missing (common on Debian/Ubuntu) -->
          <div class="rounded-box border border-warning/40 bg-base-200/50 p-3.5 space-y-2 text-xs">
            <div class="font-semibold">Python <code>venv</code> module is missing</div>
            <p class="text-[11px] opacity-80 leading-relaxed">
              Python {selectedCandidate.version} was found, but its built-in <code>venv</code> module is not installed.
              Install it (for example <code>sudo apt install python3-venv</code>) or install <code>uv</code>, then click
              <strong>Re-check</strong> above.
            </p>
          </div>
        {:else if !probing && !envReady && (!selectedCandidate || !selectedCandidate.version_ok)}
          <!-- Missing Python Guidance -->
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
