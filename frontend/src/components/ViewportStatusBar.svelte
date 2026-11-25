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
  class="@container border-t border-base-300 bg-base-200/60 px-3 py-1 min-h-9 grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-2 @[520px]:gap-3 shrink-0 text-xs relative"
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
            class="inline-flex items-center justify-center rounded-field h-6 w-6 p-0 text-current opacity-75 hover:opacity-100 hover:bg-error/15 transition-colors cursor-pointer focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-current shrink-0"
            title={copied ? 'Copied!' : 'Copy error'}
            aria-label={copied ? 'Copied error to clipboard' : 'Copy error to clipboard'}
            onclick={copyError}
          >
            {#if copied}
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
                <path d="M20 6 9 17l-5-5" />
              </svg>
            {:else}
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
                <rect width="14" height="14" x="8" y="8" rx="2" ry="2" />
                <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
              </svg>
            {/if}
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

  {#if !studio.activeModel}
    <div class="col-span-3 flex items-center justify-center">
      <span class="text-[11px] opacity-50">No model selected</span>
    </div>
  {:else}
    <!-- Left cluster: active file and quick actions -->
    <div class="min-w-0 flex items-center gap-1.5">
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
    </div>

    <!-- Center cluster: viewport-centered build status, progress, or dimensions & timing -->
    <div class="min-w-0 flex items-center justify-center gap-1.5">
      {#if studio.rendering}
        <span class="loading loading-spinner loading-xs text-primary shrink-0"></span>
        <span class="font-mono text-[11px] opacity-85 whitespace-nowrap">
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
          class="btn btn-ghost btn-xs h-6 min-h-0 px-1.5 gap-1 text-error opacity-85 hover:opacity-100 hover:bg-error/15 hover:border-transparent shrink-0 font-medium"
          aria-expanded={errorDrawerOpen}
          aria-label={hasModelError ? 'Toggle model error details' : 'Toggle build error details'}
          title={`${hasModelError ? 'Model Error' : 'Build Error'}: ${errorSummary}`}
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
          <span class="hidden @[440px]:inline text-[11px]">
            {hasModelError ? 'Model Error' : 'Build Error'}
          </span>
        </button>
      {:else if studio.status.kind === 'message'}
        <span
          class="text-success font-medium text-[11px] truncate max-w-[min(18rem,32cqi)] select-text"
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
        <div
          class="hidden @[460px]:flex items-center gap-1.5 text-[11px] font-mono opacity-75 min-w-0 select-text"
        >
          {#if studio.status.bbox}
            <span class="whitespace-nowrap" title="Bounding box (X × Y × Z mm)">
              {studio.status.bbox.dx.toFixed(2)} × {studio.status.bbox.dy.toFixed(2)} × {studio.status.bbox.dz.toFixed(2)} mm
            </span>
            <span class="hidden @[600px]:inline opacity-40 shrink-0" aria-hidden="true">·</span>
          {/if}
          <span
            class={studio.status.bbox
              ? 'hidden @[600px]:inline shrink-0 whitespace-nowrap'
              : 'shrink-0 whitespace-nowrap'}
            title="Build + Tessellate time"
          >
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
    <div class="justify-self-end flex items-center">
      <button
        type="button"
        class="btn btn-xs btn-primary h-6 min-h-0 px-2.5 gap-1"
        disabled={exportDisabled}
        title="Export model (Ctrl+E)"
        aria-label="Export model"
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
        <span class="hidden @[360px]:inline">Export…</span>
      </button>
    </div>
  {/if}
</div>
