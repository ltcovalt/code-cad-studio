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
        class="btn btn-ghost btn-xs btn-square"
        title="Refresh model list"
        aria-label="Refresh model list"
        disabled={studio.modelsLoading}
        onclick={() => studio.refreshModels()}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-3.5 w-3.5"
          class:animate-spin={studio.modelsLoading}
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8" />
          <path d="M21 3v5h-5" />
        </svg>
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
      <div class="text-xs opacity-80 py-2 space-y-2">
        <div>No project folder configured yet.</div>
        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="btn btn-xs btn-primary"
            onclick={() => studio.openOnboarding()}
          >
            Run Setup Wizard
          </button>
          <button
            type="button"
            class="link link-hover text-xs opacity-75"
            onclick={() => (studio.settingsOpen = true)}
          >
            Open Settings
          </button>
        </div>
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
