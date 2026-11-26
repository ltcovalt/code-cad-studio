<script module lang="ts">
  export interface LibraryDraft {
    uid: string;
    id: string;
    name: string;
    path: string;
    modelCount?: number;
  }

  let uidCounter = 0;

  export function nextLibraryDraftUid(): string {
    uidCounter += 1;
    return `lib-draft-${uidCounter}`;
  }
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';
  import * as api from '../lib/api';
  import { pathBasename, pathDirname, pathsEqual, suggestLibraryName } from '../lib/paths';
  import { studio } from '../lib/studioState.svelte';

  interface Props {
    libraries: LibraryDraft[];
    title: string;
    subtitle?: Snippet;
    emptyState?: Snippet;
    busy?: boolean;
    os?: string;
    reorderable?: boolean;
    libraryErrors?: Record<string, string>;
    onChange?: () => void;
    onError?: (message: string) => void;
  }

  let {
    libraries = $bindable<LibraryDraft[]>([]),
    title,
    subtitle,
    emptyState,
    busy = false,
    os = studio.os,
    reorderable = false,
    libraryErrors = {},
    onChange,
    onError,
  }: Props = $props();

  let manualPathInput = $state('');

  async function appendLibraryPath(rawPath: string): Promise<boolean> {
    const clean = rawPath.trim();
    if (!clean) return false;
    onError?.('');
    const inspect = await api.inspectSetupDir(clean).catch(() => null);
    const resolvedPath = inspect?.ok && inspect.info.path ? inspect.info.path : clean;
    if (
      libraries.some(
        (l) =>
          pathsEqual(l.path, clean, os) || pathsEqual(l.path, resolvedPath, os)
      )
    ) {
      onError?.('That library is already added.');
      return false;
    }
    libraries = [
      ...libraries,
      {
        uid: nextLibraryDraftUid(),
        id: '',
        name: suggestLibraryName(
          resolvedPath,
          libraries.map((l) => l.name)
        ),
        path: resolvedPath,
        modelCount: inspect?.ok ? inspect.info.model_count : undefined,
      },
    ];
    onChange?.();
    return true;
  }

  const LAST_BROWSE_DIR_KEY = 'code-cad-last-library-browse-dir';

  function getLastBrowseDir(): string {
    try {
      return localStorage.getItem(LAST_BROWSE_DIR_KEY) ?? '';
    } catch {
      return '';
    }
  }

  function setLastBrowseDir(dir: string): void {
    if (!dir) return;
    try {
      localStorage.setItem(LAST_BROWSE_DIR_KEY, dir);
    } catch {
      // ignore storage errors
    }
  }

  async function handleAddFolder() {
    onError?.('');
    try {
      const startDir =
        getLastBrowseDir() || (libraries[0]?.path ? pathDirname(libraries[0].path) : '');
      const resp = await api.browseProject(startDir);
      if (!resp.cancelled && resp.path) {
        setLastBrowseDir(pathDirname(resp.path));
        await appendLibraryPath(resp.path);
      }
    } catch (err) {
      onError?.(err instanceof Error ? err.message : String(err));
    }
  }

  async function handleAddManualPath() {
    try {
      if (await appendLibraryPath(manualPathInput)) {
        manualPathInput = '';
      }
    } catch (err) {
      onError?.(err instanceof Error ? err.message : String(err));
    }
  }

  function moveLibrary(index: number, delta: -1 | 1) {
    const target = index + delta;
    if (target < 0 || target >= libraries.length) return;
    const next = [...libraries];
    const [item] = next.splice(index, 1);
    next.splice(target, 0, item);
    libraries = next;
    onChange?.();
  }

  function removeLibrary(uid: string) {
    libraries = libraries.filter((l) => l.uid !== uid);
    onChange?.();
  }
</script>

