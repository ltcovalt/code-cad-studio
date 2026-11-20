import type { ParamField, ParamsRecord } from './types';

export function isPlainObject(value: unknown): value is ParamsRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
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

export function getParamByPath(root: ParamsRecord, path: string): unknown {
  const parts = path.split('.');
  let curr: unknown = root;
  for (const part of parts) {
    if (!isPlainObject(curr)) return undefined;
    curr = curr[part];
  }
  return curr;
}

export function setParamByPath(root: ParamsRecord, path: string, value: unknown): ParamsRecord {
  const parts = path.split('.');
  const next: ParamsRecord = { ...root };
  let curr: ParamsRecord = next;
  for (let i = 0; i < parts.length - 1; i++) {
    const part = parts[i];
    const existing = curr[part];
    const clonedChild: ParamsRecord = isPlainObject(existing) ? { ...existing } : {};
    curr[part] = clonedChild;
    curr = clonedChild;
  }
  curr[parts[parts.length - 1]] = value;
  return next;
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
