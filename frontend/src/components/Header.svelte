<script lang="ts">
  import { DAISY_THEMES } from '../lib/cadViewer';
  import { studio } from '../lib/studioState.svelte';
  import type { LayoutMode } from '../lib/types';

  interface Props {
    layoutMode: LayoutMode;
    onLayoutChange: (mode: LayoutMode) => void;
  }

  let { layoutMode, onLayoutChange }: Props = $props();

  function selectLayout(mode: LayoutMode) {
    onLayoutChange(mode);
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
  }

  function selectTheme(themeId: string) {
    studio.setTheme(themeId);
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
  }
</script>

<header class="navbar bg-base-200 border-b border-base-300 min-h-12 px-3 gap-2 shrink-0">
  <div class="flex-1 flex items-center gap-3 min-w-0">
    <div class="flex items-center gap-2 font-bold text-base tracking-tight">
      <span class="truncate">Code CAD Studio</span>
    </div>
    {#if studio.reloadedVisible}
      <span class="badge badge-ghost badge-xs">Reloaded</span>
    {/if}
  </div>

  <div class="flex-none flex items-center gap-1">
    <!-- Layout Switcher Dropdown -->
    <div class="dropdown dropdown-end">
      <div
        tabindex="0"
        role="button"
        class="btn btn-ghost btn-sm btn-square"
        title="Layout view"
        aria-label="Layout view"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-4 w-4"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          {#if layoutMode === 'stacked'}
            <rect x="3" y="4" width="18" height="16" rx="2" />
            <line x1="10" y1="4" x2="10" y2="20" />
            <line x1="3" y1="12" x2="10" y2="12" />
          {:else if layoutMode === 'side-by-side'}
            <rect x="3" y="4" width="18" height="16" rx="2" />
            <line x1="8" y1="4" x2="8" y2="20" />
            <line x1="13" y1="4" x2="13" y2="20" />
          {:else}
            <rect x="3" y="4" width="18" height="16" rx="2" />
            <line x1="8" y1="4" x2="8" y2="20" />
            <line x1="16" y1="4" x2="16" y2="20" />
          {/if}
        </svg>
      </div>
      <ul
        class="dropdown-content bg-base-300 rounded-box z-50 w-44 p-1.5 shadow-2xl border border-base-content/10 space-y-0.5"
      >
        <li>
          <button
            type="button"
            class="btn btn-xs btn-block btn-ghost justify-start gap-2 font-normal"
            class:btn-active={layoutMode === 'stacked'}
            onclick={() => selectLayout('stacked')}
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-3.5 w-3.5 shrink-0"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <rect x="3" y="4" width="18" height="16" rx="2" />
              <line x1="10" y1="4" x2="10" y2="20" />
              <line x1="3" y1="12" x2="10" y2="12" />
            </svg>
            <span>Single Column</span>
          </button>
        </li>
        <li>
          <button
            type="button"
            class="btn btn-xs btn-block btn-ghost justify-start gap-2 font-normal"
            class:btn-active={layoutMode === 'side-by-side'}
            onclick={() => selectLayout('side-by-side')}
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-3.5 w-3.5 shrink-0"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <rect x="3" y="4" width="18" height="16" rx="2" />
              <line x1="8" y1="4" x2="8" y2="20" />
              <line x1="13" y1="4" x2="13" y2="20" />
            </svg>
            <span>Side by Side</span>
          </button>
        </li>
        <li>
          <button
            type="button"
            class="btn btn-xs btn-block btn-ghost justify-start gap-2 font-normal"
            class:btn-active={layoutMode === 'right'}
            onclick={() => selectLayout('right')}
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-3.5 w-3.5 shrink-0"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <rect x="3" y="4" width="18" height="16" rx="2" />
              <line x1="8" y1="4" x2="8" y2="20" />
              <line x1="16" y1="4" x2="16" y2="20" />
            </svg>
            <span>Right Parameters</span>
          </button>
        </li>
      </ul>
    </div>

    <!-- Theme Switcher Dropdown -->
    <div class="dropdown dropdown-end">
      <div
        tabindex="0"
        role="button"
        class="btn btn-ghost btn-sm btn-square"
        title="Theme"
        aria-label="Theme"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-4 w-4"
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <circle cx="13.5" cy="6.5" r=".5" fill="currentColor" />
          <circle cx="17.5" cy="10.5" r=".5" fill="currentColor" />
          <circle cx="8.5" cy="7.5" r=".5" fill="currentColor" />
          <circle cx="6.5" cy="12.5" r=".5" fill="currentColor" />
          <path
            d="M12 2C6.5 2 2 6.5 2 12s4.5 10 10 10c.926 0 1.648-.746 1.648-1.688 0-.437-.18-.835-.437-1.125-.29-.289-.438-.652-.438-1.125a1.64 1.64 0 0 1 1.668-1.668h1.996c3.051 0 5.555-2.503 5.555-5.554C21.965 6.012 17.461 2 12 2z"
          />
        </svg>
      </div>
      <ul
        class="dropdown-content bg-base-300 rounded-box z-50 w-36 p-1.5 shadow-2xl border border-base-content/10 space-y-0.5"
      >
        {#each DAISY_THEMES as themeOption (themeOption.id)}
          <li>
            <button
              type="button"
              class="btn btn-xs btn-block btn-ghost justify-start font-normal"
              class:btn-active={studio.theme === themeOption.id}
              onclick={() => selectTheme(themeOption.id)}
            >
              {themeOption.label}
            </button>
          </li>
        {/each}
      </ul>
    </div>

    <!-- Settings Button -->
    <button
      type="button"
      class="btn btn-ghost btn-sm btn-square"
      title="Project & Python Settings"
      aria-label="Settings"
      onclick={() => (studio.settingsOpen = true)}
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        class="h-4 w-4"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path
          d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"
        />
        <circle cx="12" cy="12" r="3" />
      </svg>
    </button>
  </div>
</header>
