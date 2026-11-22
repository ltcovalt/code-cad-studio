<script lang="ts">
  import { tick } from 'svelte';
  import { studio } from '../lib/studioState.svelte';
  import type { VerificationStatus } from '../lib/types';
  import {
    VERIFICATION_STATUSES,
    formatVerificationTooltip,
    getPresetVerification,
    getVerificationMeta,
  } from '../lib/verification';
  import VerificationIcon from './VerificationIcon.svelte';

  const POPOVER_ID = 'preset-verification-popover';

  let open = $state(false);
  let draftStatus = $state<VerificationStatus>('unverified');
  let draftNote = $state('');
  let saving = $state(false);
  let formError = $state('');

  let triggerEl: HTMLButtonElement | undefined = $state();
  let panelEl: HTMLDivElement | undefined = $state();

  let panelLeft = $state(0);
  let panelWidth = $state(288);
  let panelTop = $state<number | null>(null);
  let panelBottom = $state<number | null>(null);

  const preset = $derived(studio.selectedPreset);
  const current = $derived(getPresetVerification(preset));
  const currentMeta = $derived(getVerificationMeta(current.status));
  const modified = $derived(studio.paramsModified);

  const hasNoteOnly = $derived(current.status === 'unverified' && current.note !== '');
  const hasAnySaved = $derived(current.status !== 'unverified' || current.note !== '');
  const isDirty = $derived(draftStatus !== current.status || draftNote.trim() !== current.note);

  const triggerTooltip = $derived.by(() => {
    if (!preset) return 'Preset verification';
    return formatVerificationTooltip(current.status, current.note, modified);
  });

  const sourceLabel = $derived.by(() => {
    if (!preset) return '';
    if (preset.source === 'default') return 'Model';
    if (preset.source === 'project') return 'Project';
    return 'User';
  });

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
    const maxViewportWidth = Math.max(220, window.innerWidth - margin * 2);
    const width = Math.min(288, maxViewportWidth);
    const preferredLeft = rect.right - width;
    const left = Math.max(margin, Math.min(preferredLeft, window.innerWidth - width - margin));

    const spaceBelow = window.innerHeight - rect.bottom - gap - margin;
    const flipUp = spaceBelow < 260 && rect.top > spaceBelow;

    panelLeft = left;
    panelWidth = width;
    if (flipUp) {
      panelTop = null;
      panelBottom = window.innerHeight - rect.top + gap;
    } else {
      panelTop = rect.bottom + gap;
      panelBottom = null;
    }
  }

  async function openPopover() {
    if (!preset) return;
    draftStatus = current.status;
    draftNote = current.note;
    formError = '';
    computePosition();
    open = true;
    await tick();
    panelEl?.focus();
  }

  function closePopover(restoreFocus = false) {
    if (!open) return;
    open = false;
    formError = '';
    if (restoreFocus) {
      triggerEl?.focus();
    }
  }

  function togglePopover() {
    if (open) {
      closePopover(false);
    } else {
      void openPopover();
    }
  }

  function handlePanelKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      closePopover(true);
    }
  }

  async function handleSave(e: SubmitEvent) {
    e.preventDefault();
    if (!preset) return;
    saving = true;
    formError = '';
    try {
      await studio.updatePresetVerification(preset.key, preset.source, draftStatus, draftNote);
      closePopover(true);
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  async function handleClear() {
    if (!preset) return;
    saving = true;
    formError = '';
    try {
      await studio.updatePresetVerification(preset.key, preset.source, 'unverified', '');
      closePopover(true);
    } catch (err) {
      formError = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }

  // Close popover if active model or selected preset changes underneath
  $effect(() => {
    void studio.activeModelId;
    void studio.selectedPresetKey;
    open = false;
  });

  $effect(() => {
    if (!open) return;

    const onPointerDown = (e: PointerEvent) => {
      const target = e.target as Node | null;
      if (!target) return;
      if (triggerEl?.contains(target) || panelEl?.contains(target)) return;
      closePopover(false);
    };

    const onResize = () => {
      closePopover(false);
    };

    window.addEventListener('pointerdown', onPointerDown, true);
    window.addEventListener('resize', onResize);

    return () => {
      window.removeEventListener('pointerdown', onPointerDown, true);
      window.removeEventListener('resize', onResize);
    };
  });
</script>

<button
  bind:this={triggerEl}
  type="button"
  disabled={!preset}
  aria-haspopup="dialog"
  aria-expanded={open}
  aria-controls={POPOVER_ID}
  aria-label={preset
    ? `Preset verification: ${currentMeta.label}${modified ? ' (parameters modified)' : ''}`
    : 'Preset verification'}
  title={triggerTooltip}
  class="relative btn btn-ghost btn-xs btn-square h-6 w-6 min-h-6 shrink-0 {open
    ? 'bg-base-200'
    : ''} {current.status === 'unverified' && !hasNoteOnly
    ? 'opacity-70 hover:opacity-100'
    : modified
      ? 'opacity-55 hover:opacity-90'
      : 'opacity-95 hover:opacity-100'}"
  onclick={togglePopover}
>
  <VerificationIcon status={current.status} class="h-3.5 w-3.5" />
  {#if modified && current.status !== 'unverified'}
    <span
      class="absolute top-0.5 right-0.5 h-1.5 w-1.5 rounded-full bg-warning ring-1 ring-base-100"
      aria-hidden="true"
    ></span>
  {:else if hasNoteOnly}
    <span
      class="absolute top-0.5 right-0.5 h-1.5 w-1.5 rounded-full bg-info ring-1 ring-base-100"
      aria-hidden="true"
    ></span>
  {/if}
</button>

{#if open && preset}
  <!-- Zero-transition fixed popover portaled to body to avoid overflow clipping & WebKitGTK WebGL lag -->
  <div
    use:portal
    bind:this={panelEl}
    id={POPOVER_ID}
    role="dialog"
    aria-label={`Verification for ${preset.label}`}
    tabindex="-1"
    class="fixed z-40 bg-base-100 border border-base-300 rounded-box shadow-md p-3 space-y-2.5 focus:outline-none"
    style="left: {panelLeft}px; width: {panelWidth}px; {panelTop !== null
      ? `top: ${panelTop}px;`
      : `bottom: ${panelBottom}px;`}"
    onkeydown={handlePanelKeydown}
  >
    <div class="flex items-start justify-between gap-2">
      <div class="min-w-0">
        <div class="flex items-center gap-1.5 min-w-0">
          <span class="text-xs font-semibold truncate">{preset.label}</span>
          <span class="badge badge-xs badge-soft shrink-0">{sourceLabel}</span>
        </div>
        <p class="text-[10px] opacity-60">Print verification &amp; notes</p>
      </div>
      <button
        type="button"
        class="btn btn-ghost btn-xs btn-square h-5 w-5 min-h-0 opacity-70 hover:opacity-100"
        aria-label="Close verification popover"
        onclick={() => closePopover(true)}
      >
        ✕
      </button>
    </div>

    {#if modified}
      <div class="rounded-field bg-warning/10 border border-warning/30 px-2 py-1.5 text-[11px] leading-snug text-warning">
        Live parameters differ from this preset. Reset or save your changes before verifying them.
      </div>
    {/if}

    <form class="space-y-2.5" onsubmit={handleSave}>
      <fieldset class="space-y-1">
        <legend class="text-[11px] font-semibold opacity-80">Status</legend>
        <div class="grid grid-cols-2 gap-1.5">
          {#each VERIFICATION_STATUSES as opt (opt.value)}
            {@const active = draftStatus === opt.value}
            <button
              type="button"
              aria-pressed={active}
              title={opt.hint}
              class="flex items-center gap-1.5 px-2 py-1.5 rounded-field border text-left text-[11px] cursor-pointer focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary {active
                ? `${opt.selectedButtonClass} font-semibold`
                : 'border-base-300 hover:border-base-content/30 bg-base-100'}"
              onclick={() => {
                draftStatus = opt.value;
              }}
            >
              <VerificationIcon status={opt.value} class="h-3.5 w-3.5" />
              <span class="truncate">{opt.label}</span>
            </button>
          {/each}
        </div>
      </fieldset>

      <div class="form-control">
        <label class="label py-0.5" for="popover-verification-note">
          <span class="label-text text-[11px] font-semibold opacity-80">Print Notes</span>
        </label>
        <textarea
          id="popover-verification-note"
          bind:value={draftNote}
          rows="3"
          placeholder="Material, layer height, fit feedback, or known issues..."
          class="textarea textarea-bordered textarea-xs w-full text-xs leading-snug"
        ></textarea>
      </div>

      {#if formError}
        <div class="alert alert-error alert-soft text-[11px] py-1.5 px-2">
          <span>{formError}</span>
        </div>
      {/if}

      <div class="flex items-center justify-between gap-2 pt-0.5">
        <div>
          {#if hasAnySaved}
            <button
              type="button"
              class="btn btn-ghost btn-xs text-error/80 hover:text-error"
              disabled={saving}
              onclick={handleClear}
            >
              Clear
            </button>
          {/if}
        </div>
        <div class="flex items-center gap-1.5">
          <button
            type="button"
            class="btn btn-ghost btn-xs"
            disabled={saving}
            onclick={() => closePopover(true)}
          >
            Cancel
          </button>
          <button
            type="submit"
            class="btn btn-primary btn-xs"
            disabled={saving || !isDirty}
          >
            {#if saving}
              <span class="loading loading-spinner loading-xs"></span>
            {:else}
              Save
            {/if}
          </button>
        </div>
      </div>
    </form>
  </div>
{/if}
