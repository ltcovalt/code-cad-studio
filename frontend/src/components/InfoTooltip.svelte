<script lang="ts">
  interface Props {
    text: string;
    label?: string;
    class?: string;
  }

  let { text, label = '', class: extraClass = '' }: Props = $props();

  const tooltipId = $props.id();

  let triggerEl: HTMLButtonElement | undefined = $state();
  let tooltipEl: HTMLDivElement | undefined = $state();
  let open = $state(false);
  let left = $state(0);
  let top = $state(0);
  let positioned = $state(false);

  function portal(node: HTMLElement) {
    document.body.appendChild(node);
    return {
      destroy() {
        node.remove();
      },
    };
  }

  function show() {
    open = true;
    positioned = false;
  }

  function hide() {
    open = false;
    positioned = false;
  }

  function updatePosition() {
    if (!triggerEl || !tooltipEl) return;
    const rect = triggerEl.getBoundingClientRect();
    const tipRect = tooltipEl.getBoundingClientRect();
    const margin = 8;
    const gap = 6;

    const centeredLeft = rect.left + rect.width / 2 - tipRect.width / 2;
    left = Math.max(margin, Math.min(centeredLeft, window.innerWidth - tipRect.width - margin));

    const aboveTop = rect.top - tipRect.height - gap;
    const belowTop = rect.bottom + gap;
    if (aboveTop >= margin) {
      top = aboveTop;
    } else {
      top = Math.min(belowTop, Math.max(margin, window.innerHeight - tipRect.height - margin));
    }
    positioned = true;
  }

  $effect(() => {
    if (!open || !tooltipEl) return;
    updatePosition();

    const onDismiss = () => hide();
    window.addEventListener('scroll', onDismiss, true);
    window.addEventListener('resize', onDismiss);
    return () => {
      window.removeEventListener('scroll', onDismiss, true);
      window.removeEventListener('resize', onDismiss);
    };
  });

  function handleClick(e: MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    if (open) {
      hide();
    } else {
      show();
    }
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape' && open) {
      e.stopPropagation();
      hide();
      triggerEl?.blur();
    }
  }
</script>

<button
  bind:this={triggerEl}
  type="button"
  class="inline-flex items-center justify-center shrink-0 rounded-full p-0.5 text-base-content/45 hover:text-base-content/90 focus-visible:text-base-content focus-visible:outline-1 focus-visible:outline-primary cursor-help {extraClass}"
  aria-label={label ? `Description for ${label}` : 'Parameter description'}
  aria-describedby={open ? tooltipId : undefined}
  onmouseenter={show}
  onmouseleave={hide}
  onfocus={show}
  onblur={hide}
  onclick={handleClick}
  onkeydown={handleKeydown}
>
  <svg
    xmlns="http://www.w3.org/2000/svg"
    class="h-3 w-3"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2.2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <circle cx="12" cy="12" r="10" />
    <path d="M12 16v-4" />
    <path d="M12 8h.01" />
  </svg>
</button>

{#if open}
  <div
    use:portal
    bind:this={tooltipEl}
    id={tooltipId}
    role="tooltip"
    class="fixed z-[60] pointer-events-none max-w-[260px] rounded-md bg-neutral text-neutral-content border border-base-content/15 shadow-lg px-2.5 py-1.5 text-[11px] font-normal leading-snug whitespace-normal break-words"
    style="left: {left}px; top: {top}px; visibility: {positioned ? 'visible' : 'hidden'};"
  >
    {text}
  </div>
{/if}
