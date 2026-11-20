<script lang="ts">
  import type { StatusState } from '../lib/types';

  interface Props {
    status: StatusState;
  }

  let { status }: Props = $props();
  let copied = $state(false);

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
  {#if status.kind === 'error'}
    <div class="alert alert-error alert-soft text-xs p-2.5 flex flex-col items-start gap-1.5 select-text">
      <div class="flex items-center justify-between w-full gap-2">
        <span class="font-semibold">Build Error</span>
        <button
          type="button"
          class="btn btn-ghost btn-xs text-[10px] h-5 min-h-0 px-1.5"
          onclick={copyError}
        >
          {copied ? 'Copied!' : 'Copy'}
        </button>
      </div>
      <span class="font-mono text-[11px] break-all select-text">{status.error}</span>
      {#if status.traceback}
        <details class="w-full mt-0.5">
          <summary class="cursor-pointer opacity-75 text-[10px] select-none">Show traceback</summary>
          <pre
            class="text-[10px] mt-1 p-2 bg-base-300/60 rounded overflow-x-auto max-h-44 font-mono whitespace-pre-wrap select-text"
          >{status.traceback}</pre>
        </details>
      {/if}
    </div>
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
        {Math.round(status.buildTimeMs)}ms + {Math.round(status.tessellateTimeMs)}ms
      </span>
    </div>
  {:else}
    <div class="text-xs opacity-60">Ready</div>
  {/if}
</div>
