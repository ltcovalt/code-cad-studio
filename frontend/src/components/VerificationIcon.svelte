<script lang="ts">
  import type { VerificationStatus } from '../lib/types';
  import { getVerificationMeta } from '../lib/verification';

  interface Props {
    status: VerificationStatus;
    class?: string;
    inheritColor?: boolean;
  }

  let { status, class: className = 'h-3.5 w-3.5', inheritColor = false }: Props = $props();

  const colorClass = $derived(inheritColor ? '' : getVerificationMeta(status).iconClass);
</script>

<svg
  xmlns="http://www.w3.org/2000/svg"
  class="{className} {colorClass} shrink-0"
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width="2"
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
>
  {#if status === 'verified'}
    <path d="M21.801 10A10 10 0 1 1 17 3.335" />
    <path d="m9 11 3 3L22 4" />
  {:else if status === 'usable'}
    <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3" />
    <path d="M12 9v4" />
    <path d="M12 17h.01" />
  {:else if status === 'unusable'}
    <circle cx="12" cy="12" r="10" />
    <path d="m15 9-6 6" />
    <path d="m9 9 6 6" />
  {:else}
    <path d="M10.1 2.182a10 10 0 0 1 3.8 0" />
    <path d="M13.9 21.818a10 10 0 0 1-3.8 0" />
    <path d="M17.609 3.721a10 10 0 0 1 2.69 2.7" />
    <path d="M2.182 13.9a10 10 0 0 1 0-3.8" />
    <path d="M20.279 17.609a10 10 0 0 1-2.7 2.69" />
    <path d="M21.818 10.1a10 10 0 0 1 0 3.8" />
    <path d="M3.721 6.391a10 10 0 0 1 2.7-2.69" />
    <path d="M6.391 20.279a10 10 0 0 1-2.69-2.7" />
    <path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3" />
    <path d="M12 17h.01" />
  {/if}
</svg>
