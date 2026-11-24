<script lang="ts">
  import { splitFields } from '../lib/params';
  import { studio } from '../lib/studioState.svelte';
  import type { ExportQuality } from '../lib/types';
  import Alert from './Alert.svelte';
  import ParamField from './ParamField.svelte';
  import PresetBar from './PresetBar.svelte';
  import StatusBar from './StatusBar.svelte';

  let split = $derived(splitFields(studio.fields));
  let isBusy = $derived(studio.rendering || studio.exporting || studio.modelLoading);
  let hasModelError = $derived(
    studio.activeModel?.kind === 'error' || Boolean(studio.modelLoadError)
  );
  let exportDisabled = $derived(isBusy || studio.status.kind === 'error' || hasModelError);
  let activeBreadcrumb = $derived.by(() => {
    const model = studio.activeModel;
    if (!model) return '';
    const parts: string[] = [];
    if ((studio.config.libraries?.length ?? 0) > 1 && model.library_name) {
      parts.push(model.library_name);
    }
    if (model.folder_labels && model.folder_labels.length > 0) {
      parts.push(...model.folder_labels);
    }
    return parts.join(' / ');
  });
</script>

<section
  id="model-workspace"
  class="bg-base-100 flex flex-col w-full h-full min-h-0 min-w-0 overflow-hidden"
