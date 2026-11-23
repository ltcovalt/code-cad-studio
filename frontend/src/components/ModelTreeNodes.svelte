<script lang="ts">
  import type { TreeNode } from '../lib/modelTree';
  import { studio } from '../lib/studioState.svelte';
  import ModelTreeNodes from './ModelTreeNodes.svelte';

  interface Props {
    nodes: TreeNode[];
    depth?: number;
  }

  let { nodes, depth = 0 }: Props = $props();
</script>

<div class="space-y-0.5">
  {#each nodes as node (node.key)}
    {#if node.kind === 'folder'}
      {@const collapsed = studio.isFolderCollapsed(node.key)}
      {@const isLibrary = node.nodeType === 'library'}
      <div class:pt-1={isLibrary && depth === 0}>
        <button
          type="button"
          class="w-full flex items-center gap-1.5 rounded-field px-1.5 py-1 text-left hover:bg-base-200/80 transition-colors cursor-pointer select-none focus:outline-none focus-visible:outline-2 focus-visible:outline-primary"
          aria-expanded={!collapsed}
          title={node.error ? `${node.path || node.label}\n${node.error}` : (node.path || node.label)}
          onclick={() => studio.toggleFolder(node.key)}
        >
          <!-- Chevron -->
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-3 w-3 shrink-0 opacity-60 transition-transform duration-100"
            class:rotate-90={!collapsed}
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="2.2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <polyline points="9 6 15 12 9 18" />
          </svg>

          <!-- Node Icon (Library vs Category Folder) -->
          {#if isLibrary}
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-3.5 w-3.5 shrink-0 opacity-75"
              class:text-warning={Boolean(node.error)}
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="1.9"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M12 2 2 7l10 5 10-5-10-5Z" />
              <path d="m2 17 10 5 10-5" />
              <path d="m2 12 10 5 10-5" />
            </svg>
          {:else}
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-3.5 w-3.5 shrink-0 opacity-70"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="1.9"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              {#if collapsed}
                <path d="M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.93a2 2 0 0 1-1.66-.9l-.82-1.2A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13c0 1.1.9 2 2 2Z" />
              {:else}
                <path d="m6 14 1.5-2.9A2 2 0 0 1 9.24 10H20a2 2 0 0 1 1.94 2.5l-1.54 6a2 2 0 0 1-1.95 1.5H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H18a2 2 0 0 1 2 2v2" />
              {/if}
            </svg>
          {/if}

          <span
            class="text-xs truncate min-w-0 flex-1"
            class:font-semibold={isLibrary}
            class:font-medium={!isLibrary}
          >
            {node.label}
            {#if node.subtitle}
              <span class="font-normal opacity-55 ml-1">({node.subtitle})</span>
            {/if}
          </span>

          {#if node.error}
            <span
              class="badge badge-xs badge-warning shrink-0"
              title={node.error}
            >
              !
            </span>
          {:else}
            <span class="text-[10px] opacity-50 tabular-nums shrink-0 pr-0.5">
              {node.modelCount}
            </span>
          {/if}
        </button>

        {#if !collapsed}
          <div class="ml-2.5 pl-1.5 border-l border-base-content/10 mt-0.5">
            {#if node.error}
              <div class="text-[11px] text-warning/90 px-2 py-1 break-words">
                {node.error}
              </div>
            {:else if node.children.length === 0}
              <div class="text-[11px] opacity-50 italic px-2 py-1">
                No models found
              </div>
            {:else}
              <ModelTreeNodes nodes={node.children} depth={depth + 1} />
            {/if}
          </div>
        {/if}
      </div>
    {:else}
      <button
        type="button"
        class="btn btn-ghost btn-xs w-full justify-start text-left h-auto py-1.5 px-2 font-normal flex flex-col items-start gap-0.5"
        class:btn-active={studio.activeModelId === node.model.id}
        title={node.model.rel_path}
        onclick={() => studio.selectModel(node.model.id)}
      >
        <span class="font-medium text-xs truncate w-full">{node.model.name}</span>
        {#if node.model.description}
          <span class="text-[11px] opacity-60 truncate w-full">{node.model.description}</span>
        {/if}
      </button>
    {/if}
  {/each}
</div>
