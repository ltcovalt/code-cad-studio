<script module lang="ts">
  import type { TreeFolderNode } from '../lib/modelTree';
  import type { ModelInfo } from '../lib/types';

  export type ExplorerContextTarget =
    | { kind: 'model'; model: ModelInfo }
    | { kind: 'library'; node: TreeFolderNode };
</script>

<script lang="ts">
  import { revealActionLabel } from '../lib/paths';
  import { studio } from '../lib/studioState.svelte';

  interface Props {
    open: boolean;
    x: number;
    y: number;
    target: ExplorerContextTarget | null;
    onClose: () => void;
    onNewModel?: (libraryId: string) => void;
  }

  let { open, x, y, target, onClose, onNewModel }: Props = $props();

  const VIEWPORT_MARGIN = 8;

  let menuEl: HTMLDivElement | undefined = $state();
  let posX = $state(0);
  let posY = $state(0);

  let revealLabel = $derived(revealActionLabel(studio.os));

  async function copyText(text: string): Promise<void> {
    if (navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(text);
        return;
      } catch {
        // Fall through to textarea fallback when Clipboard API is unavailable or blocked
      }
    }
    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', '');
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    textarea.style.pointerEvents = 'none';
    document.body.appendChild(textarea);
    textarea.select();
    try {
      document.execCommand('copy');
    } finally {
      document.body.removeChild(textarea);
    }
  }

  // Clamp menu position inside viewport bounds whenever opened, moved, or target changes
  $effect(() => {
    if (!open || !target) return;
    const rawX = x;
    const rawY = y;
    const el = menuEl;
    if (!el) {
      posX = rawX;
      posY = rawY;
      return;
    }
    const rect = el.getBoundingClientRect();
    const maxX = Math.max(VIEWPORT_MARGIN, window.innerWidth - rect.width - VIEWPORT_MARGIN);
    const maxY = Math.max(VIEWPORT_MARGIN, window.innerHeight - rect.height - VIEWPORT_MARGIN);
    posX = Math.max(VIEWPORT_MARGIN, Math.min(rawX, maxX));
    posY = Math.max(VIEWPORT_MARGIN, Math.min(rawY, maxY));
  });

  // Global dismissal listeners: outside pointerdown/click, Escape, scroll, window blur/resize
  $effect(() => {
    if (!open || !target) return;

    const handleOutsidePointerDown = (e: PointerEvent) => {
      if (menuEl && e.target instanceof Node && menuEl.contains(e.target)) return;
      onClose();
    };

    const handleOutsideClick = (e: MouseEvent) => {
      if (menuEl && e.target instanceof Node && menuEl.contains(e.target)) return;
      onClose();
    };

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        onClose();
        return;
      }
      if (!menuEl) return;
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Home' || e.key === 'End') {
        const items = Array.from(
          menuEl.querySelectorAll<HTMLButtonElement>('button[role="menuitem"]:not(:disabled)')
        );
        if (items.length === 0) return;
        e.preventDefault();
        const activeIdx = items.findIndex((item) => item === document.activeElement);
        let nextIdx = 0;
        if (e.key === 'Home') {
          nextIdx = 0;
        } else if (e.key === 'End') {
          nextIdx = items.length - 1;
        } else if (e.key === 'ArrowDown') {
          nextIdx = activeIdx >= 0 ? (activeIdx + 1) % items.length : 0;
        } else {
          nextIdx = activeIdx >= 0 ? (activeIdx - 1 + items.length) % items.length : items.length - 1;
        }
        items[nextIdx]?.focus();
      }
    };

    const handleScroll = (e: Event) => {
      if (menuEl && e.target instanceof Node && menuEl.contains(e.target)) return;
      onClose();
    };

    const handleWindowDismiss = () => {
      onClose();
    };

    window.addEventListener('pointerdown', handleOutsidePointerDown, true);
    window.addEventListener('click', handleOutsideClick, true);
    window.addEventListener('keydown', handleKeyDown, true);
    window.addEventListener('scroll', handleScroll, true);
    window.addEventListener('blur', handleWindowDismiss);
    window.addEventListener('resize', handleWindowDismiss);

    queueMicrotask(() => {
      menuEl?.focus({ preventScroll: true });
    });

    return () => {
      window.removeEventListener('pointerdown', handleOutsidePointerDown, true);
      window.removeEventListener('click', handleOutsideClick, true);
      window.removeEventListener('keydown', handleKeyDown, true);
      window.removeEventListener('scroll', handleScroll, true);
      window.removeEventListener('blur', handleWindowDismiss);
      window.removeEventListener('resize', handleWindowDismiss);
    };
  });

  function handleOpenInEditor(filePath: string) {
    onClose();
    void studio.openModelInEditor(filePath);
  }

  function handleRevealPath(path: string) {
    onClose();
    void studio.revealPath(path);
  }

  function handleCopyRelativePath(relPath: string) {
    onClose();
    void copyText(relPath);
  }

  async function handleExportModel(model: ModelInfo) {
    onClose();
    if (model.kind === 'error') return;
    if (studio.activeModelId !== model.id) {
      await studio.selectModel(model.id);
    }
    if (studio.activeModelId === model.id && !studio.modelLoadError && studio.activeModel?.kind !== 'error') {
      studio.exportModalOpen = true;
    }
  }

  function handleRebuild() {
    onClose();
    void studio.triggerRender(false, true);
  }

  function handleNewModel(libraryId: string) {
    onClose();
    onNewModel?.(libraryId);
  }

  function handleRefreshLibraries() {
    onClose();
    void studio.refreshModels();
  }
