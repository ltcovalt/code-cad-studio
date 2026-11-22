<script lang="ts">
  import InfoTooltip from './InfoTooltip.svelte';
  import Self from './ParamField.svelte';
  import { evaluateWhen, getParamByPath, isPlainObject, stepNumber } from '../lib/params';
  import type { ParamField, ParamsRecord, ParamUpdateMode } from '../lib/types';

  interface Props {
    field: ParamField;
    rootParams: ParamsRecord;
    groupParams: ParamsRecord;
    showDescriptions: boolean;
    onUpdate: (path: string, value: unknown, mode?: ParamUpdateMode) => void;
    onPreviewCommit?: () => void;
  }

  let { field, rootParams, groupParams, showDescriptions, onUpdate, onPreviewCommit }: Props =
    $props();

  let isVisible = $derived(evaluateWhen(field.when, rootParams, groupParams));

  // For group fields
  let toggleChild = $derived(
    field.type === 'group'
      ? (field.children?.find((c) => c.name === 'enabled' && c.type === 'bool' && !c.nullable) ??
          null)
      : null
  );

  let bodyChildren = $derived(
    field.type === 'group'
      ? (field.children ?? []).filter((c) => !(toggleChild && c.name === toggleChild.name))
      : []
  );

  let currentValue = $derived(getParamByPath(rootParams, field.path));

  let childGroupParams = $derived<ParamsRecord>(
    isPlainObject(currentValue) ? currentValue : {}
  );

  let toggleChecked = $derived(
    toggleChild ? Boolean(getParamByPath(rootParams, toggleChild.path)) : true
  );

  let detailsOpen = $state(true);
  let lastToggleState = $state<boolean | null>(null);

  $effect(() => {
    if (field.type === 'group' && toggleChild) {
      if (lastToggleState === null || lastToggleState !== toggleChecked) {
        detailsOpen = toggleChecked;
        lastToggleState = toggleChecked;
      }
    }
  });

  // Numeric helpers
  let isInt = $derived(field.type === 'int');
  let stepVal = $derived(field.step ?? (isInt ? 1 : 0.1));
  let hasRange = $derived(field.min !== undefined && field.max !== undefined);
  let numValue = $derived<number | null>(
    typeof currentValue === 'number' && Number.isFinite(currentValue)
      ? currentValue
      : currentValue === null || currentValue === undefined || currentValue === ''
        ? null
        : Number.isFinite(Number(currentValue))
          ? Number(currentValue)
          : null
  );
  let rangeValue = $derived<number>(
    numValue !== null ? numValue : (field.min ?? 0)
  );

  function handleNumberInput(raw: string) {
    const trimmed = raw.trim();
    if (trimmed === '') {
      if (field.nullable) {
        onUpdate(field.path, null, 'debounced');
      }
      return;
    }
    const parsed = isInt ? Math.round(Number(trimmed)) : Number(trimmed);
    if (!Number.isFinite(parsed)) return;
    onUpdate(field.path, parsed, 'debounced');
  }

  function handleStep(direction: -1 | 1) {
    const next = stepNumber(numValue, direction, stepVal, isInt, field.min, field.max);
    onUpdate(field.path, next, 'debounced');
  }

  let rangePointerDown = false;
  let rangeDragged = false;
  let rangeStartValue: number | null = null;

  function parseNumericValue(raw: string): number | null {
    const parsed = isInt ? Math.round(Number(raw)) : Number(raw);
    return Number.isFinite(parsed) ? parsed : null;
  }

  function handleRangePointerDown() {
    rangePointerDown = true;
    rangeDragged = false;
    rangeStartValue = numValue;
  }

  function handleRangeKeyDown() {
    rangePointerDown = false;
    rangeDragged = false;
  }

  function handleRangeInput(raw: string) {
    const parsed = parseNumericValue(raw);
    if (parsed === null) return;
    if (rangePointerDown) {
      // Live-update UI readout and conditional fields while dragging, deferring CAD rebuild to release.
      rangeDragged = true;
      onUpdate(field.path, parsed, 'preview');
    } else {
      onUpdate(field.path, parsed, 'debounced');
    }
  }

  function handleRangeChange(raw: string) {
    const parsed = parseNumericValue(raw);
    const wasPointer = rangePointerDown || rangeDragged;
    rangePointerDown = false;
    rangeDragged = false;
    if (parsed === null) return;
    if (wasPointer) {
      onUpdate(field.path, parsed, 'immediate');
    }
  }

  function handleRangePointerUp(raw: string) {
    rangePointerDown = false;
    if (rangeDragged) {
      const parsed = parseNumericValue(raw);
      if (parsed !== null && parsed === rangeStartValue) {
        // Dragged away and returned to the original value: browser will not fire `change`.
        rangeDragged = false;
        onPreviewCommit?.();
      }
    }
  }

  function handleRangeCancel(raw: string) {
    const hadDrag = rangeDragged;
    rangePointerDown = false;
    rangeDragged = false;
    if (hadDrag) {
      const parsed = parseNumericValue(raw);
      if (parsed !== null) {
        onUpdate(field.path, parsed, 'immediate');
      } else {
        onPreviewCommit?.();
      }
    }
  }

  function handleClearNullable() {
    onUpdate(field.path, null, 'immediate');
  }

  function handleToggleGroupChange(checked: boolean) {
    if (!toggleChild) return;
    detailsOpen = checked;
    lastToggleState = checked;
    onUpdate(toggleChild.path, checked, 'immediate');
  }
