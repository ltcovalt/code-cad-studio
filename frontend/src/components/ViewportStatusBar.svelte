<script lang="ts">
  import { pathBasename, revealActionLabel } from '../lib/paths';
  import { studio } from '../lib/studioState.svelte';
  import Alert from './Alert.svelte';

  let elapsedMs = $state(0);
  let errorDrawerOpen = $state(false);
  let copied = $state(false);
  let lastErrorKey = '';

  let isBusy = $derived(studio.rendering || studio.exporting || studio.modelLoading);
  let hasModelError = $derived(
    studio.activeModel?.kind === 'error' || Boolean(studio.modelLoadError)
  );
  let exportDisabled = $derived(
    !studio.activeModel || isBusy || hasModelError || studio.status.kind === 'error'
  );
  let revealLabel = $derived(revealActionLabel(studio.os));

  let activeFilename = $derived.by(() => {
    const m = studio.activeModel;
    if (!m) return '';
    return pathBasename(m.rel_path || m.py_path || m.name);
  });

  let activeFileTitle = $derived.by(() => {
    const m = studio.activeModel;
    if (!m) return '';
    const headParts: string[] = [];
    if (m.library_name) headParts.push(m.library_name);
    if (m.rel_path) headParts.push(m.rel_path);
    const head = headParts.join(' · ');
    if (m.py_path && m.py_path !== m.rel_path) {
      return head ? `${head}\n${m.py_path}` : m.py_path;
    }
    return head || m.name;
  });

  let errorSummary = $derived.by(() => {
    if (studio.status.kind !== 'error') return '';
    const firstLine = studio.status.error.split(/\r?\n/)[0]?.trim();
    return firstLine || studio.status.error;
  });

  $effect(() => {
    if (!studio.rendering) {
      elapsedMs = 0;
      return;
    }
    const start = studio.buildStartedAt ?? Date.now();
    elapsedMs = Math.max(0, Date.now() - start);
    const timer = setInterval(() => {
      elapsedMs = Math.max(0, Date.now() - start);
    }, 100);
    return () => clearInterval(timer);
  });

  $effect(() => {
    if (studio.status.kind === 'error') {
      const key = `${studio.status.error}\n${studio.status.traceback ?? ''}`;
      if (key !== lastErrorKey) {
        lastErrorKey = key;
        errorDrawerOpen = true;
        copied = false;
      }
    } else {
      lastErrorKey = '';
      errorDrawerOpen = false;
    }
  });

  async function copyError() {
    if (studio.status.kind !== 'error') return;
    const text = studio.status.traceback
      ? `${studio.status.error}\n\n${studio.status.traceback}`
      : studio.status.error;
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const ta = document.createElement('textarea');
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      ta.remove();
    }
    copied = true;
    setTimeout(() => {
      copied = false;
    }, 1500);
  }
</script>

<div
  class="border-t border-base-300 bg-base-200/60 px-3 py-1 min-h-9 flex items-center justify-between gap-3 shrink-0 text-xs relative"
