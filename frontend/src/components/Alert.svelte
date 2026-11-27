<script lang="ts">
  import type { Snippet } from 'svelte';

  export type AlertVariant = 'info' | 'success' | 'warning' | 'error';

  interface Props {
    variant: AlertVariant;
    title?: string;
    id?: string;
    role?: 'alert' | 'status';
    compact?: boolean;
    soft?: boolean;
    onDismiss?: () => void;
    action?: Snippet;
    class?: string;
    children?: Snippet;
  }

  let {
    variant,
    title,
    id,
    role,
    compact = false,
    soft = true,
    onDismiss,
    action,
    class: className = '',
    children,
  }: Props = $props();

  const VARIANT_CLASSES: Record<AlertVariant, string> = {
    info: 'alert-info',
    success: 'alert-success',
    warning: 'alert-warning',
    error: 'alert-error',
  };

  const VARIANT_HOVER_CLASSES: Record<AlertVariant, string> = {
    info: 'hover:bg-info/15',
    success: 'hover:bg-success/15',
    warning: 'hover:bg-warning/15',
    error: 'hover:bg-error/15',
  };

  const resolvedRole = $derived(role ?? (variant === 'error' ? 'alert' : 'status'));
  const iconSizeClass = 'h-3.5 w-3.5';
  const basePaddingClass = $derived(
    compact ? 'py-1.5! px-2! text-[11px]!' : 'py-2! px-2.5! text-xs!'
  );
  const iconBtnClass = $derived(
    `inline-flex items-center justify-center rounded-field h-6 w-6 p-0 text-current opacity-75 hover:opacity-100 ${VARIANT_HOVER_CLASSES[variant]} transition-colors cursor-pointer focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-current shrink-0`
  );
</script>

{#snippet alertIcon()}
  <svg
    xmlns="http://www.w3.org/2000/svg"
    class="{iconSizeClass} shrink-0"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    {#if variant === 'info'}
      <circle cx="12" cy="12" r="10" />
      <path d="M12 16v-4" />
      <path d="M12 8h.01" />
    {:else if variant === 'success'}
      <path d="M21.801 10A10 10 0 1 1 17 3.335" />
      <path d="m9 11 3 3L22 4" />
    {:else if variant === 'warning'}
      <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3" />
      <path d="M12 9v4" />
      <path d="M12 17h.01" />
    {:else}
      <circle cx="12" cy="12" r="10" />
      <line x1="12" x2="12" y1="8" y2="12" />
      <line x1="12" x2="12.01" y1="16" y2="16" />
    {/if}
  </svg>
{/snippet}

{#snippet dismissButton()}
  {#if onDismiss}
    <button
      type="button"
      class={iconBtnClass}
      title="Dismiss"
      aria-label="Dismiss notification"
      onclick={onDismiss}
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        class="h-3.5 w-3.5"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <line x1="18" y1="6" x2="6" y2="18" />
        <line x1="6" y1="6" x2="18" y2="18" />
      </svg>
    </button>
  {/if}
{/snippet}

{#if title}
  <div
    {id}
    role={resolvedRole}
    class="alert {VARIANT_CLASSES[variant]} {soft ? 'alert-soft' : ''} flex! items-start! gap-1.5! w-full {basePaddingClass} {className}"
  >
    <span class="shrink-0 mt-[1px]">{@render alertIcon()}</span>
    <div class="min-w-0 flex-1 space-y-0.5">
      <div class="flex items-start justify-between gap-2">
        <span class="font-semibold leading-snug min-w-0 flex-1">{title}</span>
        {#if action || onDismiss}
          <div class="flex items-center gap-1 shrink-0 ml-auto -my-0.5">
            {@render action?.()}
            {@render dismissButton()}
          </div>
        {/if}
      </div>
      {#if children}
        <div class="text-[11px] opacity-90 leading-snug space-y-1.5 w-full">
          {@render children()}
        </div>
      {/if}
    </div>
  </div>
{:else}
  <div
    {id}
    role={resolvedRole}
    class="alert {VARIANT_CLASSES[variant]} {soft ? 'alert-soft' : ''} flex! items-start! gap-1.5! w-full {basePaddingClass} {className}"
  >
    <span class="shrink-0 mt-[1px]">{@render alertIcon()}</span>
    <div class="min-w-0 flex-1 leading-snug">
      {@render children?.()}
    </div>
    {#if action || onDismiss}
      <div class="flex items-center gap-1 shrink-0 ml-auto -my-0.5">
        {@render action?.()}
        {@render dismissButton()}
      </div>
    {/if}
  </div>
{/if}
