<script lang="ts">
  import { onMount } from 'svelte';
  import Alert from './components/Alert.svelte';
  import CadViewport from './components/CadViewport.svelte';
  import ExportModal from './components/ExportModal.svelte';
  import Header from './components/Header.svelte';
  import ManageLibrariesModal from './components/ManageLibrariesModal.svelte';
  import ManagePresetsModal from './components/ManagePresetsModal.svelte';
  import ModelSidebar from './components/ModelSidebar.svelte';
  import ModelWorkspace from './components/ModelWorkspace.svelte';
  import OnboardingModal from './components/OnboardingModal.svelte';
  import PresetDetailsModal from './components/PresetDetailsModal.svelte';
  import SavePresetModal from './components/SavePresetModal.svelte';
  import SettingsModal from './components/SettingsModal.svelte';
  import WindowResizeHandles from './components/WindowResizeHandles.svelte';
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

  function getBounds(mode: LayoutMode, width: number, height: number) {
    const maxTotal = Math.max(420, Math.floor(width * 0.72));
    if (mode === 'right') {
      const maxModels = Math.min(420, Math.max(150, Math.floor(width * 0.35)));
      const mw = Math.max(150, Math.min(maxModels, layout.modelsWidth));
      const maxParams = Math.min(520, Math.max(220, width - mw - 260));
      return {
        minSidebar: 220,
        maxSidebar: maxTotal,
        minModelsH: 100,
        maxModelsH: Math.max(120, Math.floor(height * 0.7)),
        minModelsW: 150,
        maxModelsW: maxModels,
        minParamsW: 220,
        maxParamsW: maxParams,
      };
    }
    if (mode === 'stacked') {
      return {
        minSidebar: 220,
        maxSidebar: maxTotal,
        minModelsH: 100,
        maxModelsH: Math.max(120, Math.floor(height * 0.7)),
        minModelsW: 150,
        maxModelsW: 420,
        minParamsW: 220,
        maxParamsW: 520,
      };
    }
    const maxModels = Math.min(420, Math.max(150, maxTotal - 220 - 5));
    const mw = Math.max(150, Math.min(maxModels, layout.modelsWidth));
    const maxParams = Math.max(220, maxTotal - mw - 5);
    return {
      minSidebar: 220,
      maxSidebar: maxTotal,
      minModelsH: 100,
      maxModelsH: Math.max(120, Math.floor(height * 0.7)),
      minModelsW: 150,
      maxModelsW: maxModels,
      minParamsW: 220,
      maxParamsW: maxParams,
    };
  }

  // Clamped layout dimensions derived from workspace size
  let clamped = $derived.by(() => {
    const b = getBounds(layout.mode, wsWidth, wsHeight);
    if (layout.mode === 'right') {
      const mw = Math.max(b.minModelsW, Math.min(b.maxModelsW, layout.modelsWidth));
      const pw = Math.max(b.minParamsW, Math.min(b.maxParamsW, layout.paramsWidth));
      return {
        sidebarWidth: mw,
        modelsHeight: layout.modelsHeight,
        modelsWidth: mw,
        paramsWidth: pw,
        gridTemplateColumns: `${mw}px 5px minmax(0, 1fr) 5px ${pw}px`,
        gridTemplateRows: 'minmax(0, 1fr)',
      };
    }
    if (layout.mode === 'stacked') {
      const w = Math.max(b.minSidebar, Math.min(b.maxSidebar, layout.sidebarWidth));
      const h = Math.max(b.minModelsH, Math.min(b.maxModelsH, layout.modelsHeight));
      return {
        sidebarWidth: w,
        modelsHeight: h,
        modelsWidth: layout.modelsWidth,
        paramsWidth: layout.paramsWidth,
        gridTemplateColumns: `${w}px 5px minmax(0, 1fr)`,
        gridTemplateRows: `${h}px 5px minmax(0, 1fr)`,
      };
    }
    // side-by-side
    const mw = Math.max(b.minModelsW, Math.min(b.maxModelsW, layout.modelsWidth));
    const pw = Math.max(b.minParamsW, Math.min(b.maxParamsW, layout.paramsWidth));
    return {
      sidebarWidth: mw,
      modelsHeight: layout.modelsHeight,
      modelsWidth: mw,
      paramsWidth: pw,
      gridTemplateColumns: `${mw}px 5px ${pw}px 5px minmax(0, 1fr)`,
      gridTemplateRows: 'minmax(0, 1fr)',
    };
  });

  function clamp(val: number, min: number, max: number): number {
    return Math.max(min, Math.min(max, val));
  }

  function startOuterDrag(evt: PointerEvent) {
    evt.preventDefault();
    const target = evt.currentTarget as HTMLElement;
    target.setPointerCapture(evt.pointerId);
    draggingSplitter = 'outer';
    const startX = evt.clientX;
    const startSidebarW = clamped.sidebarWidth;
    const startParamsW = clamped.paramsWidth;
    const startModelsW = clamped.modelsWidth;

    const onMove = (e: PointerEvent) => {
      const dx = e.clientX - startX;
      const b = getBounds(layout.mode, wsWidth, wsHeight);
      if (layout.mode === 'stacked') {
        layout.sidebarWidth = clamp(startSidebarW + dx, b.minSidebar, b.maxSidebar);
      } else if (layout.mode === 'side-by-side') {
        layout.paramsWidth = clamp(startParamsW + dx, b.minParamsW, b.maxParamsW);
      } else {
        layout.modelsWidth = clamp(startModelsW + dx, b.minModelsW, b.maxModelsW);
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
    const startModelsH = clamped.modelsHeight;
    const startModelsW = clamped.modelsWidth;

    const onMove = (e: PointerEvent) => {
      const b = getBounds(layout.mode, wsWidth, wsHeight);
      if (layout.mode === 'stacked') {
        layout.modelsHeight = clamp(startModelsH + (e.clientY - startY), b.minModelsH, b.maxModelsH);
      } else {
        layout.modelsWidth = clamp(startModelsW + (e.clientX - startX), b.minModelsW, b.maxModelsW);
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
    const startParamsW = clamped.paramsWidth;

    const onMove = (e: PointerEvent) => {
      const dx = e.clientX - startX;
      const b = getBounds('right', wsWidth, wsHeight);
      layout.paramsWidth = clamp(startParamsW - dx, b.minParamsW, b.maxParamsW);
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

  function handleWindowKeydown(e: KeyboardEvent) {
    if (
      studio.onboardingOpen ||
      studio.settingsOpen ||
      studio.librariesOpen ||
      studio.presetDetailsModalOpen ||
      studio.savePresetModalOpen ||
      studio.managePresetsModalOpen ||
      studio.exportModalOpen ||
      document.querySelector('dialog[open]')
    ) {
      return;
    }

    const mod = e.metaKey || e.ctrlKey;
    const key = e.key.toLowerCase();
    const target = e.target as HTMLElement | null;
    const inEditable = Boolean(
      target?.closest('input, textarea, select, [contenteditable="true"]')
    );

    if (mod && !e.altKey && key === 'k') {
      e.preventDefault();
      const searchEl = document.getElementById('model-search-input') as HTMLInputElement | null;
      searchEl?.focus();
      searchEl?.select();
      return;
    }

    if (mod && !e.altKey && e.key === ',') {
      e.preventDefault();
      studio.settingsOpen = true;
      return;
    }

    if (mod && !e.altKey && key === 's') {
      e.preventDefault();
      if (studio.activeModel && studio.activeModel.kind !== 'error' && !studio.modelLoadError) {
        studio.savePresetModalOpen = true;
      }
      return;
    }

    if (mod && !e.altKey && key === 'e') {
      e.preventDefault();
      if (
        studio.activeModel &&
        studio.activeModel.kind !== 'error' &&
        !studio.modelLoadError &&
        !studio.rendering &&
        !studio.exporting &&
        !studio.modelLoading &&
        studio.status.kind !== 'error'
      ) {
        studio.exportModalOpen = true;
      }
      return;
    }

    if (!inEditable && ((mod && !e.altKey && key === 'r') || e.key === 'F5')) {
      e.preventDefault();
      if (studio.activeModel && studio.activeModel.kind !== 'error' && !studio.modelLoadError) {
        void studio.triggerRender(false, true);
      }
    }
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

<svelte:window onkeydown={handleWindowKeydown} />

<Header layoutMode={layout.mode} onLayoutChange={setLayoutMode} />

{#if studio.workerError}
  <div class="px-3 py-1.5 border-b border-base-300 bg-base-200/70 shrink-0">
    <Alert variant="error" compact onDismiss={() => (studio.workerError = '')}>
      {#snippet action()}
        <button
          type="button"
          class="btn btn-xs btn-ghost h-6 min-h-0 px-2"
          onclick={() => (studio.settingsOpen = true)}
        >
          Configure Python
        </button>
      {/snippet}
      <span class="font-mono break-words">{studio.workerError}</span>
    </Alert>
  </div>
{/if}

<div
  id="workspace-root"
  bind:this={workspaceEl}
  data-layout={layout.mode}
  class="flex-1 grid min-h-0 overflow-hidden"
  style="grid-template-columns: {clamped.gridTemplateColumns}; grid-template-rows: {clamped.gridTemplateRows};"
>
  <ModelSidebar />

  {#if layout.mode !== 'right'}
    <!-- Inner Resizable Splitter (Models <-> Parameters) -->
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      class="splitter inner-splitter"
      class:splitter-h={layout.mode === 'stacked'}
      class:splitter-v={layout.mode === 'side-by-side'}
      class:dragging={draggingSplitter === 'inner'}
      title="Drag to resize models / parameters"
      onpointerdown={startInnerDrag}
    ></div>
  {/if}

  <ModelWorkspace />

  <!-- Outer Vertical Splitter (Sidebar <-> 3D Viewport) -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div
    class="splitter splitter-v outer-splitter"
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
      class="splitter splitter-v right-splitter"
      class:dragging={draggingSplitter === 'right'}
      title="Drag to resize parameters panel"
      onpointerdown={startRightDrag}
    ></div>
  {/if}
</div>

<OnboardingModal />
<SettingsModal />
<ManageLibrariesModal />
<PresetDetailsModal />
<SavePresetModal />
<ManagePresetsModal />
<ExportModal />
<WindowResizeHandles />

<style>
  #workspace-root[data-layout='stacked'] > :global(#models-pane) {
    grid-column: 1;
    grid-row: 1;
  }
  #workspace-root[data-layout='stacked'] > .inner-splitter {
    grid-column: 1;
    grid-row: 2;
  }
  #workspace-root[data-layout='stacked'] > :global(#model-workspace) {
    grid-column: 1;
    grid-row: 3;
  }
  #workspace-root[data-layout='stacked'] > .outer-splitter {
    grid-column: 2;
    grid-row: 1 / -1;
  }
  #workspace-root[data-layout='stacked'] > :global(main) {
    grid-column: 3;
    grid-row: 1 / -1;
  }

  #workspace-root[data-layout='side-by-side'] > :global(#models-pane) {
    grid-column: 1;
    grid-row: 1;
  }
  #workspace-root[data-layout='side-by-side'] > .inner-splitter {
    grid-column: 2;
    grid-row: 1;
  }
  #workspace-root[data-layout='side-by-side'] > :global(#model-workspace) {
    grid-column: 3;
    grid-row: 1;
  }
  #workspace-root[data-layout='side-by-side'] > .outer-splitter {
    grid-column: 4;
    grid-row: 1;
  }
  #workspace-root[data-layout='side-by-side'] > :global(main) {
    grid-column: 5;
    grid-row: 1;
  }

  #workspace-root[data-layout='right'] > :global(#models-pane) {
    grid-column: 1;
    grid-row: 1;
  }
  #workspace-root[data-layout='right'] > .outer-splitter {
    grid-column: 2;
    grid-row: 1;
  }
  #workspace-root[data-layout='right'] > :global(main) {
    grid-column: 3;
    grid-row: 1;
  }
  #workspace-root[data-layout='right'] > .right-splitter {
    grid-column: 4;
    grid-row: 1;
  }
  #workspace-root[data-layout='right'] > :global(#model-workspace) {
    grid-column: 5;
    grid-row: 1;
  }
</style>
