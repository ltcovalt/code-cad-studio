<script lang="ts">
  import { onMount } from 'svelte';
  import type { CadViewportController } from '../lib/cadViewer';
  import { studio } from '../lib/studioState.svelte';
  import ViewportStatusBar from './ViewportStatusBar.svelte';

  let containerEl: HTMLDivElement | undefined = $state();
  let viewportLoading = $state(true);
  let contextLost = $state(false);
  let activeController: CadViewportController | null = null;
  let cancelled = false;

  const hasRenderError = $derived(
    studio.status.kind === 'error' ||
      Boolean(studio.modelLoadError) ||
      studio.activeModel?.kind === 'error'
  );

  async function mountController() {
    if (!containerEl) return;
    viewportLoading = true;
    try {
      const { CadViewportController: Controller } = await import('../lib/cadViewer');
      if (cancelled || !containerEl) return;
      activeController = new Controller(containerEl, studio.theme, {
        onContextLost: () => {
          contextLost = true;
        },
        onContextRestored: () => {
          contextLost = false;
          if (studio.activeModelId) {
            void studio.triggerRender(false, true);
          }
        },
      });
      studio.attachViewport(activeController);
    } finally {
      if (!cancelled) {
        viewportLoading = false;
      }
    }
  }

  async function handleReloadViewport() {
    contextLost = false;
    studio.attachViewport(null);
    activeController?.dispose();
    activeController = null;
    await mountController();
    if (studio.activeModelId) {
      void studio.triggerRender(false, true);
    }
  }

  onMount(() => {
    cancelled = false;
    void mountController();
    return () => {
      cancelled = true;
      studio.attachViewport(null);
      activeController?.dispose();
      activeController = null;
    };
  });
</script>

<main class="flex-1 flex flex-col min-w-0 min-h-0 bg-base-100 overflow-hidden">
  <div class="flex-1 relative min-w-0 min-h-0 p-2">
    <div id="cad-viewer-root" bind:this={containerEl}></div>

    {#if contextLost}
      <div
        class="absolute inset-0 z-20 flex items-center justify-center bg-base-100/90 backdrop-blur-xs p-4"
        role="alert"
      >
        <div class="card bg-base-200 border border-error/40 shadow-lg max-w-sm p-4 text-center space-y-3">
          <div class="text-sm font-semibold text-error">3D Viewport Context Lost</div>
          <p class="text-xs opacity-75">
            The WebGL graphics context was interrupted by the system or GPU driver.
          </p>
          <div>
            <button
              type="button"
              class="btn btn-primary btn-xs"
              onclick={() => void handleReloadViewport()}
            >
              Reload Viewport
            </button>
          </div>
        </div>
      </div>
    {:else if viewportLoading}
      <div class="absolute inset-0 flex items-center justify-center pointer-events-none">
        <div class="flex items-center gap-2 text-xs opacity-60">
          <span class="loading loading-spinner loading-xs text-primary"></span>
          <span>Initializing 3D Viewport…</span>
        </div>
      </div>
    {:else if !studio.hasMesh}
      <div class="absolute inset-0 flex items-center justify-center pointer-events-none p-4">
        {#if hasRenderError}
          <div class="text-center space-y-1.5 max-w-sm">
            <div class="inline-flex items-center justify-center gap-1.5 text-lg font-semibold text-error/85">
              <svg
                xmlns="http://www.w3.org/2000/svg"
                class="h-5 w-5 shrink-0"
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
              <span>Failed to render 3D model</span>
            </div>
            <div class="text-xs opacity-60">
              Inspect the build error below or adjust parameters to rebuild
            </div>
          </div>
        {:else}
          <div class="text-center opacity-50 space-y-1">
            <div class="text-lg font-semibold">3D CAD Viewport</div>
            <div class="text-xs">Select a model from the sidebar to render</div>
          </div>
        {/if}
      </div>
    {/if}
  </div>

  <ViewportStatusBar />
</main>
