<script lang="ts">
  import { onMount } from 'svelte';
  import { CadViewportController } from '../lib/cadViewer';
  import { studio } from '../lib/studioState.svelte';

  let containerEl: HTMLDivElement | undefined = $state();

  onMount(() => {
    if (!containerEl) return;
    const controller = new CadViewportController(containerEl, studio.theme);
    studio.attachViewport(controller);
    return () => {
      studio.attachViewport(null);
      controller.dispose();
    };
  });
</script>

<main class="flex-1 relative min-w-0 min-h-0 bg-base-100 overflow-hidden p-2">
  <div id="cad-viewer-root" bind:this={containerEl}></div>
  {#if !studio.hasMesh}
    <div class="absolute inset-0 flex items-center justify-center pointer-events-none">
      <div class="text-center opacity-50 space-y-1">
        <div class="text-lg font-semibold">3D CAD Viewport</div>
        <div class="text-xs">Select a model from the sidebar to render</div>
      </div>
    </div>
  {/if}
</main>
