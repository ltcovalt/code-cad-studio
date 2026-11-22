<script lang="ts">
  import { onMount } from 'svelte';
  import CadViewport from './components/CadViewport.svelte';
  import Header from './components/Header.svelte';
  import ManagePresetsModal from './components/ManagePresetsModal.svelte';
  import ModelSidebar from './components/ModelSidebar.svelte';
  import ModelWorkspace from './components/ModelWorkspace.svelte';
  import OnboardingModal from './components/OnboardingModal.svelte';
  import SavePresetModal from './components/SavePresetModal.svelte';
  import SettingsModal from './components/SettingsModal.svelte';
  import { studio } from './lib/studioState.svelte';
  import type { LayoutMode, LayoutState } from './lib/types';

  const LAYOUT_STORAGE_KEY = 'code-cad-layout';
  const VALID_LAYOUTS = new Set<LayoutMode>(['stacked', 'side-by-side', 'right']);

  function loadLayout(): LayoutState {
    const defaults: LayoutState = {
      mode: 'stacked',
      sidebarWidth: 320,
      modelsHeight: 220,
      modelsWidth: 210,
      paramsWidth: 320,
    };
    try {
      const raw = localStorage.getItem(LAYOUT_STORAGE_KEY);
      if (raw) {
        const parsed = { ...defaults, ...JSON.parse(raw) } as LayoutState;
        if (!VALID_LAYOUTS.has(parsed.mode)) parsed.mode = 'stacked';
        return parsed;
      }
    } catch {
      // ignore
    }
    return defaults;
  }

  let layout = $state<LayoutState>(loadLayout());
  let workspaceEl: HTMLDivElement | undefined = $state();
  let wsWidth = $state(1200);
  let wsHeight = $state(760);
  let draggingSplitter = $state<'outer' | 'inner' | 'right' | null>(null);

  function saveLayout() {
    try {
      localStorage.setItem(LAYOUT_STORAGE_KEY, JSON.stringify(layout));
    } catch {
      // ignore
    }
  }

  function setLayoutMode(mode: LayoutMode) {
    if (!VALID_LAYOUTS.has(mode) || layout.mode === mode) return;
    layout.mode = mode;
    saveLayout();
  }

  // Clamped layout dimensions derived from workspace size
  let clamped = $derived.by(() => {
    const maxTotal = Math.max(420, Math.floor(wsWidth * 0.72));
    if (layout.mode === 'right') {
      const maxModels = Math.min(420, Math.max(150, Math.floor(wsWidth * 0.35)));
      const mw = Math.max(150, Math.min(maxModels, layout.modelsWidth));
      const maxParams = Math.min(520, Math.max(220, wsWidth - mw - 260));
      const pw = Math.max(220, Math.min(maxParams, layout.paramsWidth));
      return {
        leftAsideWidth: mw,
        modelsStyle: 'width: 100%; height: 100%;',
        rightAsideWidth: pw,
      };
    }
    if (layout.mode === 'stacked') {
      const w = Math.max(220, Math.min(maxTotal, layout.sidebarWidth));
      const maxH = Math.max(120, Math.floor(wsHeight * 0.7));
      const h = Math.max(100, Math.min(maxH, layout.modelsHeight));
      return {
        leftAsideWidth: w,
        modelsStyle: `width: 100%; height: ${h}px;`,
        rightAsideWidth: 0,
      };
    }
    // side-by-side
    const mw = Math.max(150, Math.min(420, layout.modelsWidth));
    const pw = Math.max(220, Math.min(Math.max(220, maxTotal - mw), layout.paramsWidth));
    return {
      leftAsideWidth: mw + 5 + pw,
      modelsStyle: `width: ${mw}px; height: 100%;`,
      rightAsideWidth: 0,
    };
  });

  function startOuterDrag(evt: PointerEvent) {
    evt.preventDefault();
    const target = evt.currentTarget as HTMLElement;
    target.setPointerCapture(evt.pointerId);
    draggingSplitter = 'outer';
    const startX = evt.clientX;
    const startSidebarW = layout.sidebarWidth;
    const startParamsW = layout.paramsWidth;
    const startModelsW = layout.modelsWidth;

    const onMove = (e: PointerEvent) => {
      const dx = e.clientX - startX;
      if (layout.mode === 'stacked') {
        layout.sidebarWidth = Math.max(220, startSidebarW + dx);
      } else if (layout.mode === 'side-by-side') {
        layout.paramsWidth = Math.max(220, startParamsW + dx);
      } else {
        layout.modelsWidth = Math.max(150, startModelsW + dx);
      }
    };

    const onUp = () => {
      draggingSplitter = null;
      target.removeEventListener('pointermove', onMove);
      target.removeEventListener('pointerup', onUp);
      target.removeEventListener('pointercancel', onUp);
      saveLayout();
    };

    target.addEventListener('pointermove', onMove);
    target.addEventListener('pointerup', onUp);
    target.addEventListener('pointercancel', onUp);
  }

  function startInnerDrag(evt: PointerEvent) {
    evt.preventDefault();
    const target = evt.currentTarget as HTMLElement;
    target.setPointerCapture(evt.pointerId);
    draggingSplitter = 'inner';
    const startX = evt.clientX;
    const startY = evt.clientY;
    const startModelsH = layout.modelsHeight;
    const startModelsW = layout.modelsWidth;

    const onMove = (e: PointerEvent) => {
      if (layout.mode === 'stacked') {
        layout.modelsHeight = Math.max(100, startModelsH + (e.clientY - startY));
      } else {
        layout.modelsWidth = Math.max(150, startModelsW + (e.clientX - startX));
      }
    };

    const onUp = () => {
      draggingSplitter = null;
      target.removeEventListener('pointermove', onMove);
      target.removeEventListener('pointerup', onUp);
      target.removeEventListener('pointercancel', onUp);
      saveLayout();
    };

    target.addEventListener('pointermove', onMove);
    target.addEventListener('pointerup', onUp);
    target.addEventListener('pointercancel', onUp);
  }

  function startRightDrag(evt: PointerEvent) {
    evt.preventDefault();
    const target = evt.currentTarget as HTMLElement;
    target.setPointerCapture(evt.pointerId);
    draggingSplitter = 'right';
    const startX = evt.clientX;
    const startParamsW = layout.paramsWidth;

    const onMove = (e: PointerEvent) => {
      const dx = e.clientX - startX;
      layout.paramsWidth = Math.max(220, startParamsW - dx);
    };

    const onUp = () => {
      draggingSplitter = null;
      target.removeEventListener('pointermove', onMove);
      target.removeEventListener('pointerup', onUp);
      target.removeEventListener('pointercancel', onUp);
      saveLayout();
    };

    target.addEventListener('pointermove', onMove);
    target.addEventListener('pointerup', onUp);
    target.addEventListener('pointercancel', onUp);
  }

  onMount(() => {
    void studio.init();

    const syncWorkspaceSize = () => {
      if (!workspaceEl) return;
      wsWidth = workspaceEl.clientWidth || window.innerWidth;
      wsHeight = workspaceEl.clientHeight || window.innerHeight;
    };
    syncWorkspaceSize();

    const ro = new ResizeObserver(syncWorkspaceSize);
    if (workspaceEl) ro.observe(workspaceEl);

    return () => {
      ro.disconnect();
      studio.destroy();
    };
  });
