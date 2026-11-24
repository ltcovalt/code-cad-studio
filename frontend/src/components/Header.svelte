<script lang="ts">
  import { onMount } from 'svelte';
  import * as api from '../lib/api';
  import { DAISY_THEMES } from '../lib/themes';
  import { studio } from '../lib/studioState.svelte';
  import type { LayoutMode } from '../lib/types';

  interface Props {
    layoutMode: LayoutMode;
    onLayoutChange: (mode: LayoutMode) => void;
  }

  let { layoutMode, onLayoutChange }: Props = $props();
  let isMaximized = $state(false);

  async function syncWindowState() {
    if (!studio.hasDesktop) return;
    try {
      const res = await api.windowState();
      isMaximized = Boolean(res?.maximized);
    } catch {
      // ignore in web mode
    }
  }

  async function handleMinimize() {
    try {
      await api.windowMinimize();
    } catch {
      // ignore
    }
  }

  async function handleToggleMaximize() {
    try {
      const res = await api.windowToggleMaximize();
      isMaximized = Boolean(res?.maximized);
    } catch {
      // ignore
    }
  }

  async function handleClose() {
    try {
      await api.windowClose();
    } catch {
      // ignore
    }
  }

  function handleTitlebarDblClick(evt: MouseEvent) {
    if (!studio.hasDesktop) return;
    const target = evt.target as HTMLElement | null;
    if (target?.closest('.titlebar-no-drag')) return;
    void handleToggleMaximize();
  }

  onMount(() => {
    let timer: ReturnType<typeof setTimeout> | null = null;
    const onResize = () => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => void syncWindowState(), 120);
    };
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('resize', onResize);
      if (timer) clearTimeout(timer);
    };
  });

  $effect(() => {
    if (studio.hasDesktop) {
      void syncWindowState();
    }
  });

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

<!-- svelte-ignore a11y_no_static_element_interactions -->
<header
  class="navbar bg-base-200 border-b border-base-300 min-h-10 py-1 px-3 gap-2 shrink-0"
  class:titlebar-drag={studio.hasDesktop}
  class:macos-traffic-lights={studio.hasDesktop && studio.os === 'darwin'}
  ondblclick={studio.hasDesktop ? handleTitlebarDblClick : undefined}
>
  <div class="flex-1 flex items-center gap-3 min-w-0">
    <div class="flex items-center gap-2 font-bold text-base tracking-tight pointer-events-none">
      <span class="truncate">Code CAD Studio</span>
    </div>
    {#if studio.reloadedVisible}
      <span class="badge badge-ghost badge-xs pointer-events-none">Reloaded</span>
    {/if}
  </div>

  <div class="flex-none flex items-center gap-1" class:titlebar-no-drag={studio.hasDesktop}>
    <!-- Layout Switcher Dropdown -->
    <div class="dropdown dropdown-end">
      <div
        tabindex="0"
        role="button"
        class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0"
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
        class="dropdown-content bg-base-100 rounded-box z-50 w-48 p-1.5 shadow-xl border border-base-300 space-y-0.5"
      >
        <li>
          <button
            type="button"
            class="w-full flex items-center justify-between gap-2 px-2.5 py-1.5 rounded-field text-xs transition-colors cursor-pointer text-left {layoutMode === 'stacked'
              ? 'bg-base-content/10 text-base-content font-semibold'
              : 'text-base-content hover:bg-base-content/10 font-normal'}"
            aria-current={layoutMode === 'stacked' ? 'true' : undefined}
            onclick={() => selectLayout('stacked')}
          >
            <span class="flex items-center gap-2 min-w-0">
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
            </span>
            {#if layoutMode === 'stacked'}
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
          </button>
        </li>
        <li>
          <button
            type="button"
            class="w-full flex items-center justify-between gap-2 px-2.5 py-1.5 rounded-field text-xs transition-colors cursor-pointer text-left {layoutMode === 'side-by-side'
              ? 'bg-base-content/10 text-base-content font-semibold'
              : 'text-base-content hover:bg-base-content/10 font-normal'}"
            aria-current={layoutMode === 'side-by-side' ? 'true' : undefined}
            onclick={() => selectLayout('side-by-side')}
          >
            <span class="flex items-center gap-2 min-w-0">
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
            </span>
            {#if layoutMode === 'side-by-side'}
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
          </button>
        </li>
        <li>
          <button
            type="button"
            class="w-full flex items-center justify-between gap-2 px-2.5 py-1.5 rounded-field text-xs transition-colors cursor-pointer text-left {layoutMode === 'right'
              ? 'bg-base-content/10 text-base-content font-semibold'
              : 'text-base-content hover:bg-base-content/10 font-normal'}"
            aria-current={layoutMode === 'right' ? 'true' : undefined}
            onclick={() => selectLayout('right')}
          >
            <span class="flex items-center gap-2 min-w-0">
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
            </span>
            {#if layoutMode === 'right'}
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
          </button>
        </li>
      </ul>
    </div>

    <!-- Theme Switcher Dropdown -->
    <div class="dropdown dropdown-end">
      <div
        tabindex="0"
        role="button"
        class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0"
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
        class="dropdown-content bg-base-100 rounded-box z-50 w-40 p-1.5 shadow-xl border border-base-300 space-y-0.5"
      >
        {#each DAISY_THEMES as themeOption (themeOption.id)}
          {@const isSelected = studio.theme === themeOption.id}
          <li>
            <button
              type="button"
              class="w-full flex items-center justify-between gap-2 px-2.5 py-1.5 rounded-field text-xs transition-colors cursor-pointer text-left {isSelected
                ? 'bg-base-content/10 text-base-content font-semibold'
                : 'text-base-content hover:bg-base-content/10 font-normal'}"
              aria-current={isSelected ? 'true' : undefined}
              onclick={() => selectTheme(themeOption.id)}
            >
              <span>{themeOption.label}</span>
              {#if isSelected}
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
            </button>
          </li>
        {/each}
      </ul>
    </div>

    <!-- Settings Button -->
    <button
      type="button"
      class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0"
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

    {#if studio.hasDesktop && studio.os !== 'darwin'}
      <div class="h-4 w-px bg-base-content/15 mx-1" aria-hidden="true"></div>

      <!-- Minimize Button -->
      <button
        type="button"
        class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0"
        title="Minimize"
        aria-label="Minimize window"
        onclick={handleMinimize}
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
          <line x1="5" y1="12" x2="19" y2="12" />
        </svg>
      </button>

      <!-- Maximize / Restore Button -->
      <button
        type="button"
        class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0"
        title={isMaximized ? 'Restore' : 'Maximize'}
        aria-label={isMaximized ? 'Restore window' : 'Maximize window'}
        onclick={handleToggleMaximize}
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
          {#if isMaximized}
            <rect x="4" y="8" width="12" height="12" rx="1.5" />
            <path d="M8 8V5.5A1.5 1.5 0 0 1 9.5 4H18.5A1.5 1.5 0 0 1 20 5.5V14.5A1.5 1.5 0 0 1 18.5 16H16" />
          {:else}
            <rect x="4" y="4" width="16" height="16" rx="1.5" />
          {/if}
        </svg>
      </button>

      <!-- Close Button -->
      <button
        type="button"
        class="btn btn-ghost btn-square h-[26px] w-[26px] min-h-0 p-0 hover:bg-error/85 hover:text-error-content"
        title="Close"
        aria-label="Close window"
        onclick={handleClose}
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
          <line x1="6" y1="6" x2="18" y2="18" />
          <line x1="18" y1="6" x2="6" y2="18" />
        </svg>
      </button>
    {/if}
  </div>
</header>
