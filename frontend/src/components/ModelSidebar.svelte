<script lang="ts">
  import { studio } from '../lib/studioState.svelte';

  interface Props {
    style?: string;
  }

  let { style = '' }: Props = $props();
</script>

<section
  id="models-pane"
  class="flex flex-col min-h-0 min-w-0 overflow-hidden shrink-0"
  {style}
>
  <div class="p-2.5 pb-2 border-b border-base-300/70 shrink-0 space-y-2">
    <div class="flex items-center justify-between gap-1">
      <span class="text-xs font-semibold uppercase tracking-wider opacity-70">Models</span>
      <button
        type="button"
        class="btn btn-ghost btn-xs px-1.5"
        title="Refresh model list"
        disabled={studio.modelsLoading}
        onclick={() => studio.refreshModels()}
      >
        {#if studio.modelsLoading}
          <span class="loading loading-spinner loading-xs"></span>
        {:else}
          Refresh
        {/if}
      </button>
    </div>
    <input
      type="search"
      bind:value={studio.searchQuery}
      placeholder="Filter models..."
      class="input input-bordered input-xs w-full"
    />
  </div>

  <div class="flex-1 overflow-y-auto space-y-1 p-2">
    {#if !studio.config.project_root}
      <div class="text-xs opacity-70 py-2">
        No project folder configured yet.
        <button
          type="button"
          class="link link-primary ml-1"
          onclick={() => (studio.settingsOpen = true)}
        >
          Open Settings
        </button>
      </div>
    {:else if studio.modelsError}
      <div class="alert alert-error alert-soft text-xs p-2">
        <span>{studio.modelsError}</span>
      </div>
    {:else if studio.modelsLoading && studio.models.length === 0}
      <div class="text-xs opacity-60 py-2">Loading models...</div>
    {:else if studio.filteredModels.length === 0}
      <div class="text-xs opacity-60 py-2">
        No models found{#if studio.searchQuery.trim()} matching "{studio.searchQuery.trim()}"{/if}.
      </div>
    {:else}
      {#each studio.filteredModels as model (model.id)}
        <button
          type="button"
          class="btn btn-ghost btn-xs w-full justify-start text-left h-auto py-1.5 px-2 font-normal flex flex-col items-start gap-0.5"
          class:btn-active={studio.activeModelId === model.id}
          onclick={() => studio.selectModel(model.id)}
        >
          <span class="font-medium text-xs truncate w-full">{model.name}</span>
          {#if model.description}
            <span class="text-[11px] opacity-60 truncate w-full">{model.description}</span>
          {/if}
        </button>
      {/each}
    {/if}
  </div>
</section>
