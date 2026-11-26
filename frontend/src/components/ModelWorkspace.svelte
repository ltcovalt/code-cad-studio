<script lang="ts">
  import { splitFields } from '../lib/params';
  import { studio } from '../lib/studioState.svelte';
  import Alert from './Alert.svelte';
  import ParamField from './ParamField.svelte';
  import PresetBar from './PresetBar.svelte';

  let split = $derived(splitFields(studio.fields));
  let isBusy = $derived(studio.rendering || studio.exporting || studio.modelLoading);
  let hasModelError = $derived(
    studio.activeModel?.kind === 'error' || Boolean(studio.modelLoadError)
  );
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
      <!-- Parameters Header & Presets -->
      <div class="p-2.5 pb-2 border-b border-base-300/70 shrink-0 space-y-2">
        <div class="flex items-center justify-between gap-1 min-h-6">
          <span class="text-xs font-semibold uppercase tracking-wider opacity-70 truncate">
            Parameters
          </span>
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
              components={studio.components}
              selectedComponentPresets={studio.selectedComponentPresets}
              presetComponentPresets={studio.presetComponentPresets}
              onUpdate={(path, val, mode) => studio.updateParam(path, val, mode)}
              onPreviewCommit={() => studio.commitPreviewIfStale()}
              onToggleNullableGroup={(path, f, enabled) =>
                studio.toggleNullableGroup(path, f, enabled)}
              onSelectComponentPreset={(path, compId, presetKey) =>
                studio.selectComponentPreset(path, compId, presetKey)}
              onResetComponentGroup={(path, compId) =>
                studio.resetComponentGroupToPreset(path, compId)}
              onOpenComponent={(compId) => void studio.selectModel(compId)}
            />
          {/each}
        {/if}
      </div>
    </div>
  {/if}
</section>
