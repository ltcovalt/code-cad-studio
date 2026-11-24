<script lang="ts">
  import { studio } from '../lib/studioState.svelte';
  import Alert from './Alert.svelte';
  import ModelTreeNodes from './ModelTreeNodes.svelte';
  import NewModelModal from './NewModelModal.svelte';

  interface Props {
    style?: string;
  }

  let { style = '' }: Props = $props();

  let newModelOpen = $state(false);
  let scaffoldingDemo = $state(false);
  let demoError = $state('');

  let hasExpandable = $derived(studio.expandableFolderKeys.length > 0);
  let allCollapsed = $derived.by(() => {
    const keys = studio.expandableFolderKeys;
    if (keys.length === 0) return false;
    return keys.every((k) => Boolean(studio.collapsedFolders[k]));
  });

  async function handleCreateDemo() {
    if (scaffoldingDemo) return;
    scaffoldingDemo = true;
    demoError = '';
    try {
      await studio.scaffoldDemo();
    } catch (err) {
      demoError = err instanceof Error ? err.message : String(err);
    } finally {
      scaffoldingDemo = false;
    }
  }
</script>

<section
  id="models-pane"
  class="bg-base-100 flex flex-col w-full h-full min-h-0 min-w-0 overflow-hidden"
  {style}
>
  <div class="p-2.5 pb-2 border-b border-base-300/70 shrink-0 space-y-2">
    <div class="flex items-center justify-between gap-1">
      <span class="text-xs font-semibold uppercase tracking-wider opacity-70 truncate">
        Explorer
      </span>
      <div class="flex items-center gap-0.5 shrink-0">
        {#if studio.hasLibraries}
          <button
            type="button"
            class="btn btn-ghost btn-xs btn-square"
            title="New Model"
            aria-label="New Model"
            onclick={() => (newModelOpen = true)}
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-3.5 w-3.5"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z" />
              <path d="M14 2v4a2 2 0 0 0 2 2h4" />
              <path d="M9 15h6" />
              <path d="M12 18v-6" />
            </svg>
          </button>
        {/if}

        {#if hasExpandable && !studio.searchQuery.trim()}
          <button
            type="button"
            class="btn btn-ghost btn-xs btn-square"
            title={allCollapsed ? 'Expand all folders' : 'Collapse all folders'}
            aria-label={allCollapsed ? 'Expand all folders' : 'Collapse all folders'}
            onclick={() =>
              allCollapsed ? studio.expandAllFolders() : studio.collapseAllFolders()}
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-3.5 w-3.5"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              {#if allCollapsed}
                <path d="m7 15 5 5 5-5" />
                <path d="m7 9 5-5 5 5" />
              {:else}
                <path d="m7 20 5-5 5 5" />
                <path d="m7 4 5 5 5-5" />
              {/if}
            </svg>
          </button>
        {/if}

        <button
          type="button"
          class="btn btn-ghost btn-xs btn-square"
          title="Refresh libraries & models"
          aria-label="Refresh libraries and models"
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

        <button
          type="button"
          class="btn btn-ghost btn-xs btn-square"
          title="Manage libraries"
          aria-label="Manage libraries"
          onclick={() => (studio.settingsOpen = true)}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-3.5 w-3.5"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="M12 10v6" />
            <path d="M9 13h6" />
            <path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z" />
          </svg>
        </button>
      </div>
    </div>
    <input
      id="model-search-input"
      type="search"
      aria-label="Filter models and folders"
      bind:value={studio.searchQuery}
      placeholder={studio.config.libraries.length > 1
        ? 'Filter libraries, folders, models...'
        : 'Filter models & folders...'}
      class="input input-bordered input-xs w-full"
    />
  </div>

  <div class="flex-1 overflow-y-auto p-2">
    {#if !studio.hasLibraries}
      <div class="text-xs opacity-85 py-2 space-y-2">
        <div>No model libraries configured yet.</div>
        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="btn btn-xs btn-primary"
            disabled={scaffoldingDemo}
            onclick={handleCreateDemo}
          >
            {#if scaffoldingDemo}
              <span class="loading loading-spinner loading-xs"></span>
              Creating…
            {:else}
              Create Demo Project
            {/if}
          </button>
          <button
            type="button"
            class="btn btn-xs btn-ghost border border-base-300"
            disabled={scaffoldingDemo}
            onclick={() => studio.openOnboarding(1)}
          >
            Setup Wizard
          </button>
          <button
            type="button"
            class="link link-hover text-xs opacity-75"
            disabled={scaffoldingDemo}
            onclick={() => (studio.settingsOpen = true)}
          >
            Settings
          </button>
        </div>
        {#if demoError}
          <Alert variant="error" compact>{demoError}</Alert>
        {/if}
      </div>
    {:else if studio.modelsError}
      <Alert variant="error" compact>{studio.modelsError}</Alert>
    {:else if studio.modelsLoading && studio.models.length === 0}
      <div class="text-xs opacity-60 py-2">Loading models...</div>
    {:else if studio.modelTree.length === 0}
      <div class="text-xs opacity-70 py-2 space-y-2">
        <div>
          No models found{#if studio.searchQuery.trim()} matching "{studio.searchQuery.trim()}"{/if}.
        </div>
        {#if !studio.searchQuery.trim()}
          <button
            type="button"
            class="btn btn-xs btn-outline"
            onclick={() => (newModelOpen = true)}
          >
            + New Model
          </button>
        {/if}
      </div>
    {:else}
      <ModelTreeNodes nodes={studio.modelTree} />
    {/if}
  </div>
</section>

<NewModelModal open={newModelOpen} onClose={() => (newModelOpen = false)} />
