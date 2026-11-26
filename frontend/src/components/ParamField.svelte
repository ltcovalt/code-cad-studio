<script lang="ts">
  import InfoTooltip from './InfoTooltip.svelte';
  import ParamListField from './ParamListField.svelte';
  import Self from './ParamField.svelte';
  import {
    deepClone,
    evaluateWhen,
    getGroupDefaultsForField,
    getParamByPath,
    isPlainObject,
    paramsEqual,
    resolveComponentPresetParams,
    setParamByPath,
    stepNumber,
  } from '../lib/params';
  import type {
    ComponentPresetBundle,
    ParamField,
    ParamsRecord,
    ParamUpdateMode,
  } from '../lib/types';

  interface Props {
    field: ParamField;
    rootParams: ParamsRecord;
    groupParams: ParamsRecord;
    presetDefaults: ParamsRecord;
    components?: Record<string, ComponentPresetBundle>;
    selectedComponentPresets?: Record<string, string>;
    presetComponentPresets?: Record<string, string>;
    onUpdate: (path: string, value: unknown, mode?: ParamUpdateMode) => void;
    onPreviewCommit?: () => void;
    onToggleNullableGroup?: (path: string, field: ParamField, enabled: boolean) => void;
    onSelectComponentPreset?: (path: string, componentId: string, presetKey: string) => void;
    onResetComponentGroup?: (path: string, componentId: string) => void;
    onOpenComponent?: (componentId: string) => void;
  }

  let {
    field,
    rootParams,
    groupParams,
    presetDefaults,
    components = {},
    selectedComponentPresets = {},
    presetComponentPresets = {},
    onUpdate,
    onPreviewCommit,
    onToggleNullableGroup,
    onSelectComponentPreset,
    onResetComponentGroup,
    onOpenComponent,
  }: Props = $props();

  let isVisible = $derived(evaluateWhen(field.when, rootParams, groupParams));

  // For group fields
  let toggleChild = $derived(
    field.type === 'group' && !field.nullable
      ? (field.children?.find((c) => c.name === 'enabled' && c.type === 'bool' && !c.nullable) ??
          null)
      : null
  );
  let isNullableGroup = $derived(field.type === 'group' && Boolean(field.nullable));
  let isToggleableGroup = $derived(toggleChild !== null || isNullableGroup);

  let bodyChildren = $derived(
    field.type === 'group'
      ? (field.children ?? []).filter((c) => !(toggleChild && c.name === toggleChild.name))
      : []
  );

  let rawCurrentValue = $derived(getParamByPath(rootParams, field.path));
  let currentValue = $derived(rawCurrentValue !== undefined ? rawCurrentValue : field.default);
  let rawPresetDefaultValue = $derived(getParamByPath(presetDefaults, field.path));
  let presetDefaultValue = $derived(
    rawPresetDefaultValue !== undefined ? rawPresetDefaultValue : field.default
  );
  let isModified = $derived(
    field.type !== 'group' && !paramsEqual(currentValue, presetDefaultValue)
  );

  let childGroupParams = $derived<ParamsRecord>(
    isPlainObject(currentValue) ? currentValue : getGroupDefaultsForField(field)
  );

  let toggleChecked = $derived(
    toggleChild
      ? Boolean(getParamByPath(rootParams, toggleChild.path))
      : isNullableGroup
        ? currentValue !== null && currentValue !== undefined
        : true
  );
  let toggleDefaultValue = $derived(
    toggleChild
      ? Boolean(getParamByPath(presetDefaults, toggleChild.path))
      : isNullableGroup
        ? presetDefaultValue !== null && presetDefaultValue !== undefined
        : true
  );
  let isToggleModified = $derived(isToggleableGroup && toggleChecked !== toggleDefaultValue);

  // Component-backed group state
  let componentId = $derived(field.type === 'group' ? (field.component_id ?? '') : '');
  let componentBundle = $derived<ComponentPresetBundle | null>(
    componentId && components[componentId] ? components[componentId] : null
  );
  let activeComponentPresetKey = $derived(
    componentId ? (selectedComponentPresets[field.path] || 'default') : ''
  );
  let savedComponentPresetKey = $derived(
    componentId ? (presetComponentPresets[field.path] || 'default') : ''
  );
  let activeComponentPreset = $derived(
    componentBundle?.presets.find((p) => p.key === activeComponentPresetKey) ?? null
  );
  let isMissingComponentPreset = $derived(
    Boolean(
      componentId &&
        componentBundle &&
        activeComponentPresetKey &&
        activeComponentPresetKey !== 'default' &&
        !activeComponentPreset
    )
  );
  let componentPresetBaseline = $derived.by<ParamsRecord | null>(() => {
    if (!componentId) return null;
    return resolveComponentPresetParams(
      componentId,
      activeComponentPresetKey,
      getGroupDefaultsForField(field),
      components
    );
  });
  let isComponentGroupOverridden = $derived(
    Boolean(
      componentId &&
        toggleChecked &&
        isPlainObject(currentValue) &&
        componentPresetBaseline !== null &&
        !paramsEqual(currentValue, componentPresetBaseline)
    )
  );
  let isComponentPresetRefModified = $derived(
    Boolean(componentId && toggleChecked && activeComponentPresetKey !== savedComponentPresetKey)
  );
  let childPresetDefaults = $derived.by<ParamsRecord>(() => {
    if (componentId && componentPresetBaseline !== null) {
      return setParamByPath(presetDefaults, field.path, componentPresetBaseline);
    }
    return presetDefaults;
  });

  function formatComponentPresetOption(p: {
    key: string;
    label: string;
    source: string;
    verification?: { status: string } | null;
  }): string {
    const status = p.verification?.status;
    const prefix =
      status === 'verified'
        ? '✓ '
        : status === 'usable'
          ? '~ '
          : status === 'unusable'
            ? '! '
            : '';
    const suffix =
      p.key === activeComponentPresetKey && isComponentGroupOverridden ? ' (modified)' : '';
    return `${prefix}${p.label}${suffix}`;
  }

  function formatValueForLabel(value: unknown, targetField: ParamField): string {
    if (value === null || value === undefined) return 'Auto';
    if (targetField.type === 'list' && Array.isArray(value)) {
      return `${value.length} ${value.length === 1 ? 'item' : 'items'}`;
    }
    if (targetField.type === 'bool') return value ? 'On' : 'Off';
    if (targetField.type === 'int' || targetField.type === 'float') {
      return targetField.unit ? `${value} ${targetField.unit}` : String(value);
    }
    if (typeof value === 'string') {
      return value === '' ? 'empty' : `"${value}"`;
    }
    if (typeof value === 'object') {
      return JSON.stringify(value);
    }
    return String(value);
  }

  let resetActionLabel = $derived(
    `Reset ${field.label} to ${formatValueForLabel(presetDefaultValue, field)}`
  );
  let toggleResetActionLabel = $derived(
    `Reset Enable ${field.label} to ${toggleDefaultValue ? 'On' : 'Off'}`
  );

  let detailsOpen = $state(true);
  let lastToggleState = $state<boolean | null>(null);

  $effect(() => {
    if (field.type === 'group' && isToggleableGroup) {
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

  function commitNormalizedNumber(
    input: HTMLInputElement,
    mode: ParamUpdateMode,
    forceImmediate = false
  ) {
    const trimmed = input.value.trim();
    if (trimmed === '') {
      if (input.validity && !input.validity.valid) {
        input.value = numValue === null ? '' : String(numValue);
        return;
      }
      if (field.nullable) {
        if (numValue !== null || forceImmediate) {
          onUpdate(field.path, null, mode);
        }
        input.value = '';
      } else {
        input.value = numValue === null ? '' : String(numValue);
      }
      return;
    }
    let parsed = isInt ? Math.round(Number(trimmed)) : Number(trimmed);
    if (!Number.isFinite(parsed)) {
      input.value = numValue === null ? '' : String(numValue);
      return;
    }
    if (field.min !== undefined && Number.isFinite(field.min)) {
      parsed = Math.max(field.min, parsed);
    }
    if (field.max !== undefined && Number.isFinite(field.max)) {
      parsed = Math.min(field.max, parsed);
    }
    input.value = String(parsed);
    if (parsed !== numValue || forceImmediate) {
      onUpdate(field.path, parsed, mode);
    }
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
    if (toggleChild) {
      detailsOpen = checked;
      lastToggleState = checked;
      onUpdate(toggleChild.path, checked, 'immediate');
      return;
    }
    if (isNullableGroup) {
      detailsOpen = checked;
      lastToggleState = checked;
      onToggleNullableGroup?.(field.path, field, checked);
    }
  }

  function handleResetField() {
    onUpdate(field.path, deepClone(presetDefaultValue), 'immediate');
  }

  function handleResetToggle(e: MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    handleToggleGroupChange(toggleDefaultValue);
  }
</script>

{#snippet resetIconButton(actionLabel: string, onReset: (e: MouseEvent) => void)}
  <button
    type="button"
    class="inline-flex items-center justify-center shrink-0 rounded-full p-0.5 text-primary hover:bg-primary/15 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-primary transition-colors cursor-pointer"
    title={actionLabel}
    aria-label={actionLabel}
    onclick={onReset}
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
{/snippet}

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
            <div class="flex items-center gap-1.5 min-w-0">
              <span class="text-xs font-semibold leading-tight break-words">{field.label}</span>
              {#if componentId && onOpenComponent}
                <!-- svelte-ignore a11y_click_events_have_key_events -->
                <!-- svelte-ignore a11y_no_static_element_interactions -->
                <span class="shrink-0 inline-flex items-center" onclick={(e) => e.stopPropagation()}>
                  <button
                    type="button"
                    class="inline-flex items-center gap-0.5 rounded px-1 py-0.5 text-[10px] font-medium text-secondary/90 hover:text-secondary hover:bg-secondary/10 transition-colors cursor-pointer"
                    title="Open {field.component_name || field.label} component"
                    aria-label="Open {field.component_name || field.label} component"
                    onclick={() => onOpenComponent(componentId)}
                  >
                    <svg
                      xmlns="http://www.w3.org/2000/svg"
                      class="h-2.5 w-2.5 shrink-0"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="2"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      aria-hidden="true"
                    >
                      <path d="M5.5 8.5 9 12l-3.5 3.5L2 12l3.5-3.5Z" />
                      <path d="m12 2 3.5 3.5L12 9 8.5 5.5 12 2Z" />
                      <path d="M18.5 8.5 22 12l-3.5 3.5L15 12l3.5-3.5Z" />
                      <path d="m12 15 3.5 3.5L12 22l-3.5-3.5L12 15Z" />
                    </svg>
                    <span>Component</span>
                  </button>
                </span>
              {/if}
            </div>
            {#if field.description}
              <div class="text-[11px] font-normal opacity-60 leading-snug mt-0.5 break-words">
                {field.description}
              </div>
            {/if}
          </div>
          {#if isToggleableGroup}
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <span class="shrink-0 flex items-center gap-1.5" onclick={(e) => e.stopPropagation()}>
              {#if isToggleModified}
                {@render resetIconButton(toggleResetActionLabel, handleResetToggle)}
              {/if}
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
      {#if bodyChildren.length > 0 || componentBundle}
        <div class="collapse-content px-3 pb-3 pt-1 min-w-0">
          <div
            class="space-y-2.5 pt-2 border-t border-base-300/70 transition-opacity min-w-0"
            class:opacity-45={isToggleableGroup && !toggleChecked}
            inert={isToggleableGroup && !toggleChecked}
          >
            {#if componentId && componentBundle}
              <div class="space-y-1.5 pb-2 border-b border-base-300/60">
                <div class="flex items-center gap-1.5 min-w-0">
                  <label
                    for="comp-preset-{field.path}"
                    class="text-[11px] font-medium opacity-75 shrink-0"
                  >
                    Preset
                  </label>
                  <select
                    id="comp-preset-{field.path}"
                    class="select select-bordered select-xs flex-1 min-w-0"
                    value={activeComponentPresetKey}
                    onchange={(e) =>
                      onSelectComponentPreset?.(
                        field.path,
                        componentId,
                        (e.currentTarget as HTMLSelectElement).value
                      )}
                  >
                    {#if isMissingComponentPreset}
                      <option value={activeComponentPresetKey}>
                        {activeComponentPresetKey} (missing)
                      </option>
                    {/if}
                    {#each componentBundle.presets as cp (cp.key)}
                      <option value={cp.key}>{formatComponentPresetOption(cp)}</option>
                    {/each}
                  </select>
                  {#if isComponentGroupOverridden}
                    {@render resetIconButton(
                      `Reset ${field.label} overrides to ${activeComponentPreset?.label ?? 'Base'}`,
                      () => onResetComponentGroup?.(field.path, componentId)
                    )}
                  {:else if isComponentPresetRefModified}
                    {@render resetIconButton(
                      `Reset ${field.label} preset to ${componentBundle.presets.find((p) => p.key === savedComponentPresetKey)?.label ?? savedComponentPresetKey}`,
                      () => onSelectComponentPreset?.(field.path, componentId, savedComponentPresetKey)
                    )}
                  {/if}
                </div>
                {#if isMissingComponentPreset}
                  <div class="text-[11px] text-warning flex items-center gap-1">
                    <span>Preset "{activeComponentPresetKey}" not found — using defaults.</span>
                  </div>
                {/if}
                {#if componentBundle.presets_error}
                  <div class="text-[11px] text-warning break-words">
                    {componentBundle.presets_error}
                  </div>
                {/if}
              </div>
            {/if}

            {#each bodyChildren as child (child.path)}
              <Self
                field={child}
                {rootParams}
                groupParams={childGroupParams}
                presetDefaults={childPresetDefaults}
                {components}
                {selectedComponentPresets}
                {presetComponentPresets}
                {onUpdate}
                {onPreviewCommit}
                {onToggleNullableGroup}
                {onSelectComponentPreset}
                {onResetComponentGroup}
                {onOpenComponent}
              />
            {/each}
          </div>
        </div>
      {/if}
    </details>
  {:else if field.type === 'list' && field.item}
    <ParamListField
      {field}
      {rootParams}
      {groupParams}
      {presetDefaults}
      {onUpdate}
      {onPreviewCommit}
    />
  {:else if field.type === 'bool' && field.nullable}
    <div class="w-full min-w-0 py-0.5">
      <div
        class="flex flex-col @[240px]:flex-row @[240px]:items-center justify-between gap-1.5 @[240px]:gap-2 min-w-0"
      >
        <div class="flex items-center gap-1 min-w-0 flex-1">
          <span class="text-xs font-medium leading-snug break-words min-w-0">{field.label}</span>
          {#if field.description}
            <InfoTooltip text={field.description} label={field.label} />
          {/if}
          {#if isModified}
            {@render resetIconButton(resetActionLabel, handleResetField)}
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
          {#if field.description}
            <InfoTooltip text={field.description} label={field.label} />
          {/if}
          {#if isModified}
            {@render resetIconButton(resetActionLabel, handleResetField)}
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
    </div>
  {:else if field.type === 'enum'}
    <div class="w-full min-w-0">
      <div class="flex flex-wrap items-center justify-between gap-x-2 gap-y-1 min-w-0">
        <div class="flex items-center gap-1 min-w-0 max-w-full flex-auto">
          <label
            class="text-xs font-medium leading-snug break-words min-w-0"
            for="param-{field.path}"
          >
            {field.label}
          </label>
          {#if field.description}
            <InfoTooltip text={field.description} label={field.label} />
          {/if}
          {#if isModified}
            {@render resetIconButton(resetActionLabel, handleResetField)}
          {/if}
        </div>
        <select
          id="param-{field.path}"
          class="select select-bordered select-xs param-select"
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
      </div>
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
          {#if field.description}
            <InfoTooltip text={field.description} label={field.label} />
          {/if}
          {#if isModified}
            {@render resetIconButton(resetActionLabel, handleResetField)}
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
          <div class="join items-center w-28">
            <button
              type="button"
              class="btn btn-xs btn-ghost border border-base-300 join-item h-7 w-6 min-h-0 px-0 shrink-0 font-mono text-xs leading-none"
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
              class="input input-bordered input-xs join-item h-7 flex-1 min-w-0 text-center font-mono text-xs px-1.5 no-spinner"
              oninput={(e) => handleNumberInput((e.currentTarget as HTMLInputElement).value)}
              onblur={(e) => commitNormalizedNumber(e.currentTarget as HTMLInputElement, 'immediate')}
              onkeydown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  const el = e.currentTarget as HTMLInputElement;
                  commitNormalizedNumber(el, 'immediate', true);
                  el.blur();
                }
              }}
            />
            <button
              type="button"
              class="btn btn-xs btn-ghost border border-base-300 join-item h-7 w-6 min-h-0 px-0 shrink-0 font-mono text-xs leading-none"
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
          class="range range-primary range-xs param-range w-full mt-1"
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
        {#if field.description}
          <InfoTooltip text={field.description} label={field.label} />
        {/if}
        {#if isModified}
          {@render resetIconButton(resetActionLabel, handleResetField)}
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
    </div>
  {/if}
{/if}
