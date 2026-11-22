<script lang="ts">
  import { splitFields } from '../lib/params';
  import { studio } from '../lib/studioState.svelte';
  import Alert from './Alert.svelte';
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
              <p class="text-[11px] opacity-65 truncate" title={studio.modelDescription}>
                {studio.modelDescription}
              </p>
            {/if}
          </div>
          <div class="flex items-center gap-1 shrink-0">
            {#if isBusy}
              <span class="loading loading-spinner loading-xs text-primary"></span>
            {/if}
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
          </div>
        </div>
        <PresetBar />
      </div>

      <!-- Scrollable Parameters Form -->
      <div class="@container flex-1 overflow-y-auto overflow-x-hidden p-3 space-y-3">
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
                {#each split.coreFields as field (field.path)}
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

        {#each split.nestedGroups as group (group.path)}
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
