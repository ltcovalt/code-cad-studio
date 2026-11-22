<script lang="ts">
  import { tick } from 'svelte';
  import type { Preset } from '../lib/types';

  interface Props {
    presets: Preset[];
    selectedKey: string;
    defaultKey: string;
    onSelect: (key: string) => void;
  }

  let { presets, selectedKey, defaultKey, onSelect }: Props = $props();

  const LISTBOX_ID = 'preset-listbox';
  const LABEL_ID = 'preset-picker-label';
  const VALUE_ID = 'preset-picker-value';

  let open = $state(false);
  let highlightedIndex = $state(0);
  let triggerEl: HTMLButtonElement | undefined = $state();
  let menuEl: HTMLDivElement | undefined = $state();

  let menuLeft = $state(0);
  let menuWidth = $state(220);
  let menuTop = $state<number | null>(null);
  let menuBottom = $state<number | null>(null);
  let menuMaxHeight = $state(256);

  const selectedPreset = $derived(presets.find((p) => p.key === selectedKey) ?? null);

  const activeOptionId = $derived.by(() => {
    if (!open || presets.length === 0) return undefined;
    const preset = presets[highlightedIndex];
    return preset ? optionId(preset.key) : undefined;
  });

  function isStartupDefault(preset: Preset): boolean {
    return defaultKey !== '' ? preset.key === defaultKey : preset.key === 'default';
  }

  function optionId(key: string): string {
    return `preset-option-${key}`;
  }

  function portal(node: HTMLElement) {
    document.body.appendChild(node);
    return {
      destroy() {
        node.remove();
      },
    };
  }

  function computePosition() {
    if (!triggerEl) return;
    const rect = triggerEl.getBoundingClientRect();
    const margin = 8;
    const gap = 4;
    const maxViewportWidth = Math.max(120, window.innerWidth - margin * 2);
    const width = Math.min(Math.max(rect.width, 220), maxViewportWidth);
    const left = Math.max(margin, Math.min(rect.left, window.innerWidth - width - margin));

    const spaceBelow = window.innerHeight - rect.bottom - gap - margin;
    const spaceAbove = rect.top - gap - margin;
    const flipUp = window.innerHeight - rect.bottom < 180 && rect.top > 180;

    menuLeft = left;
    menuWidth = width;
    if (flipUp) {
      menuTop = null;
      menuBottom = window.innerHeight - rect.top + gap;
      menuMaxHeight = Math.max(80, Math.min(256, spaceAbove));
    } else {
      menuTop = rect.bottom + gap;
      menuBottom = null;
      menuMaxHeight = Math.max(80, Math.min(256, spaceBelow));
    }
  }

  function scrollHighlightedIntoView() {
    const preset = presets[highlightedIndex];
    if (!preset) return;
    const el = document.getElementById(optionId(preset.key));
    el?.scrollIntoView({ block: 'nearest' });
  }

  async function openMenu() {
    if (presets.length === 0) return;
    computePosition();
    const idx = presets.findIndex((p) => p.key === selectedKey);
    highlightedIndex = idx >= 0 ? idx : 0;
    open = true;
    await tick();
    menuEl?.focus({ preventScroll: true });
    scrollHighlightedIntoView();
  }

  function closeMenu(restoreFocus = false) {
    if (!open) return;
    open = false;
    if (restoreFocus) {
      triggerEl?.focus({ preventScroll: true });
    }
  }

  function toggleMenu() {
    if (open) {
      closeMenu(false);
    } else {
      void openMenu();
    }
  }

  function selectByIndex(index: number) {
    const preset = presets[index];
    if (!preset) return;
    onSelect(preset.key);
    closeMenu(true);
  }

  function moveHighlight(nextIndex: number) {
    if (presets.length === 0) return;
    const count = presets.length;
    highlightedIndex = ((nextIndex % count) + count) % count;
    scrollHighlightedIntoView();
  }

  function handleTriggerKeydown(e: KeyboardEvent) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (!open) {
        void openMenu();
      }
    } else if (e.key === 'Escape' && open) {
      e.preventDefault();
      e.stopPropagation();
      closeMenu(true);
    }
  }

  function handleListboxKeydown(e: KeyboardEvent) {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        moveHighlight(highlightedIndex + 1);
        break;
      case 'ArrowUp':
        e.preventDefault();
        moveHighlight(highlightedIndex - 1);
        break;
      case 'Home':
        e.preventDefault();
        if (presets.length > 0) {
          highlightedIndex = 0;
          scrollHighlightedIntoView();
        }
        break;
      case 'End':
        e.preventDefault();
        if (presets.length > 0) {
          highlightedIndex = presets.length - 1;
          scrollHighlightedIntoView();
        }
        break;
      case 'Enter':
      case ' ':
        e.preventDefault();
        selectByIndex(highlightedIndex);
        break;
      case 'Escape':
        e.preventDefault();
        e.stopPropagation();
        closeMenu(true);
        break;
      case 'Tab':
        // Return focus to trigger before closing so Tab naturally steps to adjacent controls
        open = false;
        triggerEl?.focus({ preventScroll: true });
        break;
    }
  }

  $effect(() => {
    if (!open) return;

    if (presets.length === 0) {
      open = false;
      return;
    }
    if (highlightedIndex >= presets.length) {
      highlightedIndex = Math.max(0, presets.length - 1);
    }

    const onPointerDown = (e: PointerEvent) => {
      const target = e.target as Node | null;
      if (!target) return;
      if (triggerEl?.contains(target) || menuEl?.contains(target)) return;
      closeMenu(false);
    };

    const onScroll = (e: Event) => {
      if (menuEl && e.target instanceof Node && menuEl.contains(e.target)) return;
      closeMenu(false);
    };

    const onResize = () => {
      closeMenu(false);
    };

    window.addEventListener('pointerdown', onPointerDown, true);
    window.addEventListener('scroll', onScroll, true);
    window.addEventListener('resize', onResize);

    return () => {
      window.removeEventListener('pointerdown', onPointerDown, true);
      window.removeEventListener('scroll', onScroll, true);
      window.removeEventListener('resize', onResize);
    };
  });