<div class="space-y-2">
  <div class="flex items-start justify-between gap-2">
    <div>
      <span class="label-text text-xs font-semibold">
        {title} ({libraries.length})
      </span>
      {#if subtitle}
        {@render subtitle()}
      {/if}
    </div>
    {#if studio.hasDesktop}
      <button
        type="button"
        class="btn btn-xs btn-ghost border border-base-300 h-6 min-h-6 px-2 gap-1 text-[11px] shrink-0"
        disabled={busy}
        onclick={handleAddFolder}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-3 w-3"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2.4"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M12 5v14" />
          <path d="M5 12h14" />
        </svg>
        <span>Add Library</span>
      </button>
    {/if}
  </div>

  {#if libraries.length === 0}
    {#if emptyState}
      {@render emptyState()}
    {/if}
  {:else}
    <div class="space-y-1.5">
      {#each libraries as lib, idx (lib.uid)}
        {@const libErr = lib.id ? libraryErrors[lib.id] : ''}
        <div class="rounded-field border border-base-300 bg-base-200/40 px-2.5 py-1.5 space-y-1">
          <div class="flex items-center gap-2 min-w-0">
            <input
              type="text"
              bind:value={lib.name}
              disabled={busy}
              oninput={() => onChange?.()}
              placeholder={pathBasename(lib.path) || 'Library name'}
              aria-label="Library display name"
              class="input input-bordered input-xs w-36 font-medium text-xs shrink-0"
            />
            <span class="font-mono text-[11px] opacity-75 truncate flex-1 min-w-0" title={lib.path}>
              {lib.path}
            </span>
            {#if lib.modelCount !== undefined && !libErr}
              <span
                class="rounded-full bg-base-content/10 text-base-content/70 px-1.5 py-0.5 text-[10px] font-medium tabular-nums shrink-0"
                title="{lib.modelCount} model{lib.modelCount === 1 ? '' : 's'}"
              >
                {lib.modelCount}
              </span>
            {/if}
            <div class="flex items-center gap-0.5 shrink-0">
              {#if reorderable && libraries.length > 1}
                <button
                  type="button"
                  class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-0 opacity-60 hover:opacity-100 disabled:opacity-25"
                  title="Move up"
                  aria-label="Move library up"
                  disabled={busy || idx === 0}
                  onclick={() => moveLibrary(idx, -1)}
                >
                  <svg xmlns="http://www.w3.org/2000/svg" class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                    <polyline points="18 15 12 9 6 15" />
                  </svg>
                </button>
                <button
                  type="button"
                  class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-0 opacity-60 hover:opacity-100 disabled:opacity-25"
                  title="Move down"
                  aria-label="Move library down"
                  disabled={busy || idx === libraries.length - 1}
                  onclick={() => moveLibrary(idx, 1)}
                >
                  <svg xmlns="http://www.w3.org/2000/svg" class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2">
                    <polyline points="6 9 12 15 18 9" />
                  </svg>
                </button>
              {/if}
              <button
                type="button"
                class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-0 opacity-70 hover:opacity-100 hover:text-error"
                title="Remove library"
                aria-label="Remove library"
                disabled={busy}
                onclick={() => removeLibrary(lib.uid)}
              >
                <svg xmlns="http://www.w3.org/2000/svg" class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <line x1="6" y1="6" x2="18" y2="18" />
                  <line x1="18" y1="6" x2="6" y2="18" />
                </svg>
              </button>
            </div>
          </div>
          {#if libErr}
            <div class="text-[11px] text-warning">{libErr}</div>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  {#if !studio.hasDesktop}
    <!-- Fallback text entry for standalone browser mode without native folder dialogs -->
    <div class="join w-full">
      <input
        type="text"
        bind:value={manualPathInput}
        disabled={busy}
        onkeydown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            void handleAddManualPath();
          }
        }}
        placeholder="Enter library folder path (e.g. ~/Documents/my-cad-models)..."
        aria-label="Library folder path"
        class="input input-bordered input-xs join-item w-full font-mono text-[11px]"
      />
      <button
        type="button"
        class="btn btn-xs btn-neutral join-item"
        disabled={busy || !manualPathInput.trim()}
        onclick={handleAddManualPath}
      >
        Add
      </button>
    </div>
  {/if}
</div>