</script>

<Header layoutMode={layout.mode} onLayoutChange={setLayoutMode} />

<div
  id="workspace-root"
  bind:this={workspaceEl}
  class="flex-1 flex min-h-0 overflow-hidden"
>
  <!-- Left Sidebar (Models + optional docked Parameters) -->
  <aside
    class="bg-base-100 flex min-h-0 overflow-hidden shrink-0"
    style="width: {clamped.leftAsideWidth}px;"
  >
    <div
      class="flex w-full h-full min-h-0 overflow-hidden"
      class:flex-col={layout.mode !== 'side-by-side'}
      class:flex-row={layout.mode === 'side-by-side'}
    >
      <ModelSidebar style={clamped.modelsStyle} />

      {#if layout.mode !== 'right'}
        <!-- Inner Resizable Splitter (Models <-> Parameters) -->
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div
          class="splitter"
          class:splitter-h={layout.mode === 'stacked'}
          class:splitter-v={layout.mode === 'side-by-side'}
          class:dragging={draggingSplitter === 'inner'}
          title="Drag to resize models / parameters"
          onpointerdown={startInnerDrag}
        ></div>

        <ModelWorkspace />
      {/if}
    </div>
  </aside>

  <!-- Outer Vertical Splitter (Sidebar <-> 3D Viewport) -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="splitter splitter-v"
    class:dragging={draggingSplitter === 'outer'}
    title="Drag to resize panel"
    onpointerdown={startOuterDrag}
  ></div>

  <!-- Permanently Mounted 3D Viewport (NEVER unmounted across layout changes) -->
  <CadViewport />

  {#if layout.mode === 'right'}
    <!-- Right Vertical Splitter (3D Viewport <-> Right Parameters Panel) -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="splitter splitter-v"
      class:dragging={draggingSplitter === 'right'}
      title="Drag to resize parameters panel"
      onpointerdown={startRightDrag}
    ></div>

    <!-- Right Parameters Dock -->
    <aside
      class="bg-base-100 flex flex-col min-h-0 overflow-hidden shrink-0"
      style="width: {clamped.rightAsideWidth}px;"
    >
      <ModelWorkspace />
    </aside>
  {/if}
</div>

<OnboardingModal />
<SettingsModal />
<SavePresetModal />
<ManagePresetsModal />