</script>

<div class="flex-1 min-w-0 @container">
  <span id={LABEL_ID} class="sr-only">Preset</span>
  <button
    bind:this={triggerEl}
    type="button"
    aria-haspopup="listbox"
    aria-expanded={open}
    aria-controls={LISTBOX_ID}
    aria-labelledby="{LABEL_ID} {VALUE_ID}"
    title={selectedPreset?.description || selectedPreset?.label || 'Preset'}
    disabled={presets.length === 0}
    class="flex items-center gap-1.5 w-full min-w-0 h-7 px-2 bg-base-100 border rounded-field text-left text-xs cursor-pointer focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary disabled:opacity-50 disabled:cursor-not-allowed {open
      ? 'border-primary/60'
      : 'border-base-content/20 hover:border-base-content/35'}"
    onclick={toggleMenu}
    onkeydown={handleTriggerKeydown}
  >
    <span id={VALUE_ID} class="flex-1 min-w-0 truncate">
      {selectedPreset?.label ?? 'Default'}
    </span>

    {#if selectedPreset && (selectedPreset.source === 'user' || isStartupDefault(selectedPreset))}
      <span class="hidden @[150px]:inline-flex items-center gap-1 shrink-0">
        {#if selectedPreset.source === 'user'}
          <span class="badge badge-xs badge-ghost h-4 px-1 text-[10px] opacity-70">User</span>
        {/if}
        {#if isStartupDefault(selectedPreset)}
          <span class="badge badge-xs badge-primary badge-soft h-4 px-1 text-[10px]">Default</span>
        {/if}
      </span>
    {/if}

    <svg
      xmlns="http://www.w3.org/2000/svg"
      class="h-3 w-3 shrink-0 text-base-content/70"
      class:rotate-180={open}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2.25"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden="true"
    >
      <path d="m6 9 6 6 6-6" />
    </svg>
  </button>
</div>

{#if open}
  <!-- Zero-transition fixed listbox portaled to body to avoid overflow clipping & WebKitGTK WebGL lag -->
  <div
    use:portal
    bind:this={menuEl}
    id={LISTBOX_ID}
    role="listbox"
    tabindex="-1"
    aria-labelledby={LABEL_ID}
    aria-activedescendant={activeOptionId}
    class="fixed z-40 overflow-y-auto bg-base-100 border border-base-300 rounded-box shadow-md p-1 space-y-0.5 focus:outline-none"
    style="left: {menuLeft}px; width: {menuWidth}px; max-height: {menuMaxHeight}px; {menuTop !== null
      ? `top: ${menuTop}px;`
      : `bottom: ${menuBottom}px;`}"
    onkeydown={handleListboxKeydown}
  >
    {#each presets as preset, index (preset.key)}
      {@const isSelected = preset.key === selectedKey}
      {@const isDefault = isStartupDefault(preset)}
      {@const isHighlighted = index === highlightedIndex}
      <!-- svelte-ignore a11y_click_events_have_key_events -->
      <div
        id={optionId(preset.key)}
        role="option"
        tabindex="-1"
        aria-selected={isSelected}
        title={preset.description || undefined}
        class="flex items-start gap-1.5 rounded-field px-2 py-1.5 cursor-pointer select-none {isHighlighted
          ? 'bg-base-200'
          : ''}"
        onpointermove={() => {
          if (highlightedIndex !== index) highlightedIndex = index;
        }}
        onmousedown={(e) => e.preventDefault()}
        onclick={() => selectByIndex(index)}
      >
        <div class="shrink-0 flex items-center justify-center w-3.5 h-4">
          {#if isSelected}
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-3.5 w-3.5 text-primary"
              viewBox="0 0 24 24"
              fill="none"
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

        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-1.5 min-w-0">
            <span class="text-xs truncate {isSelected ? 'font-semibold' : 'font-medium'}">
              {preset.label}
            </span>
            {#if preset.source === 'user'}
              <span class="badge badge-xs badge-ghost h-4 px-1 text-[10px] opacity-70 shrink-0">
                User
              </span>
            {/if}
            {#if isDefault}
              <span class="badge badge-xs badge-primary badge-soft h-4 px-1 text-[10px] shrink-0">
                Default
              </span>
            {/if}
          </div>
          {#if preset.description}
            <p class="text-[10px] opacity-55 truncate leading-tight mt-0.5">
              {preset.description}
            </p>
          {/if}
        </div>
      </div>
    {/each}
  </div>
{/if}
