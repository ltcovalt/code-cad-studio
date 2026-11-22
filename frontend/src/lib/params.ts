import type { ParamField, ParamsRecord } from './types';

export function isPlainObject(value: unknown): value is ParamsRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function paramsEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if ((a === null || a === undefined) && (b === null || b === undefined)) return true;
  if (typeof a === 'number' && typeof b === 'number' && Number.isNaN(a) && Number.isNaN(b)) {
    return true;
  }
  if (Array.isArray(a) && Array.isArray(b)) {
    if (a.length !== b.length) return false;
    return a.every((item, i) => paramsEqual(item, b[i]));
  }
  if (isPlainObject(a) && isPlainObject(b)) {
    const keysA = Object.keys(a);
    const keysB = Object.keys(b);
    if (keysA.length !== keysB.length) return false;
    return keysA.every((k) => k in b && paramsEqual(a[k], b[k]));
  }
  return false;
}

export function deepClone<T>(value: T): T {
  if (Array.isArray(value)) {
    return value.map((item) => deepClone(item)) as unknown as T;
  }
  if (isPlainObject(value)) {
    const out: ParamsRecord = {};
    for (const [k, v] of Object.entries(value)) {
      out[k] = deepClone(v);
    }
    return out as unknown as T;
  }
  return value;
}

/**
 * Overlay existing user edits or sparse preset overrides onto canonical defaults.
 * Preserves null values for Optional[T] fields and drops keys removed from schema.
 */
export function mergeWithDefaults(defaults: ParamsRecord, overrides: ParamsRecord): ParamsRecord {
  const merged = deepClone(defaults);
  for (const [key, val] of Object.entries(overrides)) {
    if (!(key in merged)) continue;
    const defVal = merged[key];
    if (isPlainObject(defVal) && isPlainObject(val)) {
      merged[key] = mergeWithDefaults(defVal, val);
    } else {
      merged[key] = deepClone(val);
    }
  }
  return merged;
}

function parseArrayIndex(part: string): number | null {
  if (!/^\d+$/.test(part)) return null;
  const idx = Number(part);
  return Number.isSafeInteger(idx) ? idx : null;
}

export function getParamByPath(root: ParamsRecord, path: string): unknown {
  const parts = path.split('.');
  let curr: unknown = root;
  for (const part of parts) {
    if (Array.isArray(curr)) {
      const idx = parseArrayIndex(part);
      if (idx === null || idx < 0 || idx >= curr.length) return undefined;
      curr = curr[idx];
    } else if (isPlainObject(curr)) {
      curr = curr[part];
    } else {
      return undefined;
    }
  }
  return curr;
}

export function setParamByPath(root: ParamsRecord, path: string, value: unknown): ParamsRecord {
  const parts = path.split('.');
  const next: ParamsRecord = { ...root };
  let curr: ParamsRecord | unknown[] = next;
  for (let i = 0; i < parts.length - 1; i++) {
    const part = parts[i];
    const nextIsIndex = parseArrayIndex(parts[i + 1]) !== null;

    if (Array.isArray(curr)) {
      const idx = parseArrayIndex(part);
      if (idx === null) return next;
      const existing: unknown = curr[idx];
      const clonedChild: ParamsRecord | unknown[] = nextIsIndex
        ? Array.isArray(existing)
          ? [...existing]
          : []
        : isPlainObject(existing)
          ? { ...existing }
          : {};
      curr[idx] = clonedChild;
      curr = clonedChild;
    } else {
      const existing: unknown = curr[part];
      const clonedChild: ParamsRecord | unknown[] = nextIsIndex
        ? Array.isArray(existing)
          ? [...existing]
          : []
        : isPlainObject(existing)
          ? { ...existing }
          : {};
      curr[part] = clonedChild;
      curr = clonedChild;
    }
  }
  const lastPart = parts[parts.length - 1];
  if (Array.isArray(curr)) {
    const idx = parseArrayIndex(lastPart);
    if (idx !== null) {
      curr[idx] = value;
    }
  } else {
    curr[lastPart] = value;
  }
  return next;
}

