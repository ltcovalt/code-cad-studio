<script lang="ts">
  import * as api from '../lib/api';
  import { studio } from '../lib/studioState.svelte';
  import type { HealthResponse } from '../lib/types';

  let projectRoot = $state('');
  let pythonPath = $state('');
  let resolvedPython = $state('');
  let workerRunning = $state(false);
  let workerError = $state('');
  let health = $state<HealthResponse | null>(null);
  let healthError = $state('');
  let healthLoading = $state(false);
  let saving = $state(false);
  let savedBanner = $state(false);
  let formError = $state('');

  $effect(() => {
    if (studio.settingsOpen) {
      projectRoot = studio.config.project_root;
      pythonPath = studio.config.python_path;
      resolvedPython = studio.resolvedPython;
      workerRunning = studio.workerRunning;
      workerError = studio.workerError;
      savedBanner = false;
      formError = '';
      void fetchHealth();
    }
  });

  async function fetchHealth() {
    healthLoading = true;
    health = null;
    healthError = '';
    try {
      const resp = await api.getHealth();
      workerRunning = resp.worker_running;
      workerError = resp.worker_error;
      health = resp.health ?? null;
      healthError = resp.health_error ?? '';
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

  async function handleSubmit(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    savedBanner = false;
    formError = '';
    try {
      const resp = await api.saveConfig(projectRoot, pythonPath);
      projectRoot = resp.config.project_root;
      pythonPath = resp.config.python_path;
      resolvedPython = resp.resolved_python;
      workerRunning = resp.worker_running;
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
  <dialog class="modal modal-open">
    <div class="modal-box max-w-lg space-y-4">
      <div class="flex items-center justify-between">
        <h3 class="font-bold text-base">Project &amp; Python Settings</h3>
        <button
          type="button"
          class="btn btn-sm btn-circle btn-ghost"
          aria-label="Close settings"
          onclick={closeModal}
        >
          ✕
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
          {#if healthLoading}
            <div class="flex items-center gap-2 opacity-60">
              <span class="loading loading-spinner loading-xs"></span>
              <span>Checking Python worker...</span>
            </div>
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

        <div class="modal-action">
          <button type="button" class="btn btn-sm btn-ghost" onclick={closeModal}>Close</button>
          <button type="submit" class="btn btn-sm btn-primary" disabled={saving}>
            {#if saving}
              <span class="loading loading-spinner loading-xs"></span>
            {/if}
            Save &amp; Reload
          </button>
        </div>
      </form>
    </div>
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div class="modal-backdrop" onclick={closeModal}></div>
  </dialog>
{/if}
