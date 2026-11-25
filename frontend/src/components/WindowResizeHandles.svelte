<script lang="ts">
  import { studio } from '../lib/studioState.svelte';
  import { startNativeWindowResize, type WindowResizeEdge } from '../lib/windowResize';

  const visible = $derived(
    studio.hasDesktop && studio.os !== 'darwin' && !studio.windowMaximized
  );

  function beginResize(edge: WindowResizeEdge, evt: MouseEvent) {
    if (evt.button !== 0) return;
    evt.preventDefault();
    evt.stopPropagation();
    startNativeWindowResize(edge);
  }
</script>

{#if visible}
  <!-- Right edge resize strip (starts below titlebar close button, stops above corner grip) -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed top-10 bottom-4 right-0 w-[5px] z-30 cursor-ew-resize select-none"
    aria-hidden="true"
    onmousedown={(e) => beginResize('e-resize', e)}
  ></div>

  <!-- Bottom edge resize strip -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed bottom-0 left-3 right-4 h-[5px] z-30 cursor-ns-resize select-none"
    aria-hidden="true"
    onmousedown={(e) => beginResize('s-resize', e)}
  ></div>

  <!-- Bottom-right corner resize grip (sits above inner pane scrollbars) -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="fixed bottom-0 right-0 w-4 h-4 z-30 cursor-nwse-resize flex items-end justify-end p-[2px] text-base-content/35 hover:text-base-content/80 transition-colors select-none"
    title="Drag to resize window"
    aria-hidden="true"
    onmousedown={(e) => beginResize('se-resize', e)}
  >
    <svg
      xmlns="http://www.w3.org/2000/svg"
      class="h-2.5 w-2.5 pointer-events-none"
      viewBox="0 0 10 10"
      fill="none"
      stroke="currentColor"
      stroke-width="1.25"
      stroke-linecap="round"
      aria-hidden="true"
    >
      <line x1="9" y1="1.5" x2="1.5" y2="9" />
      <line x1="9" y1="5.25" x2="5.25" y2="9" />
    </svg>
  </div>
{/if}
