<script lang="ts">
  import type { StatusState } from '../lib/types';
  import Alert from './Alert.svelte';

  interface Props {
    status: StatusState;
    rendering?: boolean;
    buildStartedAt?: number | null;
    onCancelBuild?: () => void;
  }

  let { status, rendering = false, buildStartedAt = null, onCancelBuild }: Props = $props();
  let copied = $state(false);
  let elapsedMs = $state(0);

  $effect(() => {
    if (!rendering) {
      elapsedMs = 0;
      return;
    }
    const start = buildStartedAt ?? Date.now();
    elapsedMs = Math.max(0, Date.now() - start);
    const timer = setInterval(() => {
      elapsedMs = Math.max(0, Date.now() - start);
    }, 100);
    return () => clearInterval(timer);
  });

  async function copyError() {
    if (status.kind !== 'error') return;
    const text = status.traceback ? `${status.error}\n\n${status.traceback}` : status.error;
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const ta = document.createElement('textarea');
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      ta.remove();
    }
    copied = true;
    setTimeout(() => {
      copied = false;
    }, 1500);
  }
</script>

<div class="text-xs">
  {#if rendering}
    <div class="flex items-center justify-between gap-2 text-xs">
      <div class="flex items-center gap-1.5 min-w-0">
        <span class="loading loading-spinner loading-xs text-primary shrink-0"></span>
        <span class="font-mono text-[11px] opacity-85 truncate">
          Building… ({(elapsedMs / 1000).toFixed(1)}s)
        </span>
      </div>
      {#if onCancelBuild}
        <button
          type="button"
          class="btn btn-xs btn-ghost text-error h-5 min-h-0 px-1.5 shrink-0"
          onclick={onCancelBuild}
        >
          Cancel
        </button>
      {/if}
    </div>
  {:else if status.kind === 'error'}
    <Alert variant="error" title="Build Error" class="select-text">
      {#snippet action()}
        <button
          type="button"
          class="btn btn-ghost btn-xs text-[10px] h-5 min-h-0 px-1.5"
          onclick={copyError}
        >
          {copied ? 'Copied!' : 'Copy'}
        </button>
      {/snippet}
      <p class="font-mono break-all select-text">{status.error}</p>
      {#if status.traceback}
        <details class="w-full">
          <summary class="cursor-pointer opacity-75 text-[10px] select-none">Show traceback</summary>
          <pre
            class="text-[10px] mt-1 p-2 bg-base-300/60 rounded overflow-x-auto max-h-44 font-mono whitespace-pre-wrap select-text"
          >{status.traceback}</pre>
        </details>
      {/if}
    </Alert>
  {:else if status.kind === 'message'}
    <div class="text-xs text-success font-medium select-text">{status.message}</div>
  {:else if status.kind === 'ok'}
    <div class="flex flex-wrap items-center justify-between gap-1 text-[11px] font-mono opacity-80 select-text">
      {#if status.bbox}
        <span title="Bounding box (X × Y × Z mm)">
          {status.bbox.dx.toFixed(2)} × {status.bbox.dy.toFixed(2)} × {status.bbox.dz.toFixed(2)} mm
        </span>
      {/if}
      <span title="Build + Tessellate time">
        {#if status.cached}
          cached
        {:else}
          {Math.round(status.buildTimeMs)}ms + {Math.round(status.tessellateTimeMs)}ms
        {/if}
      </span>
    </div>
  {:else}
    <div class="text-xs opacity-60">Ready</div>
  {/if}
</div>
