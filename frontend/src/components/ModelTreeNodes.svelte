<script lang="ts">
  import type { TreeFolderNode, TreeNode } from '../lib/modelTree';
  import { studio } from '../lib/studioState.svelte';
  import type { ModelInfo } from '../lib/types';
  import ModelTreeNodes from './ModelTreeNodes.svelte';

  interface Props {
    nodes: TreeNode[];
    depth?: number;
    onModelContextMenu?: (event: MouseEvent, model: ModelInfo) => void;
    onLibraryContextMenu?: (event: MouseEvent, node: TreeFolderNode) => void;
  }

  let { nodes, depth = 0, onModelContextMenu, onLibraryContextMenu }: Props = $props();
</script>

<div class="space-y-0.5">
  {#each nodes as node (node.key)}
    {#if node.kind === 'folder'}
      {@const collapsed = studio.isFolderCollapsed(node.key)}
      {@const isLibrary = node.nodeType === 'library'}
      {@const isModelsSection = node.nodeType === 'models'}
      {@const isComponentsSection = node.nodeType === 'components'}
      {@const isSection = isModelsSection || isComponentsSection}
      <div class:pt-1={isLibrary && depth === 0} class:pt-2={isComponentsSection}>
        <button
          type="button"
          class="w-full flex items-center gap-1.5 rounded-field px-1.5 py-1 text-left hover:bg-base-200/80 transition-colors cursor-pointer select-none focus:outline-none focus-visible:outline-2 focus-visible:outline-primary"
          aria-expanded={!collapsed}
          title={node.error ? `${node.path || node.label}\n${node.error}` : (node.path || node.label)}
          onclick={() => studio.toggleFolder(node.key)}
          oncontextmenu={isLibrary
            ? (e) => {
                e.preventDefault();
                onLibraryContextMenu?.(e, node);
              }
            : undefined}
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

          <!-- Node Icon (Library vs Models Section vs Components Section vs Category Folder) -->
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
          {:else if isModelsSection}
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-3.5 w-3.5 shrink-0 text-primary/85"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="1.9"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z" />
              <path d="m3.3 7 8.7 5 8.7-5" />
              <path d="M12 22V12" />
            </svg>
          {:else if isComponentsSection}
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-3.5 w-3.5 shrink-0 text-secondary/90"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="1.9"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M5.5 8.5 9 12l-3.5 3.5L2 12l3.5-3.5Z" />
              <path d="m12 2 3.5 3.5L12 9 8.5 5.5 12 2Z" />
              <path d="M18.5 8.5 22 12l-3.5 3.5L15 12l3.5-3.5Z" />
              <path d="m12 15 3.5 3.5L12 22l-3.5-3.5L12 15Z" />
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
            class:font-semibold={isLibrary || isSection}
            class:font-medium={!isLibrary && !isSection}
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
            <span
              class="rounded-full bg-base-content/10 text-base-content/70 px-1.5 py-0.5 text-[10px] font-medium leading-none tabular-nums shrink-0"
              title="{node.modelCount} {isComponentsSection ? 'component' : 'model'}{node.modelCount === 1 ? '' : 's'}"
            >
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
              <ModelTreeNodes
                nodes={node.children}
                depth={depth + 1}
                {onModelContextMenu}
                {onLibraryContextMenu}
              />
            {/if}
          </div>
        {/if}
      </div>
    {:else}
      {@const isActive = studio.activeModelId === node.model.id}
      <button
        type="button"
        class="w-full flex flex-col items-start gap-0.5 rounded-field px-2 py-1.5 text-left transition-colors cursor-pointer select-none focus:outline-none focus-visible:outline-2 focus-visible:outline-primary {isActive
          ? 'bg-base-content/10 text-base-content font-semibold'
          : 'text-base-content hover:bg-base-content/10 font-normal'}"
        aria-current={isActive ? 'true' : undefined}
        title={node.model.kind === 'error' && node.model.parse_error
          ? `${node.model.rel_path}\n${node.model.parse_error}`
          : node.model.rel_path}
        onclick={() => studio.selectModel(node.model.id)}
        oncontextmenu={(e) => {
          e.preventDefault();
          onModelContextMenu?.(e, node.model);
        }}
      >
        <div class="flex items-center gap-1.5 w-full min-w-0">
          <span
            class="text-xs truncate flex-1 min-w-0 {isActive ? 'font-semibold' : 'font-medium'}"
            class:text-error={node.model.kind === 'error'}
          >
            {node.model.name}
          </span>
          {#if node.model.kind === 'error'}
            <span
              class="badge badge-xs badge-error shrink-0"
              title={node.model.parse_error || 'Failed to parse model script'}
            >
              !
            </span>
          {:else if node.model.kind === 'script'}
            <span
              class="badge badge-xs badge-ghost opacity-70 shrink-0"
              title="Standalone build123d script (no @dataclass parameters)"
            >
              Script
            </span>
          {/if}
          {#if isActive}
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-3.5 w-3.5 shrink-0 text-primary"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="2.5"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M20 6 9 17l-5-5" />
            </svg>
          {/if}
        </div>
        {#if node.model.kind === 'error' && node.model.parse_error}
          <span class="text-[11px] font-normal text-error/85 truncate w-full">{node.model.parse_error}</span>
        {:else if node.model.description}
          <span class="text-[11px] font-normal opacity-60 truncate w-full">{node.model.description}</span>
        {/if}
      </button>
    {/if}
  {/each}
</div>