</script>

{#if open && target}
  <div
    bind:this={menuEl}
    role="menu"
    tabindex="-1"
    aria-label={target.kind === 'model'
      ? `Actions for ${target.model.name}`
      : `Actions for library ${target.node.label}`}
    class="fixed bg-base-100 rounded-box z-50 min-w-44 p-1 shadow-xl border border-base-300 space-y-0.5 text-xs focus:outline-none"
    style="left: {posX}px; top: {posY}px;"
    oncontextmenu={(e) => e.preventDefault()}
  >
    {#if target.kind === 'model'}
      {@const model = target.model}
      {@const filePath = model.py_path || model.path}
      {@const isActive = studio.activeModelId === model.id}
      {@const isError = model.kind === 'error'}

      {#if studio.hasDesktop && filePath}
        <button
          type="button"
          role="menuitem"
          class="w-full flex items-center gap-2 px-2 py-1.5 rounded-field text-left text-base-content hover:bg-base-content/10 transition-colors cursor-pointer"
          onclick={() => handleOpenInEditor(filePath)}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-3.5 w-3.5 shrink-0 opacity-75"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <polyline points="16 18 22 12 16 6" />
            <polyline points="8 6 2 12 8 18" />
          </svg>
          <span>Open in Editor</span>
        </button>

        <button
          type="button"
          role="menuitem"
          class="w-full flex items-center gap-2 px-2 py-1.5 rounded-field text-left text-base-content hover:bg-base-content/10 transition-colors cursor-pointer"
          onclick={() => handleRevealPath(filePath)}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-3.5 w-3.5 shrink-0 opacity-75"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="m6 14 1.5-2.9A2 2 0 0 1 9.24 10H20a2 2 0 0 1 1.94 2.5l-1.54 6a2 2 0 0 1-1.95 1.5H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H18a2 2 0 0 1 2 2v2" />
          </svg>
          <span>{revealLabel}</span>
        </button>
      {/if}

      <button
        type="button"
        role="menuitem"
        class="w-full flex items-center gap-2 px-2 py-1.5 rounded-field text-left text-base-content hover:bg-base-content/10 transition-colors cursor-pointer"
        onclick={() => handleCopyRelativePath(model.rel_path)}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-3.5 w-3.5 shrink-0 opacity-75"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <rect width="14" height="14" x="8" y="8" rx="2" ry="2" />
          <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
        </svg>
        <span>Copy Relative Path</span>
      </button>

      <div class="my-1 border-t border-base-300" role="separator"></div>

      <button
        type="button"
        role="menuitem"
        disabled={isError}
        class="w-full flex items-center gap-2 px-2 py-1.5 rounded-field text-left text-base-content hover:bg-base-content/10 transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed disabled:hover:bg-transparent"
        onclick={() => void handleExportModel(model)}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-3.5 w-3.5 shrink-0 opacity-75"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
          <polyline points="7 10 12 15 17 10" />
          <line x1="12" y1="15" x2="12" y2="3" />
        </svg>
        <span>Export…</span>
      </button>

      {#if isActive && !isError}
        <button
          type="button"
          role="menuitem"
          class="w-full flex items-center gap-2 px-2 py-1.5 rounded-field text-left text-base-content hover:bg-base-content/10 transition-colors cursor-pointer"
          onclick={handleRebuild}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-3.5 w-3.5 shrink-0 opacity-75"
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
          <span>Rebuild</span>
        </button>
      {/if}
    {:else}
      {@const node = target.node}

      <button
        type="button"
        role="menuitem"
        class="w-full flex items-center gap-2 px-2 py-1.5 rounded-field text-left text-base-content hover:bg-base-content/10 transition-colors cursor-pointer"
        onclick={() => handleNewModel(node.libraryId)}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-3.5 w-3.5 shrink-0 opacity-75"
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
        <span>New Model…</span>
      </button>

      {#if studio.hasDesktop && node.path}
        <button
          type="button"
          role="menuitem"
          class="w-full flex items-center gap-2 px-2 py-1.5 rounded-field text-left text-base-content hover:bg-base-content/10 transition-colors cursor-pointer"
          onclick={() => handleRevealPath(node.path ?? '')}
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-3.5 w-3.5 shrink-0 opacity-75"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
            aria-hidden="true"
          >
            <path d="m6 14 1.5-2.9A2 2 0 0 1 9.24 10H20a2 2 0 0 1 1.94 2.5l-1.54 6a2 2 0 0 1-1.95 1.5H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H18a2 2 0 0 1 2 2v2" />
          </svg>
          <span>{revealLabel}</span>
        </button>
      {/if}

      <button
        type="button"
        role="menuitem"
        class="w-full flex items-center gap-2 px-2 py-1.5 rounded-field text-left text-base-content hover:bg-base-content/10 transition-colors cursor-pointer"
        onclick={handleRefreshLibraries}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-3.5 w-3.5 shrink-0 opacity-75"
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
        <span>Refresh Libraries</span>
      </button>
    {/if}
  </div>
{/if}
