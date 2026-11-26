<script lang="ts">
  import { onMount } from 'svelte';
  import * as api from '../lib/api';
  import { pathsEqual } from '../lib/paths';
  import { studio } from '../lib/studioState.svelte';
  import type { PythonCandidate, SetupProbeResponse } from '../lib/types';
  import Alert from './Alert.svelte';

  interface Props {
    selectedPythonPath?: string;
    envReady?: boolean;
    installing?: boolean;
    libraryRoots: string[];
    os?: string;
    studioVenvDirFallback?: string;
    disabled?: boolean;
    autoProbe?: boolean;
    onSelectionChange?: () => void;
  }

  let {
    selectedPythonPath = $bindable<string>(''),
    envReady = $bindable<boolean>(false),
    installing = $bindable<boolean>(false),
    libraryRoots,
    os = studio.os,
    studioVenvDirFallback = '',
    disabled = false,
    autoProbe = false,
    onSelectionChange,
  }: Props = $props();

  const selectId = $props.id();

  let probing = $state(false);
  let probe = $state<SetupProbeResponse | null>(null);
  let probeError = $state('');
  let installError = $state('');
  let showInstallLogs = $state(true);
  let logContainerEl: HTMLPreElement | undefined = $state();

  let selectedCandidate = $derived.by<PythonCandidate | null>(() => {
    if (!probe || probe.candidates.length === 0) return null;
    if (selectedPythonPath) {
      const exact = probe.candidates.find((c) => pathsEqual(c.path, selectedPythonPath, os));
      if (exact) return exact;
    }
    return probe.recommended ?? probe.candidates[0] ?? null;
  });

  let managedPythonVersion = $derived(probe?.managed_python_version || '3.14');

  let isReady = $derived(
    Boolean(
      selectedCandidate &&
        !selectedCandidate.error &&
        selectedCandidate.version_ok &&
        selectedCandidate.has_build123d
    )
  );

  let canInstallEnv = $derived(!isReady);

  $effect(() => {
    envReady = isReady;
  });

  // Auto-scroll live install log box as new SSE lines arrive
  $effect(() => {
    const _len = studio.setupLogs.length;
    if (logContainerEl && showInstallLogs && _len > 0) {
      logContainerEl.scrollTop = logContainerEl.scrollHeight;
    }
  });

  onMount(() => {
    if (autoProbe) {
      void runProbe(selectedPythonPath);
    }
  });

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

  export async function runProbe(explicitPython = selectedPythonPath): Promise<void> {
    probing = true;
    probeError = '';
    try {
      const resp = await api.probeSetup(libraryRoots, explicitPython);
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
      const resp = await api.browsePython(selectedPythonPath || libraryRoots[0] || '');
      if (!resp.cancelled && resp.path) {
        selectedPythonPath = resp.path;
        onSelectionChange?.();
        await runProbe(resp.path);
      }
    } catch (err) {
      probeError = err instanceof Error ? err.message : String(err);
    }
  }

  async function handleInstallEnv() {
    installError = '';
    installing = true;
    showInstallLogs = true;
    studio.clearSetupStream();

    try {
      const resp = await api.installSetupEnv({
        library_roots: libraryRoots,
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
      onSelectionChange?.();
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
</script>

<div class="space-y-3">
  <div class="rounded-box bg-base-200 border border-base-300 p-4 space-y-3">
    <div class="flex items-center justify-between gap-2">
      <span class="text-xs font-semibold uppercase tracking-wider opacity-75">
        Python Environment
      </span>
      <button
        type="button"
        class="btn btn-ghost btn-xs btn-square"
        title="Re-check Python environment"
        aria-label="Re-check Python environment"
        disabled={disabled || probing || installing}
        onclick={() => void runProbe(selectedPythonPath)}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-3.5 w-3.5"
          class:animate-spin={probing}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
          <path d="M21 3v5h-5" />
        </svg>
      </button>
    </div>

    {#if !studio.hasDesktop}
      <div class="join w-full">
        <input
          type="text"
          bind:value={selectedPythonPath}
          disabled={disabled || probing || installing}
          oninput={() => onSelectionChange?.()}
          placeholder="/path/to/python3 (or venv/bin/python)"
          aria-label="Python executable path"
          class="input input-bordered input-xs join-item w-full font-mono text-[11px]"
          onkeydown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              onSelectionChange?.();
              void runProbe(selectedPythonPath);
            }
          }}
        />
        <button
          type="button"
          class="btn btn-xs btn-neutral join-item"
          disabled={disabled || probing || installing || !selectedPythonPath.trim()}
          onclick={() => {
            onSelectionChange?.();
            void runProbe(selectedPythonPath);
          }}
        >
          Check
        </button>
      </div>
    {/if}

    {#if (probing && !probe) || (!probe && !probeError)}
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
          class:bg-base-300={!pythonOk && !selectedCandidate?.error}
          class:text-base-content={!pythonOk && !selectedCandidate?.error}
          class:bg-error={!pythonOk && Boolean(selectedCandidate?.error)}
          class:text-error-content={!pythonOk && Boolean(selectedCandidate?.error)}
          aria-hidden="true"
        >
          {#if pythonOk}
            <svg xmlns="http://www.w3.org/2000/svg" class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
            </svg>
          {:else if selectedCandidate?.error}
            <svg xmlns="http://www.w3.org/2000/svg" class="w-2.5 h-2.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3.2">
              <line x1="6" y1="6" x2="18" y2="18" />
              <line x1="18" y1="6" x2="6" y2="18" />
            </svg>
          {:else}
            <span class="w-1.5 h-1.5 rounded-full bg-current opacity-50"></span>
          {/if}
        </span>
        <div class="min-w-0 flex-1 space-y-0.5">
          <div class="flex flex-wrap items-center gap-2">
            {#if selectedCandidate && !selectedCandidate.error}
              <span class="font-semibold">Python {selectedCandidate.version}</span>
              <span class="badge badge-xs badge-outline">
                {sourceLabel(selectedCandidate)}
              </span>
            {:else if selectedCandidate?.error}
              <span class="font-semibold">Python</span>
              <span class="text-error">Selected interpreter failed check</span>
            {:else}
              <span class="font-semibold">Python</span>
              <span class="opacity-70">Not detected — Studio will install Python {managedPythonVersion} automatically</span>
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
          class:bg-success={isReady}
          class:text-success-content={isReady}
          class:bg-warning={!isReady && pythonOk}
          class:text-warning-content={!isReady && pythonOk}
          class:bg-base-300={!isReady && !pythonOk}
          class:text-base-content={!isReady && !pythonOk}
          aria-hidden="true"
        >
          {#if isReady}
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
            {#if isReady && selectedCandidate}
              <span class="badge badge-xs badge-success badge-soft font-mono">
                {selectedCandidate.build123d_version
                  ? `v${selectedCandidate.build123d_version}`
                  : 'Installed'}
              </span>
            {:else if pythonOk}
              <span class="text-warning">Not installed in this Python environment</span>
            {:else}
              <span class="opacity-60">Will be installed automatically</span>
            {/if}
          </div>
          {#if isReady}
            <div class="text-[11px] opacity-75">
              Ready — build123d is installed and available.
            </div>
          {:else}
            <div class="text-[11px] opacity-75">
              Studio can create a managed Python {managedPythonVersion} virtual environment and install
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
          {:else if isReady}
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
          <label class="label py-0.5" for={selectId}>
            <span class="label-text text-[11px] opacity-75">Detected Python environments</span>
          </label>
          <select
            id={selectId}
            class="select select-bordered select-xs w-full font-mono text-[11px]"
            disabled={disabled || installing}
            bind:value={selectedPythonPath}
            onchange={() => onSelectionChange?.()}
          >
            {#each probe.candidates as cand (cand.path)}
              <option value={cand.path}>
                {sourceLabel(cand)} — Python {cand.version || '?'}
                {cand.has_build123d ? ' ✓ build123d' : ''} ({cand.path})
              </option>
            {/each}
          </select>
          {#if studio.hasDesktop}
            <div class="flex justify-end mt-1">
              <button
                type="button"
                class="btn btn-xs btn-ghost px-1.5 -mr-1.5 opacity-75 hover:opacity-100"
                title="Choose a specific Python executable file on your computer"
                disabled={disabled || probing || installing}
                onclick={handleBrowsePython}
              >
                Manually select Python…
              </button>
            </div>
          {/if}
        </div>
      {:else if studio.hasDesktop}
        <div class="flex justify-end -mt-1">
          <button
            type="button"
            class="btn btn-xs btn-ghost px-1.5 -mr-1.5 opacity-75 hover:opacity-100"
            title="Choose a specific Python executable file on your computer"
            disabled={disabled || probing || installing}
            onclick={handleBrowsePython}
          >
            Manually select Python…
          </button>
        </div>
      {/if}
    {/if}
  </div>

  <!-- One-click Studio Venv Installer -->
  {#if probe && !isReady && canInstallEnv}
    <div class="rounded-box border border-primary/40 bg-base-200/50 p-3.5 space-y-2.5">
      <div class="flex items-start justify-between gap-3">
        <div class="space-y-0.5 text-xs">
          <div class="font-semibold">Automatic Python Setup</div>
          <p class="text-[11px] opacity-75">
            Creates <code class="font-mono break-all">{probe?.studio_venv_dir || studioVenvDirFallback || 'Studio venv'}</code>
            with managed <strong>Python {managedPythonVersion}</strong>
            and installs <code>build123d</code> &amp; <code>ocp_tessellate</code> via <code>uv</code>{#if !probe?.has_uv}{' '}(downloads <code>uv</code> automatically){/if}.
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
            {disabled}
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
  {/if}

  {#if probeError}
    <Alert variant="error">{probeError}</Alert>
  {/if}

  {#if installError}
    <Alert variant="error">{installError}</Alert>
  {/if}
</div>