</script>

{#if isVisible}
  {#if field.type === 'group'}
    <details
      class="collapse collapse-arrow bg-base-200/60 border border-base-300 rounded-box min-w-0"
      bind:open={detailsOpen}
    >
      <summary class="collapse-title min-h-0 py-2 pl-3 pr-9 min-w-0">
        <!-- Inner flex wrapper because DaisyUI v5 sets display: block on details > summary -->
        <div class="flex items-center justify-between gap-2 min-w-0">
          <div class="min-w-0 flex-1">
            <div class="text-xs font-semibold leading-tight break-words">{field.label}</div>
            {#if field.description}
              <div class="text-[11px] font-normal opacity-60 leading-snug mt-0.5 break-words">
                {field.description}
              </div>
            {/if}
          </div>
          {#if toggleChild}
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <span class="shrink-0 flex items-center" onclick={(e) => e.stopPropagation()}>
              <input
                type="checkbox"
                class="toggle toggle-primary toggle-xs"
                aria-label="Enable {field.label}"
                checked={toggleChecked}
                onchange={(e) => handleToggleGroupChange((e.currentTarget as HTMLInputElement).checked)}
              />
            </span>
          {/if}
        </div>
      </summary>
      {#if bodyChildren.length > 0}
        <div class="collapse-content px-3 pb-3 pt-1 min-w-0">
          <div
            class="space-y-2.5 pt-2 border-t border-base-300/70 transition-opacity min-w-0"
            class:opacity-45={toggleChild !== null && !toggleChecked}
          >
            {#each bodyChildren as child (child.path)}
              <Self
                field={child}
                {rootParams}
                groupParams={childGroupParams}
                {showDescriptions}
                {onUpdate}
                {onPreviewCommit}
              />
            {/each}
          </div>
        </div>
      {/if}
    </details>
  {:else if field.type === 'bool' && field.nullable}
    <div class="w-full min-w-0 py-0.5">
      <div
        class="flex flex-col @[240px]:flex-row @[240px]:items-center justify-between gap-1.5 @[240px]:gap-2 min-w-0"
      >
        <div class="flex items-center gap-1 min-w-0 flex-1">
          <span class="text-xs font-medium leading-snug break-words min-w-0">{field.label}</span>
          {#if field.description && !showDescriptions}
            <InfoTooltip text={field.description} label={field.label} />
          {/if}
        </div>
        <div
          class="join shrink-0 self-end @[240px]:self-auto"
          role="group"
          aria-label={field.label}
        >
          <button
            type="button"
            class="btn btn-xs join-item px-1.5"
            class:btn-primary={currentValue === null || currentValue === undefined}
            class:btn-ghost={currentValue !== null && currentValue !== undefined}
            class:border-base-300={currentValue !== null && currentValue !== undefined}
            onclick={() => onUpdate(field.path, null, 'immediate')}
          >
            Auto
          </button>
          <button
            type="button"
            class="btn btn-xs join-item px-1.5"
            class:btn-primary={currentValue === true}
            class:btn-ghost={currentValue !== true}
            class:border-base-300={currentValue !== true}
            onclick={() => onUpdate(field.path, true, 'immediate')}
          >
            On
          </button>
          <button
            type="button"
            class="btn btn-xs join-item px-1.5"
            class:btn-primary={currentValue === false}
            class:btn-ghost={currentValue !== false}
            class:border-base-300={currentValue !== false}
            onclick={() => onUpdate(field.path, false, 'immediate')}
          >
            Off
          </button>
        </div>
      </div>
      {#if field.description && showDescriptions}
        <p class="text-[11px] opacity-60 leading-snug mt-0.5 break-words">{field.description}</p>
      {/if}
    </div>
  {:else if field.type === 'bool'}
    <div class="w-full min-w-0 py-0.5">
      <div class="flex items-center justify-between gap-2 min-w-0">
        <div class="flex items-center gap-1 min-w-0 flex-1">
          <label
            for="param-{field.path}"
            class="text-xs font-medium leading-snug cursor-pointer break-words min-w-0"
          >
            {field.label}
          </label>
          {#if field.description && !showDescriptions}
            <InfoTooltip text={field.description} label={field.label} />
          {/if}
        </div>
        <input
          id="param-{field.path}"
          type="checkbox"
          class="toggle toggle-primary toggle-xs shrink-0"
          checked={Boolean(currentValue)}
          onchange={(e) =>
            onUpdate(field.path, (e.currentTarget as HTMLInputElement).checked, 'immediate')}
        />
      </div>
      {#if field.description && showDescriptions}
        <p class="text-[11px] opacity-60 leading-snug mt-0.5 break-words">{field.description}</p>
      {/if}
    </div>
  {:else if field.type === 'enum'}
    <div class="w-full min-w-0">
      <div class="flex items-center gap-1 mb-1 min-w-0">
        <label
          class="text-xs font-medium leading-snug break-words min-w-0"
          for="param-{field.path}"
        >
          {field.label}
        </label>
        {#if field.description && !showDescriptions}
          <InfoTooltip text={field.description} label={field.label} />
        {/if}
      </div>
      <select
        id="param-{field.path}"
        class="select select-bordered select-xs w-full"
        value={currentValue === null || currentValue === undefined ? '' : String(currentValue)}
        onchange={(e) => {
          const v = (e.currentTarget as HTMLSelectElement).value;
          onUpdate(field.path, field.nullable && v === '' ? null : v, 'immediate');
        }}
      >
        {#if field.nullable}
          <option value="">Auto</option>
        {/if}
        {#each field.choices ?? [] as choice (choice)}
          <option value={choice}>{choice}</option>
        {/each}
      </select>
      {#if field.description && showDescriptions}
        <p class="text-[11px] opacity-60 leading-snug mt-1 break-words">{field.description}</p>
      {/if}
    </div>
  {:else if field.type === 'int' || field.type === 'float'}
    <div class="w-full min-w-0">
      <div
        class="flex flex-col @[240px]:flex-row @[240px]:items-center justify-between gap-1 @[240px]:gap-2 min-w-0"
      >
        <div class="flex items-center gap-1 min-w-0 flex-1">
          <label
            class="text-xs font-medium leading-snug break-words min-w-0"
            for="param-{field.path}"
          >
            {field.label}{#if field.unit}<span class="font-normal opacity-55 ml-1">({field.unit})</span>{/if}
          </label>
          {#if field.description && !showDescriptions}
            <InfoTooltip text={field.description} label={field.label} />
          {/if}
        </div>
        <div class="flex items-center gap-1 shrink-0 self-end @[240px]:self-auto">
          {#if field.nullable && numValue !== null}
            <button
              type="button"
              class="btn btn-xs btn-ghost h-7 w-5 min-h-0 px-0 text-xs opacity-60 hover:opacity-100"
              title="Reset to Auto"
              aria-label="Reset {field.label} to Auto"
              onclick={handleClearNullable}
            >
              ×
            </button>
          {/if}
          <div class="join items-center">
            <button
              type="button"
              class="btn btn-xs btn-ghost border border-base-300 join-item h-7 min-h-0 px-2 font-mono text-xs leading-none"
              aria-label="Decrease {field.label}"
              onclick={() => handleStep(-1)}
            >
              −
            </button>
            <input
              id="param-{field.path}"
              type="number"
              value={numValue === null ? '' : String(numValue)}
              placeholder={field.nullable ? 'Auto' : ''}
              step={stepVal}
              min={field.min}
              max={field.max}
              class="input input-bordered input-xs join-item h-7 w-16 text-center font-mono text-xs px-1.5 no-spinner"
              oninput={(e) => handleNumberInput((e.currentTarget as HTMLInputElement).value)}
            />
            <button
              type="button"
              class="btn btn-xs btn-ghost border border-base-300 join-item h-7 min-h-0 px-2 font-mono text-xs leading-none"
              aria-label="Increase {field.label}"
              onclick={() => handleStep(1)}
            >
              +
            </button>
          </div>
        </div>
      </div>
      {#if hasRange}
        <input
          type="range"
          min={field.min}
          max={field.max}
          step={stepVal}
          value={rangeValue}
          class="range range-primary range-xs w-full mt-1.5"
          class:opacity-40={field.nullable && numValue === null}
          onpointerdown={handleRangePointerDown}
          onkeydown={handleRangeKeyDown}
          oninput={(e) => handleRangeInput((e.currentTarget as HTMLInputElement).value)}
          onchange={(e) => handleRangeChange((e.currentTarget as HTMLInputElement).value)}
          onpointerup={(e) => handleRangePointerUp((e.currentTarget as HTMLInputElement).value)}
          onpointercancel={(e) => handleRangeCancel((e.currentTarget as HTMLInputElement).value)}
          onblur={(e) => handleRangeCancel((e.currentTarget as HTMLInputElement).value)}
        />
      {/if}
      {#if field.description && showDescriptions}
        <p class="text-[11px] opacity-60 mt-1 leading-snug break-words">{field.description}</p>
      {/if}
    </div>
  {:else}
    <div class="w-full min-w-0">
      <div class="flex items-center gap-1 mb-1 min-w-0">
        <label
          class="text-xs font-medium leading-snug break-words min-w-0"
          for="param-{field.path}"
        >
          {field.label}
        </label>
        {#if field.description && !showDescriptions}
          <InfoTooltip text={field.description} label={field.label} />
        {/if}
      </div>
      <input
        id="param-{field.path}"
        type="text"
        value={currentValue === null || currentValue === undefined
          ? ''
          : typeof currentValue === 'object'
            ? JSON.stringify(currentValue)
            : String(currentValue)}
        placeholder={field.nullable ? 'Auto' : ''}
        class="input input-bordered input-xs w-full"
        oninput={(e) => {
          const v = (e.currentTarget as HTMLInputElement).value;
          onUpdate(field.path, field.nullable && v === '' ? null : v, 'debounced');
        }}
      />
      {#if field.description && showDescriptions}
        <p class="text-[11px] opacity-60 mt-1 leading-snug break-words">{field.description}</p>
      {/if}
    </div>
  {/if}
{/if}
