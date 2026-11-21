<script lang="ts">
  import Self from './ParamField.svelte';
  import { evaluateWhen, getParamByPath, isPlainObject, stepNumber } from '../lib/params';
  import type { ParamField, ParamsRecord } from '../lib/types';

  interface Props {
    field: ParamField;
    rootParams: ParamsRecord;
    groupParams: ParamsRecord;
    onUpdate: (path: string, value: unknown, immediate?: boolean) => void;
  }

  let { field, rootParams, groupParams, onUpdate }: Props = $props();

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
        onUpdate(field.path, null, false);
      }
      return;
    }
    const parsed = isInt ? Math.round(Number(trimmed)) : Number(trimmed);
    if (!Number.isFinite(parsed)) return;
    onUpdate(field.path, parsed, false);
  }

  function handleStep(direction: -1 | 1) {
    const next = stepNumber(numValue, direction, stepVal, isInt, field.min, field.max);
    onUpdate(field.path, next, false);
  }

  function handleRangeInput(raw: string) {
    const parsed = isInt ? Math.round(Number(raw)) : Number(raw);
    if (!Number.isFinite(parsed)) return;
    onUpdate(field.path, parsed, false);
  }

  function handleClearNullable() {
    onUpdate(field.path, null, true);
  }

  function handleToggleGroupChange(checked: boolean) {
    if (!toggleChild) return;
    detailsOpen = checked;
    lastToggleState = checked;
    onUpdate(toggleChild.path, checked, true);
  }
</script>

{#if isVisible}
  {#if field.type === 'group'}
    <details
      class="collapse collapse-arrow bg-base-200/60 border border-base-300 rounded-box"
      bind:open={detailsOpen}
    >
      <summary
        class="collapse-title text-xs font-semibold min-h-0 py-2 pl-3 pr-9 flex items-center justify-between gap-2"
      >
        <span class="truncate">
          {field.label}
          {#if field.description}
            <span class="font-normal opacity-60 ml-1">— {field.description}</span>
          {/if}
        </span>
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
      </summary>
      {#if bodyChildren.length > 0}
        <div class="collapse-content px-3 pb-3 pt-1">
          <div
            class="space-y-2.5 pt-1.5 border-t border-base-300/70 transition-opacity"
            class:opacity-45={toggleChild !== null && !toggleChecked}
          >
            {#each bodyChildren as child (child.path)}
              <Self
                field={child}
                {rootParams}
                groupParams={childGroupParams}
                {onUpdate}
              />
            {/each}
          </div>
        </div>
      {/if}
    </details>
  {:else if field.type === 'bool' && field.nullable}
    <div class="flex items-center justify-between py-0.5 gap-2" title={field.description}>
      <span class="label-text text-xs flex flex-col min-w-0">
        <span class="truncate">{field.label}</span>
        {#if field.description}
          <span class="text-[10px] opacity-60 leading-snug">{field.description}</span>
        {/if}
      </span>
      <div class="join shrink-0" role="group" aria-label={field.label}>
        <button
          type="button"
          class="btn btn-xs join-item px-2"
          class:btn-primary={currentValue === null || currentValue === undefined}
          class:btn-ghost={currentValue !== null && currentValue !== undefined}
          class:border-base-300={currentValue !== null && currentValue !== undefined}
          onclick={() => onUpdate(field.path, null, true)}
        >
          Auto
        </button>
        <button
          type="button"
          class="btn btn-xs join-item px-2"
          class:btn-primary={currentValue === true}
          class:btn-ghost={currentValue !== true}
          class:border-base-300={currentValue !== true}
          onclick={() => onUpdate(field.path, true, true)}
        >
          On
        </button>
        <button
          type="button"
          class="btn btn-xs join-item px-2"
          class:btn-primary={currentValue === false}
          class:btn-ghost={currentValue !== false}
          class:border-base-300={currentValue !== false}
          onclick={() => onUpdate(field.path, false, true)}
        >
          Off
        </button>
      </div>
    </div>
  {:else if field.type === 'bool'}
    <label class="label cursor-pointer justify-between py-0.5 gap-2" title={field.description}>
      <span class="label-text text-xs flex flex-col min-w-0">
        <span>{field.label}</span>
        {#if field.description}
          <span class="text-[10px] opacity-60 leading-snug">{field.description}</span>
        {/if}
      </span>
      <input
        type="checkbox"
        class="toggle toggle-primary toggle-xs shrink-0"
        checked={Boolean(currentValue)}
        onchange={(e) => onUpdate(field.path, (e.currentTarget as HTMLInputElement).checked, true)}
      />
    </label>
  {:else if field.type === 'enum'}
    <div class="form-control w-full" title={field.description}>
      <label class="label py-0.5" for="param-{field.path}">
        <span class="label-text text-xs">{field.label}</span>
        {#if field.description}
          <span class="label-text-alt text-[10px] opacity-60 truncate max-w-44">{field.description}</span>
        {/if}
      </label>
      <select
        id="param-{field.path}"
        class="select select-bordered select-xs w-full"
        value={currentValue === null || currentValue === undefined ? '' : String(currentValue)}
        onchange={(e) => {
          const v = (e.currentTarget as HTMLSelectElement).value;
          onUpdate(field.path, field.nullable && v === '' ? null : v, true);
        }}
      >
        {#if field.nullable}
          <option value="">Auto</option>
        {/if}
        {#each field.choices ?? [] as choice (choice)}
          <option value={choice}>{choice}</option>
        {/each}
      </select>
    </div>
  {:else if field.type === 'int' || field.type === 'float'}
    <div class="form-control w-full" title={field.description}>
      <div class="flex items-center justify-between gap-2 mb-1">
        <label class="text-xs font-medium truncate" for="param-{field.path}">
          {field.label}{#if field.unit}<span class="font-normal opacity-55 ml-1">({field.unit})</span>{/if}
        </label>
        <div class="flex items-center gap-1 shrink-0">
          {#if field.nullable && numValue !== null}
            <button
              type="button"
              class="btn btn-xs btn-ghost px-1 text-[11px] opacity-60 hover:opacity-100"
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
              class="btn btn-sm btn-ghost border border-base-300 join-item px-2.5 font-mono text-sm leading-none"
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
              class="input input-bordered input-sm join-item w-20 text-center font-mono px-2 no-spinner"
              oninput={(e) => handleNumberInput((e.currentTarget as HTMLInputElement).value)}
            />
            <button
              type="button"
              class="btn btn-sm btn-ghost border border-base-300 join-item px-2.5 font-mono text-sm leading-none"
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
          class="range range-primary range-xs mt-0.5"
          class:opacity-40={field.nullable && numValue === null}
          oninput={(e) => handleRangeInput((e.currentTarget as HTMLInputElement).value)}
        />
      {/if}
      {#if field.description}
        <span class="text-[11px] opacity-60 mt-1 leading-snug">{field.description}</span>
      {/if}
    </div>
  {:else}
    <div class="form-control w-full" title={field.description}>
      <label class="label py-0.5" for="param-{field.path}">
        <span class="label-text text-xs">{field.label}</span>
      </label>
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
          onUpdate(field.path, field.nullable && v === '' ? null : v, false);
        }}
      />
      {#if field.description}
        <span class="text-[10px] opacity-55 mt-0.5 leading-tight">{field.description}</span>
      {/if}
    </div>
  {/if}
{/if}
