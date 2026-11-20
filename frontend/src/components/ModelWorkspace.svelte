<script lang="ts">
  import { splitFields } from '../lib/params';
  import { studio } from '../lib/studioState.svelte';
  import ParamField from './ParamField.svelte';
  import PresetBar from './PresetBar.svelte';
  import StatusBar from './StatusBar.svelte';

  let split = $derived(splitFields(studio.fields));
  let isBusy = $derived(studio.rendering || studio.exporting || studio.modelLoading);
</script>

<section id="model-workspace" class="flex-1 flex flex-col min-h-0 min-w-0 overflow-hidden">
  {#if studio.modelLoading && !studio.activeModel}
    <div class="p-4 text-xs opacity-70 flex items-center justify-center gap-2 h-full">
      <span class="loading loading-spinner loading-xs text-primary"></span>
      <span>Loading model...</span>
    </div>
  {:else if studio.modelLoadError}
    <div class="p-3 space-y-2 overflow-y-auto">
      <div class="alert alert-error alert-soft text-xs p-2.5 flex flex-col items-start gap-1">
        <span class="font-semibold">Failed to load model</span>
        <span class="font-mono break-all">{studio.modelLoadError}</span>
      </div>
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
      {#if !studio.config.project_root}
        <button
          type="button"
          class="btn btn-primary btn-sm mt-2"
          onclick={() => (studio.settingsOpen = true)}
        >
          Choose Project Folder
        </button>
      {/if}
    </div>
  {:else}
    <div class="flex flex-col h-full overflow-hidden">
      <!-- Model Title & Presets -->
      <div class="p-3 border-b border-base-300 shrink-0 space-y-2">
        <div class="flex items-center justify-between gap-2">
          <div class="min-w-0">
            <h2 class="font-semibold text-sm truncate">{studio.activeModel.name}</h2>
            {#if studio.modelDescription}
              <p class="text-[11px] opacity-65 truncate">{studio.modelDescription}</p>
            {/if}
          </div>
          {#if isBusy}
            <span class="loading loading-spinner loading-xs text-primary shrink-0"></span>
          {/if}
        </div>
        <PresetBar />
      </div>

      <!-- Scrollable Parameters Form -->
      <div class="flex-1 overflow-y-auto p-3 space-y-3">
        {#if split.coreFields.length > 0}
          <details
            class="collapse collapse-arrow bg-base-200/60 border border-base-300 rounded-box"
            open
          >
            <summary class="collapse-title text-xs font-semibold min-h-0 py-2 pl-3 pr-9">
              Core Parameters
            </summary>
            <div class="collapse-content px-3 pb-3 pt-1">
              <div class="space-y-2.5 pt-1.5 border-t border-base-300/70">
                {#each split.coreFields as field (field.path)}
                  <ParamField
                    {field}
                    rootParams={studio.params}
                    groupParams={studio.params}
                    onUpdate={(path, val, immediate) => studio.updateParam(path, val, immediate)}
                  />
                {/each}
              </div>
            </div>
          </details>
        {/if}

        {#each split.nestedGroups as group (group.path)}
          <ParamField
            field={group}
            rootParams={studio.params}
            groupParams={studio.params}
            onUpdate={(path, val, immediate) => studio.updateParam(path, val, immediate)}
          />
        {/each}
      </div>

      <!-- Export & Status Footer -->
      <div class="p-3 border-t border-base-300 bg-base-200/50 shrink-0 space-y-2">
        <div class="flex items-center gap-1.5">
          <span
            class="text-[11px] font-semibold uppercase opacity-60 mr-auto truncate"
            title={studio.exportFilename ? `Filename: ${studio.exportFilename}` : 'Export'}
          >
            Export
          </span>
          <button
            type="button"
            class="btn btn-xs btn-neutral"
            disabled={studio.exporting}
            title={studio.exportFilename ? `${studio.exportFilename}.step` : 'Export STEP'}
            onclick={() => studio.exportActiveModel('step')}
          >
            .STEP
          </button>
          <button
            type="button"
            class="btn btn-xs btn-neutral"
            disabled={studio.exporting}
            title={studio.exportFilename ? `${studio.exportFilename}.stl` : 'Export STL'}
            onclick={() => studio.exportActiveModel('stl')}
          >
            .STL
          </button>
          <button
            type="button"
            class="btn btn-xs btn-neutral"
            disabled={studio.exporting}
            title={studio.exportFilename ? `${studio.exportFilename}.3mf` : 'Export 3MF'}
            onclick={() => studio.exportActiveModel('3mf')}
          >
            .3MF
          </button>
        </div>

        <StatusBar status={studio.status} />
      </div>
    </div>
  {/if}
</section>
