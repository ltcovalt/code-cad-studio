import { describe, expect, it } from 'vitest';
import {
  deepClone,
  evaluateWhen,
  getParamByPath,
  mergeWithDefaults,
  normalizeParamsWithFields,
  paramsEqual,
  prefixFieldPaths,
  setParamByPath,
  splitFields,
  stepNumber,
} from './params';
import type { ParamField, ParamsRecord } from './types';

describe('params helpers', () => {
  it('mergeWithDefaults overlays nested objects, preserves nulls, and prunes removed keys', () => {
    const defaults: ParamsRecord = {
      width: 100,
      height: null,
      core: { shape: 'round', wall: 2.0 },
    };
    const overrides: ParamsRecord = {
      width: 120,
      height: null,
      removed_field: 999,
      core: { wall: 3.5, stale_nested: 'drop-me' },
    };

    const merged = mergeWithDefaults(defaults, overrides);
    expect(merged).toEqual({
      width: 120,
      height: null,
      core: { shape: 'round', wall: 3.5 },
    });
    expect('removed_field' in merged).toBe(false);
    expect('stale_nested' in (merged.core as ParamsRecord)).toBe(false);
  });

  it('mergeWithDefaults blocks __proto__, constructor, and prototype pollution', () => {
    const defaults: ParamsRecord = { width: 50, nested: { a: 1 } };
    const malicious = JSON.parse(
      '{"__proto__":{"polluted":true},"constructor":{"prototype":{"polluted2":true}},"prototype":{"polluted3":true},"nested":{"__proto__":{"nestedPolluted":true},"a":2}}'
    ) as ParamsRecord;

    const merged = mergeWithDefaults(defaults, malicious);
    expect(merged).toEqual({ width: 50, nested: { a: 2 } });
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
    expect(({} as Record<string, unknown>).polluted2).toBeUndefined();
    expect(({} as Record<string, unknown>).polluted3).toBeUndefined();
    expect(({} as Record<string, unknown>).nestedPolluted).toBeUndefined();
  });

  it('setParamByPath and getParamByPath handle nested objects, arrays, and dangerous keys', () => {
    const initial: ParamsRecord = {
      core: { width: 40 },
      items: [{ x: 1 }, { x: 2 }],
    };

    const updated = setParamByPath(initial, 'items.1.x', 99);
    expect(getParamByPath(updated, 'items.1.x')).toBe(99);
    expect(getParamByPath(initial, 'items.1.x')).toBe(2); // immutable

    const withNewNested = setParamByPath(updated, 'extra.sub.0', 'hello');
    expect(getParamByPath(withNewNested, 'extra.sub.0')).toBe('hello');

    // Dangerous paths must be no-ops
    const blocked1 = setParamByPath(initial, '__proto__.polluted', true);
    const blocked2 = setParamByPath(initial, 'core.constructor.prototype.polluted', true);
    const blocked3 = setParamByPath(initial, 'prototype.polluted', true);
    expect(blocked1).toEqual(initial);
    expect(blocked2).toEqual(initial);
    expect(blocked3).toEqual(initial);
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
    expect(getParamByPath(initial, '__proto__')).toBeUndefined();
    expect(getParamByPath(initial, 'core.constructor')).toBeUndefined();
  });

  it('normalizeParamsWithFields fills missing keys in list-of-dataclass items and nested groups', () => {
    const fields: ParamField[] = [
      {
        name: 'core',
        path: 'core',
        label: 'Core',
        type: 'group',
        default: { wall: 2 },
        description: '',
        children: [
          {
            name: 'cutouts',
            path: 'core.cutouts',
            label: 'Cutouts',
            type: 'list',
            default: [],
            description: '',
            item: {
              name: 'item',
              path: '',
              label: 'Cutout',
              type: 'group',
              default: { x: 0, y: 0, diameter: 5 },
              description: '',
              children: [],
            },
          },
        ],
      },
    ];

    const normalized = normalizeParamsWithFields(
      { core: { wall: 3, cutouts: [{ x: 10 }, { diameter: 8, obsolete: 1 }] } },
      fields
    );
    expect(normalized).toEqual({
      core: {
        wall: 3,
        cutouts: [
          { x: 10, y: 0, diameter: 5 },
          { x: 0, y: 0, diameter: 8 },
        ],
      },
    });
  });

  it('evaluateWhen resolves dotted root paths, bare group keys, and array membership', () => {
    const root: ParamsRecord = { core: { shape: 'round' }, lid: { style: 'beaded', size: 20 } };
    const group = root.lid as ParamsRecord;

    expect(evaluateWhen(undefined, root, group)).toBe(true);
    expect(evaluateWhen({ 'core.shape': 'round', style: ['beaded', 'snap'] }, root, group)).toBe(true);
    expect(evaluateWhen({ 'core.shape': 'rect' }, root, group)).toBe(false);
    expect(evaluateWhen({ style: 'flat' }, root, group)).toBe(false);
    expect(evaluateWhen({ toString: 'nope' }, root, group)).toBe(false);
  });

  it('stepNumber avoids float drift, clamps bounds, and handles null start', () => {
    expect(stepNumber(0.1, 1, 0.2, false)).toBe(0.3);
    expect(stepNumber(9.8, 1, 0.5, false, 0, 10)).toBe(10);
    expect(stepNumber(0.2, -1, 0.5, false, 0, 10)).toBe(0);
    expect(stepNumber(null, 1, 1, true, 5, 20)).toBe(5);
    expect(stepNumber(null, 1, 2, true)).toBe(2);
    expect(stepNumber(null, -1, 2, true)).toBe(0);
    expect(stepNumber(3.4, 1, 1, true)).toBe(4);
  });

  it('paramsEqual, deepClone, prefixFieldPaths, and splitFields behave consistently', () => {
    expect(paramsEqual({ a: 1, b: null }, { a: 1, b: undefined })).toBe(true);
    expect(paramsEqual(Number.NaN, Number.NaN)).toBe(true);
    expect(paramsEqual({ a: 1 }, { a: 1, toString: 2 } as unknown as ParamsRecord)).toBe(false);

    const cloned = deepClone({ list: [{ v: 1 }] });
    cloned.list[0].v = 2;
    expect(cloned.list[0].v).toBe(2);

    const prefixed = prefixFieldPaths(
      {
        name: 'sub',
        path: 'sub',
        label: 'Sub',
        type: 'group',
        default: {},
        description: '',
        children: [
          { name: 'w', path: 'sub.w', label: 'W', type: 'float', default: 1, description: '' },
        ],
      },
      'items.0'
    );
    expect(prefixed.path).toBe('items.0.sub');
    expect(prefixed.children?.[0].path).toBe('items.0.sub.w');

    const { coreFields, nestedGroups } = splitFields([
      { name: 'a', path: 'a', label: 'A', type: 'int', default: 1, description: '' },
      { name: 'g', path: 'g', label: 'G', type: 'group', default: {}, description: '' },
    ]);
    expect(coreFields.map((f) => f.name)).toEqual(['a']);
    expect(nestedGroups.map((f) => f.name)).toEqual(['g']);
  });
});
