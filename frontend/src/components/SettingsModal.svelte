<script lang="ts">
  import * as api from '../lib/api';
  import { studio } from '../lib/studioState.svelte';
  import type { HealthResponse } from '../lib/types';

  let projectRoot = $state('');
  let pythonPath = $state('');
  let exportDir = $state('');
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

  let pollTimer: ReturnType<typeof setTimeout> | null = null;

  function clearPoll() {
    if (pollTimer !== null) {
      clearTimeout(pollTimer);
      pollTimer = null;
    }
  }

  $effect(() => {
    if (studio.settingsOpen) {
      projectRoot = studio.config.project_root;
      pythonPath = studio.config.python_path;
      exportDir = studio.config.export_dir ?? '';
      resolvedPython = studio.resolvedPython;
      resolvedExportDir = studio.resolvedExportDir;
      workerRunning = studio.workerRunning;
      workerError = studio.workerError;
      savedBanner = false;
      formError = '';
      void fetchHealth();
    } else {
      clearPoll();
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

  async function handleBrowseProject() {
    try {
      const resp = await api.browseProject(projectRoot);
      if (!resp.cancelled && resp.path) {
        projectRoot = resp.path;
      }
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    }
  }

  async function handleBrowsePython() {
    try {
      const resp = await api.browsePython(pythonPath || projectRoot);
      if (!resp.cancelled && resp.path) {
        pythonPath = resp.path;
      }
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    }
  }

  async function handleBrowseExportDir() {
    try {
      const resp = await api.browseExportDir(exportDir || resolvedExportDir || projectRoot);
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
      const resp = await api.saveConfig(projectRoot, pythonPath, exportDir);
      projectRoot = resp.config.project_root;
      pythonPath = resp.config.python_path;
      exportDir = resp.config.export_dir ?? '';
      resolvedPython = resp.resolved_python;
      resolvedExportDir = resp.resolved_export_dir;
      workerRunning = resp.worker_running;
      workerStarting = Boolean(resp.worker_starting);
      workerError = resp.worker_error;
      health = resp.health ?? null;
      healthError = resp.health_error ?? '';
      savedBanner = true;
      await studio.applySavedConfig(resp);
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  function closeModal() {
    clearPoll();
    studio.settingsOpen = false;
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && studio.settingsOpen) {
      closeModal();
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

{#if studio.settingsOpen}
  <!-- Lightweight zero-transition overlay to avoid WebKitGTK/Wayland software-compositing lag over WebGL -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
    onclick={closeModal}
  >
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="settings-modal-title"
      tabindex="-1"
      class="bg-base-100 border border-base-300 rounded-box shadow-md w-full max-w-lg max-h-[90vh] overflow-y-auto p-6 space-y-4"
      onclick={(e) => e.stopPropagation()}
    >
      <div class="flex items-center justify-between">
        <h3 id="settings-modal-title" class="font-bold text-base">Workspace Settings</h3>
        <button
          type="button"
          class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
          title="Close"
          aria-label="Close settings"
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

      <form class="space-y-3" onsubmit={handleSubmit}>
        <div class="form-control">
          <label class="label py-1" for="settings-project-root">
            <span class="label-text text-xs font-semibold">Project Root Folder</span>
          </label>
          <div class="join w-full">
            <input
              id="settings-project-root"
              type="text"
              bind:value={projectRoot}
              class="input input-bordered input-sm join-item w-full font-mono text-xs"
              placeholder="/path/to/code-cad"
            />
            {#if studio.hasDesktop}
              <button
                type="button"
                class="btn btn-sm btn-neutral join-item"
                onclick={handleBrowseProject}
              >
                Browse
              </button>
            {/if}
          </div>
          <span class="text-[11px] opacity-60 mt-1">
            Folder containing <code>models/</code>, <code>components/</code>, or standalone
            <code>.py</code> scripts.
          </span>
        </div>

        <div class="form-control">
          <label class="label py-1" for="settings-export-dir">
            <span class="label-text text-xs font-semibold">Default Export Folder (optional)</span>
          </label>
          <div class="join w-full">
            <input
              id="settings-export-dir"
              type="text"
              bind:value={exportDir}
              class="input input-bordered input-sm join-item w-full font-mono text-xs"
              placeholder="Default (&lt;project_root&gt;/exports)"
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
            Initial directory opened by the Save dialog.
            {#if resolvedExportDir}
              Resolved: <code class="font-mono break-all">{resolvedExportDir}</code>
            {/if}
          </span>
        </div>

        <div class="form-control">
          <label class="label py-1" for="settings-python-path">
            <span class="label-text text-xs font-semibold">Python Interpreter (optional)</span>
          </label>
          <div class="join w-full">
            <input
              id="settings-python-path"
              type="text"
              bind:value={pythonPath}
              class="input input-bordered input-sm join-item w-full font-mono text-xs"
              placeholder="Auto-detect (.venv/bin/python or python3)"
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
            Resolved: <code class="font-mono">{resolvedPython || 'python3'}</code>
          </span>
        </div>

        <!-- Worker Health Status -->
        <div class="rounded-box bg-base-200 p-3 text-xs space-y-1">
          <div class="font-semibold">Python Worker Status</div>
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
                Point Python Interpreter to your project's <code>.venv/bin/python</code>.
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
          <div class="alert alert-error alert-soft text-xs py-2">
            <span>{formError}</span>
          </div>
        {/if}

        {#if savedBanner}
          <div class="alert alert-success alert-soft text-xs py-2">
            <span>Settings saved and worker reloaded.</span>
          </div>
        {/if}

        <div class="flex items-center justify-between gap-2 pt-2">
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            onclick={() => studio.openOnboarding()}
          >
            Run Setup Wizard…
          </button>
          <div class="flex justify-end gap-2">
            <button type="button" class="btn btn-sm btn-ghost" onclick={closeModal}>Close</button>
            <button type="submit" class="btn btn-sm btn-primary" disabled={saving}>
              {saving ? 'Saving...' : 'Save & Reload'}
            </button>
          </div>
        </div>
      </form>
    </div>
  </div>
{/if}