/**
 * Prefix a relative ParamField schema (e.g. a list item's child field) with an
 * item-scoped path such as "cutouts.items.0". Nested list `item` templates remain
 * relative so deeper lists prefix themselves when rendered.
 */
export function prefixFieldPaths(field: ParamField, prefix: string): ParamField {
  const nextPath = field.path ? `${prefix}.${field.path}` : prefix;
  return {
    ...field,
    path: nextPath,
    children: field.children?.map((child) => prefixFieldPaths(child, prefix)),
  };
}

/**
 * Fill missing keys inside list-of-dataclass items using each list field's
 * canonical item default so sparse presets always render complete item forms.
 */
export function normalizeParamsWithFields(
  params: ParamsRecord,
  fields: ParamField[]
): ParamsRecord {
  const out: ParamsRecord = { ...params };
  for (const f of fields) {
    const val = out[f.name];
    if (f.type === 'group' && f.children && isPlainObject(val)) {
      out[f.name] = normalizeParamsWithFields(val, f.children);
    } else if (
      f.type === 'list' &&
      f.item?.type === 'group' &&
      isPlainObject(f.item.default) &&
      Array.isArray(val)
    ) {
      const itemDefaults = f.item.default;
      const itemChildren = f.item.children ?? [];
      out[f.name] = val.map((el) => {
        const merged = isPlainObject(el)
          ? mergeWithDefaults(itemDefaults, el)
          : deepClone(itemDefaults);
        return normalizeParamsWithFields(merged, itemChildren);
      });
    }
  }
  return out;
}

/**
 * Evaluate conditional visibility (`metadata={"when": {...}}`).
 * - Keys containing `.` resolve from root params (e.g. "core.shape").
 * - Bare keys resolve from the current group object (e.g. "style").
 * - Expected value matches by strict equality or array membership.
 */
export function evaluateWhen(
  when: Record<string, unknown> | undefined,
  rootParams: ParamsRecord,
  groupParams: ParamsRecord
): boolean {
  if (!when) return true;
  for (const [key, expected] of Object.entries(when)) {
    const actual = key.includes('.') ? getParamByPath(rootParams, key) : groupParams[key];
    if (Array.isArray(expected)) {
      if (!expected.includes(actual)) return false;
    } else if (actual !== expected) {
      return false;
    }
  }
  return true;
}

export function splitFields(fields: ParamField[]): {
  coreFields: ParamField[];
  nestedGroups: ParamField[];
} {
  const coreFields: ParamField[] = [];
  const nestedGroups: ParamField[] = [];
  for (const f of fields) {
    if (f.type === 'group') {
      nestedGroups.push(f);
    } else {
      coreFields.push(f);
    }
  }
  return { coreFields, nestedGroups };
}

export function stepNumber(
  current: number | null,
  direction: -1 | 1,
  step: number,
  isInt: boolean,
  min?: number,
  max?: number
): number {
  const effectiveStep = Number.isFinite(step) && step > 0 ? step : isInt ? 1 : 0.1;
  let next: number;
  if (current === null || !Number.isFinite(current)) {
    if (min !== undefined) {
      next = min;
    } else {
      next = direction > 0 ? effectiveStep : 0;
    }
  } else {
    next = current + direction * effectiveStep;
  }

  if (min !== undefined && Number.isFinite(min)) {
    next = Math.max(min, next);
  }
  if (max !== undefined && Number.isFinite(max)) {
    next = Math.min(max, next);
  }

  if (isInt) {
    return Math.round(next);
  }
  const stepStr = String(effectiveStep);
  const decimals = (stepStr.split('.')[1] ?? '').length;
  return decimals > 0 ? Number(next.toFixed(decimals)) : Number(next.toFixed(4));
}
