<script lang="ts">
  import { untrack } from 'svelte';
  import { pathBasename, revealActionLabel } from '../lib/paths';
  import { studio } from '../lib/studioState.svelte';
  import type { ExportFormat, ExportQuality } from '../lib/types';
  import Alert from './Alert.svelte';

  const FORMAT_STORAGE_KEY = 'code-cad-export-format';
  const VALID_FORMATS = new Set<ExportFormat>(['step', 'stl', '3mf']);

  function loadExportFormat(): ExportFormat {
    try {
      const raw = localStorage.getItem(FORMAT_STORAGE_KEY);
      if (raw && VALID_FORMATS.has(raw as ExportFormat)) {
        return raw as ExportFormat;
      }
    } catch {
      // ignore storage errors
    }
    return 'stl';
  }

  function saveExportFormat(fmt: ExportFormat): void {
    try {
      localStorage.setItem(FORMAT_STORAGE_KEY, fmt);
    } catch {
      // ignore storage errors
    }
  }

  interface FormatOption {
    value: ExportFormat;
    label: string;
    subtitle: string;
  }

  const FORMAT_OPTIONS: FormatOption[] = [
    { value: 'step', label: '.STEP', subtitle: 'Exact CAD solid / B-Rep' },
    { value: 'stl', label: '.STL', subtitle: '3D printing mesh' },
    { value: '3mf', label: '.3MF', subtitle: '3D printing package' },
  ];

  interface QualityOption {
    value: ExportQuality;
    label: string;
    detail: string;
  }

  const QUALITY_OPTIONS: QualityOption[] = [
    { value: 'draft', label: 'Draft', detail: 'Fast, coarser curves (0.02 mm / 0.3 rad)' },
    { value: 'standard', label: 'Standard', detail: 'Balanced detail & size (0.001 mm / 0.1 rad)' },
    { value: 'fine', label: 'Fine', detail: 'Smooth curves, larger files (0.0005 mm / 0.05 rad)' },
    { value: 'custom', label: 'Custom', detail: 'Set linear & angular deflection' },
  ];

  let dialogEl: HTMLDialogElement | undefined = $state();
  let dialogBodyEl: HTMLDivElement | undefined = $state();
  let pressedOutside = false;

  let format = $state<ExportFormat>(loadExportFormat());
  let quality = $state<ExportQuality>('standard');
  let linearDeflection = $state(0.001);
  let angularDeflection = $state(0.1);

  let exportError = $state('');
  let exportTraceback = $state('');
  let exportSuccess = $state<{ filename: string; sizeKb: number } | null>(null);

  let revealLabel = $derived(revealActionLabel(studio.os));
  let isMeshFormat = $derived(format === 'stl' || format === '3mf');

  let defaultExportDir = $derived.by(() => {
    if (studio.config.export_dir?.trim()) {
      return studio.resolvedExportDir || studio.config.export_dir.trim();
    }
    const libId = studio.activeModel?.library_id;
    const lib = libId ? (studio.config.libraries ?? []).find((l) => l.id === libId) : undefined;
    if (lib?.path) {
      const sep = lib.path.includes('\\') && !lib.path.includes('/') ? '\\' : '/';
      const trimmed = lib.path.replace(/[\\/]+$/, '');
      return `${trimmed}${sep}exports`;
    }
    return studio.resolvedExportDir;
  });

  function initializeForm() {
    format = loadExportFormat();
    quality = studio.exportQuality ?? studio.config.export_quality ?? 'standard';
    exportError = '';
    exportTraceback = '';
    exportSuccess = null;
  }

  let wasOpen = false;
  $effect(() => {
    const el = dialogEl;
    const isOpen = studio.exportModalOpen && Boolean(studio.activeModel);
    if (!el) return;
    if (isOpen && !wasOpen) {
      wasOpen = true;
      untrack(() => initializeForm());
      if (!el.open) {
        el.showModal();
      }
      queueMicrotask(() => {
        dialogBodyEl?.focus({ preventScroll: true });
      });
    } else if (!isOpen && wasOpen) {
      wasOpen = false;
      if (el.open) {
        el.close();
      }
    }
  });

  function closeModal() {
    if (studio.exporting) return;
    studio.exportModalOpen = false;
  }

  function handleDialogCancel(e: Event) {
    if (studio.exporting) {
      e.preventDefault();
    }
  }

  function handleDialogClose() {
    if (dialogEl) {
      dialogEl.returnValue = '';
    }
    wasOpen = false;
    pressedOutside = false;
    if (studio.exportModalOpen) {
      studio.exportModalOpen = false;
    }
  }

  function isOutsideDialog(e: PointerEvent, el: HTMLDialogElement): boolean {
    const r = el.getBoundingClientRect();
    return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
  }

  function handleDialogPointerDown(e: PointerEvent) {
    if (!dialogEl || 'closedBy' in HTMLDialogElement.prototype) return;
    pressedOutside = isOutsideDialog(e, dialogEl);
  }

  function handleDialogPointerUp(e: PointerEvent) {
    if (!dialogEl || 'closedBy' in HTMLDialogElement.prototype) return;
    if (pressedOutside && isOutsideDialog(e, dialogEl)) {
      closeModal();
    }
    pressedOutside = false;
  }

  function handleFormatChange(next: ExportFormat) {
    format = next;
    saveExportFormat(next);
    exportError = '';
    exportTraceback = '';
  }

  function handleQualityChange(next: ExportQuality) {
    quality = next;
    studio.setExportQuality(next);
    exportError = '';
    exportTraceback = '';
  }

  async function handleSubmit(e: SubmitEvent) {
    e.preventDefault();
    if (studio.exporting || !studio.activeModel) return;

    exportError = '';
    exportTraceback = '';
    exportSuccess = null;

    if (isMeshFormat && quality === 'custom') {
      if (!Number.isFinite(linearDeflection) || linearDeflection < 0.00001 || linearDeflection > 10) {
        exportError = 'Linear deflection must be between 0.00001 and 10 mm.';
        return;
      }
      if (!Number.isFinite(angularDeflection) || angularDeflection < 0.001 || angularDeflection > 3.15) {
        exportError = 'Angular deflection must be between 0.001 and 3.15 rad.';
        return;
      }
    }

    const opts = isMeshFormat
      ? {
          quality,
          ...(quality === 'custom'
            ? {
                linear_deflection: linearDeflection,
                angular_deflection: angularDeflection,
              }
            : {}),
        }
      : { quality: 'standard' as ExportQuality };

    const res = await studio.exportActiveModel(format, opts);
    if (res.cancelled) {
      return;
    }
    if (!res.ok) {
      exportError = res.error || 'Export failed';
      exportTraceback = res.traceback || '';
      return;
    }

    const fallbackName = `${studio.exportFilename.trim() || studio.activeModel.local_id || 'model'}.${format}`;
    const filename = res.path ? pathBasename(res.path) || fallbackName : fallbackName;
    const sizeKb = Math.ceil((res.size ?? 0) / 1024);
    exportSuccess = { filename, sizeKb };
  }
