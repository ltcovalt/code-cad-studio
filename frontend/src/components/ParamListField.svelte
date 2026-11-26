<script lang="ts">
  import InfoTooltip from './InfoTooltip.svelte';
  import ParamField from './ParamField.svelte';
  import {
    deepClone,
    evaluateWhen,
    getParamByPath,
    isPlainObject,
    paramsEqual,
    prefixFieldPaths,
  } from '../lib/params';
  import type { ParamField as ParamFieldType, ParamsRecord, ParamUpdateMode } from '../lib/types';

  interface Props {
    field: ParamFieldType;
    rootParams: ParamsRecord;
    groupParams: ParamsRecord;
    presetDefaults: ParamsRecord;
    onUpdate: (path: string, value: unknown, mode?: ParamUpdateMode) => void;
    onPreviewCommit?: () => void;
  }

  let {
    field,
    rootParams,
    groupParams,
    presetDefaults,
    onUpdate,
    onPreviewCommit,
  }: Props = $props();

  const listUid = $props.id();

  let itemSchema = $derived(field.item!);
  let isStructured = $derived(itemSchema.type === 'group');
  let itemLabel = $derived(itemSchema.label || 'Item');

  let rawValue = $derived(getParamByPath(rootParams, field.path));
  let currentValue = $derived(rawValue !== undefined ? rawValue : field.default);
  let isAutoNull = $derived(
    Boolean(field.nullable && (currentValue === null || currentValue === undefined))
  );
  let items = $derived<unknown[]>(Array.isArray(currentValue) ? currentValue : []);

  let rawPresetDefault = $derived(getParamByPath(presetDefaults, field.path));
  let presetDefaultValue = $derived(
    rawPresetDefault !== undefined ? rawPresetDefault : field.default
  );
  let isModified = $derived(!paramsEqual(currentValue, presetDefaultValue));

  let resetActionLabel = $derived.by(() => {
    if (presetDefaultValue === null || presetDefaultValue === undefined) {
      return `Reset ${field.label} to Auto`;
    }
    if (Array.isArray(presetDefaultValue)) {
      const n = presetDefaultValue.length;
      return `Reset ${field.label} to ${n} ${n === 1 ? 'item' : 'items'}`;
    }
    return `Reset ${field.label}`;
  });

  // Client-only stable keys & disclosure state per list item (never serialized into params)
  let nextKey = 0;
  let itemKeys = $state<number[]>([]);
  let openKeys = $state<Record<number, boolean>>({});
  let initialized = false;
  let internalUpdatePending = false;
  let lastItemsRef: unknown[] | null = null;
  let lastPresetDefaultsRef: ParamsRecord | null = null;

  function resetKeysForCount(count: number) {
    const freshKeys = Array.from({ length: count }, () => ++nextKey);
    itemKeys = freshKeys;
    openKeys = count === 1 ? { [freshKeys[0]]: true } : {};
  }

  $effect(() => {
    const currentItems = items;
    const currentPresetDefaults = presetDefaults;
    const count = currentItems.length;

    if (!initialized) {
      initialized = true;
      lastItemsRef = currentItems;
      lastPresetDefaultsRef = currentPresetDefaults;
      resetKeysForCount(count);
      return;
    }

    const presetChanged = currentPresetDefaults !== lastPresetDefaultsRef;
    const itemsReplacedExternally = !internalUpdatePending && currentItems !== lastItemsRef;
    internalUpdatePending = false;
    lastItemsRef = currentItems;
    lastPresetDefaultsRef = currentPresetDefaults;

    if (presetChanged || itemsReplacedExternally) {
      resetKeysForCount(count);
      return;
    }

    if (itemKeys.length !== count) {
      const next: number[] = [];
      for (let i = 0; i < count; i++) {
        next.push(itemKeys[i] ?? ++nextKey);
      }
      const keep = new Set(next);
      const prunedOpen: Record<number, boolean> = {};
      for (const [k, v] of Object.entries(openKeys)) {
        const numKey = Number(k);
        if (keep.has(numKey) && v) {
          prunedOpen[numKey] = true;
        }
      }
      itemKeys = next;
      openKeys = prunedOpen;
    }
  });

  function forwardUpdate(path: string, value: unknown, mode?: ParamUpdateMode) {
    internalUpdatePending = true;
    onUpdate(path, value, mode);
  }

  function ensureKeysForCurrentItems(): number[] {
    return items.map((_, i) => itemKeys[i] ?? ++nextKey);
  }

  function handleAddItem() {
    internalUpdatePending = true;
    const baseKeys = ensureKeysForCurrentItems();
    const key = ++nextKey;
    itemKeys = [...baseKeys, key];
    openKeys = { ...openKeys, [key]: true };
    const nextItem = deepClone(itemSchema.default);
    onUpdate(field.path, [...items, nextItem], 'immediate');
  }

  function handleDuplicateItem(index: number) {
    internalUpdatePending = true;
    const baseKeys = ensureKeysForCurrentItems();
    const key = ++nextKey;
    itemKeys = [...baseKeys.slice(0, index + 1), key, ...baseKeys.slice(index + 1)];
    openKeys = { ...openKeys, [key]: true };
    const cloned = deepClone(items[index]);
    const nextItems = [...items.slice(0, index + 1), cloned, ...items.slice(index + 1)];
    onUpdate(field.path, nextItems, 'immediate');
  }

  function handleRemoveItem(index: number) {
    internalUpdatePending = true;
    const baseKeys = ensureKeysForCurrentItems();
    const removedKey = baseKeys[index];
    itemKeys = baseKeys.filter((_, i) => i !== index);
    const nextOpen = { ...openKeys };
    delete nextOpen[removedKey];
    openKeys = nextOpen;
    const nextItems = items.filter((_, i) => i !== index);
    onUpdate(field.path, nextItems, 'immediate');
  }

  function handleSetAuto() {
    internalUpdatePending = true;
    itemKeys = [];
    openKeys = {};
    onUpdate(field.path, null, 'immediate');
  }

  function handleResetList() {
    const next = deepClone(presetDefaultValue);
    resetKeysForCount(Array.isArray(next) ? next.length : 0);
    internalUpdatePending = false;
    onUpdate(field.path, next, 'immediate');
  }

  function toggleItemOpen(key: number) {
    openKeys = { ...openKeys, [key]: !openKeys[key] };
  }

  const PRIORITY_NUM_NAMES = new Set([
    'length',
    'width',
    'height',
    'diameter',
    'radius',
    'size',
    'depth',
    'offset',
  ]);

  function summarizeItem(item: unknown, children: ParamFieldType[]): string {
    if (!isPlainObject(item)) return '';

    for (const keyName of ['name', 'label', 'title', 'id']) {
      const val = item[keyName];
      if (typeof val === 'string' && val.trim() !== '') {
        return val.trim();
      }
    }

    const visibleChildren = children.filter((c) => evaluateWhen(c.when, rootParams, item));
    const enumTokens: string[] = [];
    const priorityNumTokens: string[] = [];
    const otherNumTokens: string[] = [];

    for (const child of visibleChildren) {
      const raw = item[child.name];
      const val = raw !== undefined ? raw : child.default;
      if (val === null || val === undefined || val === '') continue;

      if (child.type === 'enum' && enumTokens.length < 2) {
        enumTokens.push(String(val));
      } else if ((child.type === 'int' || child.type === 'float') && typeof val === 'number' && val !== 0) {
        const formatted = child.unit ? `${val} ${child.unit}` : String(val);
        if (PRIORITY_NUM_NAMES.has(child.name)) {
          priorityNumTokens.push(formatted);
        } else {
          otherNumTokens.push(formatted);
        }
      }
    }

    const tokens = [...enumTokens, ...priorityNumTokens, ...otherNumTokens].slice(0, 3);
    return tokens.join(' · ');
  }

  function scopedChildrenFor(index: number): ParamFieldType[] {
    const prefix = `${field.path}.${index}`;
    return (itemSchema.children ?? []).map((child) => prefixFieldPaths(child, prefix));
  }

  function scopedScalarFieldFor(index: number): ParamFieldType {
    return {
      ...itemSchema,
      path: `${field.path}.${index}`,
      label: `${itemLabel} ${index + 1}`,
    };
  }