>
  {#if studio.status.kind === 'error' && errorDrawerOpen}
    <div
      class="absolute bottom-full left-2 right-2 mb-2 z-20 bg-base-100 rounded-box shadow-lg border border-error/30 max-h-72 overflow-y-auto"
    >
      <Alert
        variant="error"
        title="Build Error"
        class="select-text border-0 rounded-box"
        onDismiss={() => (errorDrawerOpen = false)}
      >
        {#snippet action()}
          <button
            type="button"
            class="btn btn-ghost btn-xs text-[10px] h-5 min-h-0 px-1.5"
            onclick={copyError}
          >
            {copied ? 'Copied!' : 'Copy'}
          </button>
        {/snippet}
        <p class="font-mono break-words whitespace-pre-wrap select-text">{studio.status.error}</p>
        {#if studio.status.traceback}
          <details class="w-full pt-1">
            <summary class="cursor-pointer opacity-75 text-[10px] select-none">
              Show Python traceback
            </summary>
            <pre
              class="text-[10px] mt-1 p-2 bg-base-300/60 rounded overflow-x-auto max-h-48 font-mono whitespace-pre-wrap select-text"
            >{studio.status.traceback}</pre>
          </details>
        {/if}
      </Alert>
    </div>
  {/if}

  <!-- Left cluster: active file & quick actions -->
  <div class="min-w-0 flex items-center gap-1.5">
    {#if studio.activeModel}
      <span
        class="font-mono text-[11px] opacity-80 truncate max-w-[14rem]"
        title={activeFileTitle}
      >
        {activeFilename}
      </span>
      {#if studio.hasDesktop}
        <button
          type="button"
          class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-0 opacity-75 hover:opacity-100 shrink-0"
          title="Open in Editor"
          aria-label="Open model in editor"
          onclick={() => void studio.openActiveModelInEditor()}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-3.5 w-3.5"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M12 20h9" />
            <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
          </svg>
        </button>
        <button
          type="button"
          class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-0 opacity-75 hover:opacity-100 shrink-0"
          title={revealLabel}
          aria-label={revealLabel}
          onclick={() => void studio.revealActiveModel()}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-3.5 w-3.5"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
          </svg>
        </button>
      {/if}
      {#if !hasModelError && !isBusy}
        <button
          type="button"
          class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-0 opacity-75 hover:opacity-100 shrink-0"
          title="Rebuild (Ctrl+R)"
          aria-label="Rebuild model"
          onclick={() => void studio.triggerRender(false, true)}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-3.5 w-3.5"
            viewBox="0 0 24 24"
            fill="none"
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
      {/if}
    {:else}
      <span class="text-[11px] opacity-50">No model selected</span>
    {/if}
  </div>

  <!-- Center cluster: build progress, error summary, export confirmation, or dimensions & timing -->
  <div class="flex items-center justify-center gap-2 min-w-0 flex-1">
    {#if studio.rendering}
      <span class="loading loading-spinner loading-xs text-primary shrink-0"></span>
      <span class="font-mono text-[11px] opacity-85 truncate">
        Building… ({(elapsedMs / 1000).toFixed(1)}s)
      </span>
      <button
        type="button"
        class="btn btn-xs btn-ghost text-error h-6 min-h-0 px-1.5 shrink-0"
        onclick={() => void studio.cancelActiveBuild()}
      >
        Cancel
      </button>
    {:else if studio.status.kind === 'error'}
      <button
        type="button"
        class="btn btn-xs btn-ghost text-error h-6 min-h-0 px-2 gap-1.5 max-w-full min-w-0 font-normal"
        aria-expanded={errorDrawerOpen}
        title={studio.status.error}
        onclick={() => (errorDrawerOpen = !errorDrawerOpen)}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-3.5 w-3.5 shrink-0"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <circle cx="12" cy="12" r="10" />
          <line x1="12" x2="12" y1="8" y2="12" />
          <line x1="12" x2="12.01" y1="16" y2="16" />
        </svg>
        <span class="truncate font-mono text-[11px]">{errorSummary}</span>
        <span class="inline-flex items-center gap-0.5 font-sans font-medium text-[11px] shrink-0">
          Details
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-3 w-3 transition-transform"
            class:rotate-180={errorDrawerOpen}
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="m18 15-6-6-6 6" />
          </svg>
        </span>
      </button>
    {:else if studio.status.kind === 'message'}
      <span
        class="text-success font-medium text-[11px] truncate select-text"
        title={studio.status.message}
      >
        {studio.status.message}
      </span>
      {#if studio.lastExportPath && studio.hasDesktop}
        <button
          type="button"
          class="btn btn-xs btn-ghost border border-base-300 h-6 min-h-0 px-1.5 shrink-0"
          title={revealLabel}
          onclick={() => void studio.revealLastExport()}
        >
          Reveal
        </button>
        <button
          type="button"
          class="btn btn-xs btn-ghost border border-base-300 h-6 min-h-0 px-1.5 shrink-0"
          onclick={() => void studio.openLastExportInSlicer()}
        >
          Open in Slicer
        </button>
      {/if}
    {:else if studio.status.kind === 'ok'}
      <div class="flex items-center gap-2 text-[11px] font-mono opacity-80 min-w-0 truncate select-text">
        {#if studio.status.bbox}
          <span class="truncate" title="Bounding box (X × Y × Z mm)">
            {studio.status.bbox.dx.toFixed(2)} × {studio.status.bbox.dy.toFixed(2)} × {studio.status.bbox.dz.toFixed(2)} mm
          </span>
          <span class="opacity-40 shrink-0" aria-hidden="true">·</span>
        {/if}
        <span class="shrink-0" title="Build + Tessellate time">
          {#if studio.status.cached}
            cached
          {:else}
            {Math.round(studio.status.buildTimeMs)}ms + {Math.round(studio.status.tessellateTimeMs)}ms
          {/if}
        </span>
      </div>
    {/if}
  </div>

  <!-- Right cluster: primary Export modal trigger -->
  <div class="shrink-0 flex items-center">
    <button
      type="button"
      class="btn btn-xs btn-primary h-6 min-h-0 px-2.5 gap-1"
      disabled={exportDisabled}
      title="Export model (Ctrl+E)"
      onclick={() => (studio.exportModalOpen = true)}
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        class="h-3.5 w-3.5"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
        <polyline points="7 10 12 15 17 10" />
        <line x1="12" x2="12" y1="15" y2="3" />
      </svg>
      <span>Export…</span>
    </button>
  </div>
</div>