>
  {#if studio.modelLoading && !studio.activeModel}
    <div class="p-4 text-xs opacity-70 flex items-center justify-center gap-2 h-full">
      <span class="loading loading-spinner loading-xs text-primary"></span>
      <span>Loading model...</span>
    </div>
  {:else if studio.modelLoadError && !studio.activeModel}
    <div class="p-3 space-y-2 overflow-y-auto">
      <Alert variant="error" title="Failed to load model">
        <p class="font-mono break-all">{studio.modelLoadError}</p>
      </Alert>
      {#if studio.modelLoadTraceback}
        <details class="collapse collapse-arrow bg-base-200 border border-base-300 rounded-box">
          <summary class="collapse-title text-xs py-1.5 px-3 min-h-0">Python Traceback</summary>
          <pre
            class="collapse-content text-[10px] overflow-x-auto p-2 font-mono whitespace-pre-wrap"
          >{studio.modelLoadTraceback}</pre>
        </details>
      {/if}
    </div>
  {:else if !studio.activeModel}
    <div
      class="p-4 text-sm opacity-60 flex flex-col items-center justify-center h-full text-center gap-2"
    >
      <p>Select a model to inspect parameters and render in 3D.</p>
      {#if !studio.hasLibraries}
        <button
          type="button"
          class="btn btn-primary btn-sm mt-2"
          onclick={() => (studio.settingsOpen = true)}
        >
          Add Model Library
        </button>
      {/if}
    </div>
  {:else}
    <div class="flex flex-col h-full overflow-hidden">
      <!-- Model Title, Path, Desktop Actions & Presets -->
      <div class="p-3 border-b border-base-300 shrink-0 space-y-2">
        <div class="flex items-start justify-between gap-2">
          <div class="min-w-0 flex-1">
            {#if activeBreadcrumb}
              <div class="text-[11px] opacity-60 truncate" title={activeBreadcrumb}>
                {activeBreadcrumb}
              </div>
            {/if}
            <h2 class="font-semibold text-sm truncate">{studio.activeModel.name}</h2>
            <div
              class="text-[11px] font-mono opacity-55 truncate"
              title={studio.activeModel.py_path || studio.activeModel.rel_path}
            >
              {studio.activeModel.rel_path}
            </div>
            {#if studio.modelDescription}
              <p class="text-[11px] opacity-65 truncate mt-0.5" title={studio.modelDescription}>
                {studio.modelDescription}
              </p>
            {/if}
          </div>
          <div class="flex items-center gap-0.5 shrink-0">
            {#if isBusy}
              <span class="loading loading-spinner loading-xs text-primary mr-1"></span>
            {/if}
            {#if studio.hasDesktop}
              <button
                type="button"
                class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-0 opacity-70 hover:opacity-100"
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
                class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-0 opacity-70 hover:opacity-100"
                title="Reveal in Folder"
                aria-label="Reveal model file in folder"
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
            {#if !hasModelError && studio.activeModel.kind !== 'script'}
              <button
                type="button"
                class="btn btn-xs btn-square h-6 w-6 min-h-0 {studio.showDescriptions
                  ? 'btn-soft btn-primary'
                  : 'btn-ghost opacity-60 hover:opacity-100'}"
                aria-pressed={studio.showDescriptions}
                aria-label={studio.showDescriptions
                  ? 'Hide parameter descriptions'
                  : 'Show parameter descriptions'}
                title={studio.showDescriptions
                  ? 'Parameter descriptions shown inline (click for hover tooltips)'
                  : 'Parameter descriptions in hover tooltips (click to show inline)'}
                onclick={() => studio.toggleShowDescriptions()}
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
                  <circle cx="12" cy="12" r="10" />
                  <path d="M12 16v-4" />
                  <path d="M12 8h.01" />
                </svg>
              </button>
            {/if}
          </div>
        </div>
        {#if !hasModelError && studio.activeModel.kind !== 'script'}
          <PresetBar />
        {/if}
      </div>

      <!-- Scrollable Parameters / Script / Error Body -->
      <div class="@container flex-1 overflow-y-auto overflow-x-hidden p-3 space-y-3">
        {#if hasModelError}
          <Alert variant="error" title="Model Parse Error">
            <p class="font-mono break-all">
              {studio.modelLoadError || studio.activeModel.parse_error || 'Failed to parse model script'}
            </p>
          </Alert>
          {#if studio.modelLoadTraceback}
            <details class="collapse collapse-arrow bg-base-200 border border-base-300 rounded-box">
              <summary class="collapse-title text-xs py-1.5 px-3 min-h-0">Python Traceback</summary>
              <pre
                class="collapse-content text-[10px] overflow-x-auto p-2 font-mono whitespace-pre-wrap"
              >{studio.modelLoadTraceback}</pre>
            </details>
          {/if}
        {:else if studio.activeModel.kind === 'script'}
          <Alert variant="info" title="Direct build123d Script">
            <p>
              This script has no <code>@dataclass</code> parameters and renders its geometry directly.
              Edit the script to update the model, or add a <code>Params</code> dataclass for interactive controls.
            </p>
            <div class="pt-1">
              <button
                type="button"
                class="btn btn-xs btn-primary"
                disabled={isBusy}
                onclick={() => void studio.triggerRender(false, true)}
              >
                Rebuild
              </button>
            </div>
          </Alert>
        {:else}
          {#if split.coreFields.length > 0}
            <details
              class="collapse collapse-arrow bg-base-200/60 border border-base-300 rounded-box min-w-0"
              open
            >
              <summary class="collapse-title text-xs font-semibold min-h-0 py-2 pl-3 pr-9 min-w-0">
                Core Parameters
              </summary>
              <div class="collapse-content px-3 pb-3 pt-1 min-w-0">
                <div class="space-y-2.5 pt-2 border-t border-base-300/70 min-w-0">
                  {#each split.coreFields as field (`${studio.activeModelId}:${field.path}`)}
                    <ParamField
                      {field}
                      rootParams={studio.params}
                      groupParams={studio.params}
                      presetDefaults={studio.presetDefaults}
                      showDescriptions={studio.showDescriptions}
                      onUpdate={(path, val, mode) => studio.updateParam(path, val, mode)}
                      onPreviewCommit={() => studio.commitPreviewIfStale()}
                    />
                  {/each}
                </div>
              </div>
            </details>
          {/if}

          {#each split.nestedGroups as group (`${studio.activeModelId}:${group.path}`)}
            <ParamField
              field={group}
              rootParams={studio.params}
              groupParams={studio.params}
              presetDefaults={studio.presetDefaults}
              showDescriptions={studio.showDescriptions}
              onUpdate={(path, val, mode) => studio.updateParam(path, val, mode)}
              onPreviewCommit={() => studio.commitPreviewIfStale()}
            />
          {/each}
        {/if}
      </div>

      <!-- Export & Status Footer -->
      <div class="p-3 border-t border-base-300 bg-base-200/50 shrink-0 space-y-2">
        <div class="flex items-center gap-1.5">
          <input
            type="text"
            aria-label="Export filename"
            value={studio.exportFilename}
            disabled={hasModelError || studio.exporting}
            oninput={(e) => studio.setExportFilename((e.currentTarget as HTMLInputElement).value)}
            placeholder="export_filename"
            title="Export filename stem"
            class="input input-bordered input-xs flex-1 min-w-0 font-mono text-[11px]"
          />
          <select
            aria-label="STL/3MF tessellation quality"
            title="STL / 3MF mesh quality"
            class="select select-bordered select-xs text-[11px] h-6 min-h-6 shrink-0"
            value={studio.exportQuality}
            disabled={hasModelError || studio.exporting}
            onchange={(e) =>
              studio.setExportQuality((e.currentTarget as HTMLSelectElement).value as ExportQuality)}
          >
            <option value="draft">Draft</option>
            <option value="standard">Standard</option>
            <option value="fine">Fine</option>
            {#if studio.exportQuality === 'custom'}
              <option value="custom">Custom</option>
            {/if}
          </select>
        </div>

        <div class="flex items-center gap-1.5">
          <span
            class="text-[11px] font-semibold uppercase opacity-60 mr-auto flex items-center gap-1.5 truncate"
            title={studio.exportFilename ? `Filename: ${studio.exportFilename}` : 'Export'}
          >
            <span>Export</span>
            {#if studio.exporting}
              <span class="loading loading-spinner loading-xs text-primary"></span>
            {/if}
          </span>
          <button
            type="button"
            class="btn btn-xs btn-neutral"
            disabled={exportDisabled}
            title={studio.exportFilename ? `${studio.exportFilename}.step` : 'Export STEP'}
            onclick={() => studio.exportActiveModel('step')}
          >
            .STEP
          </button>
          <button
            type="button"
            class="btn btn-xs btn-neutral"
            disabled={exportDisabled}
            title={studio.exportFilename ? `${studio.exportFilename}.stl` : 'Export STL'}
            onclick={() => studio.exportActiveModel('stl')}
          >
            .STL
          </button>
          <button
            type="button"
            class="btn btn-xs btn-neutral"
            disabled={exportDisabled}
            title={studio.exportFilename ? `${studio.exportFilename}.3mf` : 'Export 3MF'}
            onclick={() => studio.exportActiveModel('3mf')}
          >
            .3MF
          </button>
        </div>

        {#if studio.lastExportPath && studio.hasDesktop}
          <div class="flex items-center justify-end gap-1.5">
            <button
              type="button"
              class="btn btn-xs btn-ghost border border-base-300 h-6 min-h-6"
              onclick={() => void studio.revealLastExport()}
            >
              Reveal
            </button>
            <button
              type="button"
              class="btn btn-xs btn-ghost border border-base-300 h-6 min-h-6"
              onclick={() => void studio.openLastExportInSlicer()}
            >
              Open in Slicer
            </button>
          </div>
        {/if}

        <StatusBar
          status={studio.status}
          rendering={studio.rendering}
          buildStartedAt={studio.buildStartedAt}
          onCancelBuild={() => void studio.cancelActiveBuild()}
        />
      </div>
    </div>
  {/if}
</section>