</script>

<div class="w-full min-w-0 space-y-2">
  <!-- Header row -->
  <div class="flex items-center justify-between gap-2 min-w-0">
    <div class="flex items-center gap-1 min-w-0 flex-1">
      <span class="text-xs font-medium leading-snug break-words min-w-0">{field.label}</span>
      {#if !isAutoNull}
        <span class="badge badge-xs badge-ghost font-mono opacity-70 shrink-0">{items.length}</span>
      {/if}
      {#if field.description}
        <InfoTooltip text={field.description} label={field.label} />
      {/if}
      {#if isModified}
        <button
          type="button"
          class="inline-flex items-center justify-center shrink-0 rounded-full p-0.5 text-primary hover:bg-primary/15 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary transition-colors cursor-pointer"
          title={resetActionLabel}
          aria-label={resetActionLabel}
          onclick={handleResetList}
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
            <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" />
            <path d="M3 3v5h5" />
          </svg>
        </button>
      {/if}
    </div>

    <div class="flex items-center gap-1 shrink-0">
      {#if field.nullable && !isAutoNull}
        <button
          type="button"
          class="btn btn-xs btn-ghost border border-base-300 h-6 min-h-6 px-1.5 text-[11px] opacity-75 hover:opacity-100"
          title="Reset {field.label} to Auto"
          aria-label="Auto — reset {field.label} to automatic"
          onclick={handleSetAuto}
        >
          Auto
        </button>
      {/if}
      <button
        type="button"
        class="btn btn-xs btn-ghost border border-base-300 h-6 min-h-6 px-2 gap-1 text-[11px]"
        title="Add {itemLabel}"
        aria-label="Add {itemLabel}"
        onclick={handleAddItem}
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-3 w-3"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2.4"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M12 5v14" />
          <path d="M5 12h14" />
        </svg>
        <span>Add</span>
      </button>
    </div>
  </div>

  <!-- Body: Auto / Empty / List of Items -->
  {#if isAutoNull}
    <div
      class="flex items-center justify-between gap-2 rounded-lg border border-dashed border-base-300 bg-base-100/40 px-2.5 py-2 text-[11px] opacity-70"
    >
      <span class="truncate">Auto (calculated)</span>
    </div>
  {:else if items.length === 0}
    <div
      class="rounded-lg border border-dashed border-base-300 bg-base-100/40 px-3 py-2.5 text-center text-[11px] opacity-65"
    >
      No items configured. Click <span class="font-medium">Add</span> to create a {itemLabel.toLowerCase()}.
    </div>
  {:else if isStructured}
    <ul role="list" class="space-y-2 list-none p-0 m-0">
      {#each items as item, index (itemKeys[index] ?? -(index + 1))}
        {@const key = itemKeys[index] ?? -(index + 1)}
        {@const isOpen = Boolean(openKeys[key])}
        {@const panelId = `${listUid}-item-${key}`}
        {@const itemObj = isPlainObject(item) ? item : {}}
        {@const summaryText = summarizeItem(itemObj, itemSchema.children ?? [])}
        {@const scopedChildren = scopedChildrenFor(index)}
        <li class="bg-base-100/75 border border-base-300 rounded-lg min-w-0 overflow-hidden">
          <div class="flex items-center gap-1 px-2 py-1.5 min-w-0">
            <button
              type="button"
              class="flex-1 flex items-center gap-1.5 text-left min-w-0 min-h-6 rounded px-0.5 hover:text-primary focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary cursor-pointer transition-colors"
              aria-expanded={isOpen}
              aria-controls={panelId}
              onclick={() => toggleItemOpen(key)}
            >
              <svg
                xmlns="http://www.w3.org/2000/svg"
                class="h-3 w-3 shrink-0 opacity-65 transition-transform duration-150 motion-reduce:transition-none {isOpen
                  ? 'rotate-0'
                  : '-rotate-90'}"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                stroke-width="2.4"
                stroke-linecap="round"
                stroke-linejoin="round"
                aria-hidden="true"
              >
                <path d="m6 9 6 6 6-6" />
              </svg>
              <span class="text-xs font-semibold leading-tight shrink-0">
                {itemLabel} {index + 1}
              </span>
              {#if summaryText}
                <span class="text-[11px] opacity-55 truncate font-mono">{summaryText}</span>
              {/if}
            </button>

            <div class="flex items-center gap-0.5 shrink-0">
              <button
                type="button"
                class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-6 min-w-6 opacity-60 hover:opacity-100"
                title="Duplicate {itemLabel} {index + 1}"
                aria-label="Duplicate {itemLabel} {index + 1}"
                onclick={() => handleDuplicateItem(index)}
              >
                <svg
                  xmlns="http://www.w3.org/2000/svg"
                  class="h-3 w-3"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  aria-hidden="true"
                >
                  <rect width="14" height="14" x="8" y="8" rx="2" ry="2" />
                  <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
                </svg>
              </button>
              <button
                type="button"
                class="btn btn-ghost btn-xs btn-square h-6 w-6 min-h-6 min-w-6 opacity-60 hover:opacity-100 hover:text-error"
                title="Remove {itemLabel} {index + 1}"
                aria-label="Remove {itemLabel} {index + 1}"
                onclick={() => handleRemoveItem(index)}
              >
                <svg
                  xmlns="http://www.w3.org/2000/svg"
                  class="h-3 w-3"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="2"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  aria-hidden="true"
                >
                  <path d="M3 6h18" />
                  <path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6" />
                  <path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" />
                </svg>
              </button>
            </div>
          </div>

          {#if isOpen}
            <div
              id={panelId}
              class="px-2.5 pb-2.5 pt-2 border-t border-base-300/70 space-y-2.5 min-w-0"
            >
              {#each scopedChildren as child (child.path)}
                <ParamField
                  field={child}
                  {rootParams}
                  groupParams={itemObj}
                  {presetDefaults}
                  onUpdate={forwardUpdate}
                  {onPreviewCommit}
                />
              {/each}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {:else}
    <ul role="list" class="space-y-1.5 list-none p-0 m-0">
      {#each items as _, index (itemKeys[index] ?? -(index + 1))}
        {@const scalarField = scopedScalarFieldFor(index)}
        <li class="flex items-start gap-1.5 min-w-0">
          <div class="flex-1 min-w-0">
            <ParamField
              field={scalarField}
              {rootParams}
              {groupParams}
              {presetDefaults}
              onUpdate={forwardUpdate}
              {onPreviewCommit}
            />
          </div>
          <button
            type="button"
            class="btn btn-ghost btn-xs btn-square h-7 w-7 min-h-7 min-w-7 shrink-0 opacity-60 hover:opacity-100 hover:text-error"
            title="Remove {itemLabel} {index + 1}"
            aria-label="Remove {itemLabel} {index + 1}"
            onclick={() => handleRemoveItem(index)}
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-3 w-3"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="M3 6h18" />
              <path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6" />
              <path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2" />
            </svg>
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</div>