</script>

<dialog
  bind:this={dialogEl}
  class="studio-dialog"
  aria-labelledby="export-modal-title"
  {...({ closedby: studio.exporting ? 'none' : 'any' } as Record<string, unknown>)}
  oncancel={handleDialogCancel}
  onclose={handleDialogClose}
  onpointerdown={handleDialogPointerDown}
  onpointerup={handleDialogPointerUp}
>
  {#if studio.activeModel}
    <div
      bind:this={dialogBodyEl}
      tabindex="-1"
      class="bg-base-100 border border-base-300 rounded-box shadow-md w-[34rem] max-w-[calc(100vw-2rem)] max-h-[calc(100vh-2rem)] overflow-y-auto p-6 space-y-4 outline-none"
    >
      <div class="flex items-center justify-between gap-3">
        <div class="min-w-0">
          <h2 id="export-modal-title" class="font-bold text-base">Export Model</h2>
          <p class="text-[11px] opacity-60 truncate">{studio.activeModel.name}</p>
        </div>
        <button
          type="button"
          class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content shrink-0"
          title="Close"
          aria-label="Close export dialog"
          disabled={studio.exporting}
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

      <form
        class="space-y-4"
        aria-labelledby="export-modal-title"
        onsubmit={handleSubmit}
        novalidate
      >
        <!-- Format -->
        <fieldset class="space-y-1.5" disabled={studio.exporting}>
          <legend class="text-xs font-semibold">Format</legend>
          <div class="grid grid-cols-3 gap-2">
            {#each FORMAT_OPTIONS as opt (opt.value)}
              <label
                class={`flex items-start gap-2 p-2.5 rounded-box border cursor-pointer text-xs transition-colors ${
                  format === opt.value
                    ? 'border-primary bg-primary/5'
                    : 'border-base-300 bg-base-100 hover:border-base-content/25'
                }`}
              >
                <input
                  type="radio"
                  name="export-format"
                  value={opt.value}
                  checked={format === opt.value}
                  onchange={() => handleFormatChange(opt.value)}
                  class="radio radio-xs radio-primary mt-0.5 shrink-0"
                />
                <span class="leading-tight min-w-0">
                  <span class="font-semibold font-mono block">{opt.label}</span>
                  <span class="text-[11px] opacity-65 block mt-0.5">{opt.subtitle}</span>
                </span>
              </label>
            {/each}
          </div>
        </fieldset>

        <!-- Filename -->
        <div class="space-y-1">
          <div class="flex items-center justify-between gap-2">
            <label class="text-xs font-semibold" for="export-filename-input">Filename</label>
            {#if studio.exportFilenameEdited}
              <button
                type="button"
                class="btn btn-ghost btn-xs h-5 min-h-0 px-1.5 text-[11px] text-primary"
                disabled={studio.exporting}
                onclick={() => studio.resetExportFilename()}
              >
                Reset
              </button>
            {/if}
          </div>
          <div class="join w-full">
            <input
              id="export-filename-input"
              type="text"
              value={studio.exportFilename}
              disabled={studio.exporting}
              oninput={(e) => {
                studio.setExportFilename((e.currentTarget as HTMLInputElement).value);
                if (exportError) exportError = '';
              }}
              placeholder="export_filename"
              class="input input-bordered input-sm join-item flex-1 min-w-0 font-mono text-xs"
            />
            <span
              class="join-item inline-flex items-center px-3 bg-base-200 border border-l-0 border-base-300 text-xs font-mono opacity-75 select-none"
            >
              .{format}
            </span>
          </div>
        </div>

        <!-- Mesh Quality (Tessellation) -->
        {#if !isMeshFormat}
          <div class="rounded-box bg-base-200/60 border border-base-300 px-3 py-2.5 text-xs opacity-75">
            <span class="font-mono font-semibold">.STEP</span> exports exact analytic CAD geometry
            (B-Rep), so no mesh tessellation is needed.
          </div>
        {:else}
          <fieldset class="space-y-2" disabled={studio.exporting}>
            <div>
              <legend class="text-xs font-semibold">Mesh Quality (Tessellation)</legend>
              <p class="text-[11px] opacity-65 mt-0.5">
                Controls how finely curved surfaces are triangulated.
              </p>
            </div>
            <div class="grid grid-cols-2 gap-2">
              {#each QUALITY_OPTIONS as opt (opt.value)}
                <label
                  class={`flex items-start gap-2 p-2.5 rounded-box border cursor-pointer text-xs transition-colors ${
                    quality === opt.value
                      ? 'border-primary bg-primary/5'
                      : 'border-base-300 bg-base-100 hover:border-base-content/25'
                  }`}
                >
                  <input
                    type="radio"
                    name="export-quality"
                    value={opt.value}
                    checked={quality === opt.value}
                    onchange={() => handleQualityChange(opt.value)}
                    class="radio radio-xs radio-primary mt-0.5 shrink-0"
                  />
                  <span class="leading-tight min-w-0">
                    <span class="font-medium block">{opt.label}</span>
                    <span class="text-[11px] opacity-65 block mt-0.5">{opt.detail}</span>
                  </span>
                </label>
              {/each}
            </div>

            {#if quality === 'custom'}
              <div class="grid grid-cols-2 gap-2.5 rounded-box bg-base-200/60 border border-base-300 p-3">
                <div class="space-y-1">
                  <label class="text-[11px] font-medium block" for="export-linear-deflection">
                    Linear Deflection (mm)
                  </label>
                  <input
                    id="export-linear-deflection"
                    type="number"
                    min="0.00001"
                    max="10"
                    step="0.0005"
                    bind:value={linearDeflection}
                    class="input input-bordered input-xs w-full font-mono"
                  />
                </div>
                <div class="space-y-1">
                  <label class="text-[11px] font-medium block" for="export-angular-deflection">
                    Angular Deflection (rad)
                  </label>
                  <input
                    id="export-angular-deflection"
                    type="number"
                    min="0.001"
                    max="3.15"
                    step="0.01"
                    bind:value={angularDeflection}
                    class="input input-bordered input-xs w-full font-mono"
                  />
                </div>
              </div>
            {/if}
          </fieldset>
        {/if}

        <!-- Destination hint -->
        <div class="text-[11px] opacity-65 flex items-start gap-1.5 min-w-0">
          {#if studio.hasDesktop}
            <span class="shrink-0">Destination:</span>
            <span class="min-w-0">
              {#if defaultExportDir}
                <span class="font-mono break-all" title={defaultExportDir}>{defaultExportDir}</span>
                <span class="opacity-80"> (a Save dialog will open)</span>
              {:else}
                A Save dialog will open to choose the export location.
              {/if}
            </span>
          {:else}
            <span>The exported file will download in your browser.</span>
          {/if}
        </div>

        <!-- Inline feedback -->
        {#if exportError}
          <Alert variant="error" title="Export Failed">
            <p class="font-mono break-words whitespace-pre-wrap select-text">{exportError}</p>
            {#if exportTraceback}
              <details class="w-full pt-1">
                <summary class="cursor-pointer opacity-75 text-[10px] select-none">
                  Show Python traceback
                </summary>
                <pre
                  class="text-[10px] mt-1 p-2 bg-base-300/60 rounded overflow-x-auto max-h-40 font-mono whitespace-pre-wrap select-text"
                >{exportTraceback}</pre>
              </details>
            {/if}
          </Alert>
        {/if}

        {#if exportSuccess}
          <Alert variant="success" title="Exported">
            <p class="select-text">
              <code class="font-mono break-all">{exportSuccess.filename}</code>
              <span class="opacity-75 whitespace-nowrap">({exportSuccess.sizeKb} KB)</span>
            </p>
            {#if studio.hasDesktop && studio.lastExportPath}
              <div class="flex flex-wrap items-center gap-1.5 pt-1">
                <button
                  type="button"
                  class="btn btn-xs btn-ghost btn-success border border-success/30 hover:bg-success/15 hover:border-success/50 hover:text-success h-6 min-h-0 px-2 font-medium"
                  onclick={() => void studio.revealLastExport()}
                >
                  {revealLabel}
                </button>
                <button
                  type="button"
                  class="btn btn-xs btn-ghost btn-success border border-success/30 hover:bg-success/15 hover:border-success/50 hover:text-success h-6 min-h-0 px-2 font-medium"
                  onclick={() => void studio.openLastExportInSlicer()}
                >
                  Open in Slicer
                </button>
              </div>
            {/if}
          </Alert>
        {/if}

        <div class="flex justify-end gap-2 pt-2">
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            disabled={studio.exporting}
            onclick={closeModal}
          >
            {exportSuccess ? 'Done' : 'Cancel'}
          </button>
          <button type="submit" class="btn btn-sm btn-primary" disabled={studio.exporting}>
            {#if studio.exporting}
              <span class="loading loading-spinner loading-xs"></span>
              <span>Exporting…</span>
            {:else}
              Export .{format.toUpperCase()}
            {/if}
          </button>
        </div>
      </form>
    </div>
  {/if}
</dialog>
