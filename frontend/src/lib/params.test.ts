import { describe, expect, it } from 'vitest';
import {
  deepClone,
  evaluateWhen,
  extractComponentPresetSelections,
  getParamByPath,
  mergeWithDefaults,
  normalizeComponentPresetsForSave,
  normalizeParamsWithFields,
  paramsEqual,
  prefixFieldPaths,
  resolvePresetParams,
  setParamByPath,
  splitFields,
  stepNumber,
} from './params';
import type { ComponentPresetBundle, ParamField, ParamsRecord, Preset } from './types';

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

  it('resolves component presets, nullable groups, and save normalization', () => {
    const fields: ParamField[] = [
      {
        name: 'core',
        path: 'core',
        label: 'Core',
        type: 'group',
        default: { rim_diameter: 54 },
        description: '',
        children: [
          {
            name: 'rim_diameter',
            path: 'core.rim_diameter',
            label: 'Rim Diameter',
            type: 'float',
            default: 54,
            description: '',
          },
        ],
      },
      {
        name: 'pull_tab',
        path: 'pull_tab',
        label: 'Pull Tab',
        type: 'group',
        nullable: true,
        component_id: 'lib/components/pull_tab',
        component_name: 'Pull Tab',
        default: { length: 6, width: 16, hanging_hole: false },
        group_defaults: { length: 6, width: 16, hanging_hole: false },
        description: '',
        children: [
          { name: 'length', path: 'pull_tab.length', label: 'Length', type: 'float', default: 6, description: '' },
          { name: 'width', path: 'pull_tab.width', label: 'Width', type: 'float', default: 16, description: '' },
          { name: 'hanging_hole', path: 'pull_tab.hanging_hole', label: 'Hanging Hole', type: 'bool', default: false, description: '' },
        ],
      },
      {
        name: 'gridfinity',
        path: 'gridfinity',
        label: 'Gridfinity',
        type: 'group',
        nullable: true,
        component_id: 'lib/components/gridfinity_base',
        component_name: 'Gridfinity Base',
        default: null,
        group_defaults: { magnets: true, magnet_diameter: 6.1 },
        description: '',
        children: [
          { name: 'magnets', path: 'gridfinity.magnets', label: 'Magnets', type: 'bool', default: true, description: '' },
          { name: 'magnet_diameter', path: 'gridfinity.magnet_diameter', label: 'Magnet Diameter', type: 'float', default: 6.1, description: '' },
        ],
      },
    ];

    const defaults: ParamsRecord = {
      core: { rim_diameter: 54 },
      pull_tab: { length: 6, width: 16, hanging_hole: false },
      gridfinity: null,
    };

    const components: Record<string, ComponentPresetBundle> = {
      'lib/components/pull_tab': {
        id: 'lib/components/pull_tab',
        name: 'Pull Tab',
        defaults: { length: 6, width: 16, hanging_hole: false },
        fields: fields[1].children,
        presets: [
          { key: 'default', label: 'Standard', description: '', source: 'default', params: {} },
          {
            key: 'heavy_duty',
            label: 'Heavy Duty',
            description: '',
            source: 'project',
            params: { length: 8, width: 20 },
          },
        ],
      },
      'lib/components/gridfinity_base': {
        id: 'lib/components/gridfinity_base',
        name: 'Gridfinity Base',
        defaults: { magnets: true, magnet_diameter: 6.1 },
        fields: fields[2].children,
        presets: [
          { key: 'default', label: 'With Magnets', description: '', source: 'default', params: {} },
          {
            key: 'no_magnets',
            label: 'No Magnets',
            description: '',
            source: 'project',
            params: { magnets: false },
          },
        ],
      },
    };

    const preset: Preset = {
      key: 'food_28oz',
      label: 'Food 28oz',
      description: '',
      source: 'project',
      component_presets: { pull_tab: 'heavy_duty', gridfinity: 'no_magnets' },
      params: { core: { rim_diameter: 101.5 }, pull_tab: { hanging_hole: true } },
    };

    const resolved = resolvePresetParams(defaults, fields, preset, components);
    expect(resolved).toEqual({
      core: { rim_diameter: 101.5 },
      pull_tab: { length: 8, width: 20, hanging_hole: true },
      gridfinity: { magnets: false, magnet_diameter: 6.1 },
    });

    // Disabled nullable group overrides component_presets cleanly
    const disabledPreset: Preset = {
      key: 'no_tab',
      label: 'No Tab',
      description: '',
      source: 'project',
      params: { pull_tab: null },
    };
    expect(resolvePresetParams(defaults, fields, disabledPreset, components).pull_tab).toBeNull();

    // Missing component preset falls back to component defaults
    const missingRefPreset: Preset = {
      key: 'broken_ref',
      label: 'Broken Ref',
      description: '',
      source: 'project',
      component_presets: { pull_tab: 'deleted_variant' },
      params: {},
    };
    expect(resolvePresetParams(defaults, fields, missingRefPreset, components).pull_tab).toEqual({
      length: 6,
      width: 16,
      hanging_hole: false,
    });

    expect(extractComponentPresetSelections(preset, fields)).toEqual({
      pull_tab: 'heavy_duty',
      gridfinity: 'no_magnets',
    });

    // Save normalization omits implicit default for enabled pull_tab, preserves explicit default for disabled-by-default gridfinity
    expect(
      normalizeComponentPresetsForSave(
        { pull_tab: 'default', gridfinity: 'default' },
        {
          core: { rim_diameter: 54 },
          pull_tab: { length: 6, width: 16, hanging_hole: false },
          gridfinity: { magnets: true, magnet_diameter: 6.1 },
        },
        defaults,
        fields
      )
    ).toEqual({ gridfinity: 'default' });

    // Base model with component_preset set in Python defaults to that preset and omits it on save unless reverted to 'default'
    const fieldsWithBasePreset: ParamField[] = [
      fields[0],
      { ...fields[1], component_preset: 'heavy_duty' },
      fields[2],
    ];
    expect(extractComponentPresetSelections(null, fieldsWithBasePreset)).toEqual({
      pull_tab: 'heavy_duty',
      gridfinity: 'default',
    });
    expect(
      normalizeComponentPresetsForSave(
        { pull_tab: 'heavy_duty' },
        { pull_tab: { length: 8 } },
        defaults,
        fieldsWithBasePreset
      )
    ).toEqual({});
    expect(
      normalizeComponentPresetsForSave(
        { pull_tab: 'default' },
        { pull_tab: { length: 6 } },
        defaults,
        fieldsWithBasePreset
      )
    ).toEqual({ pull_tab: 'default' });

    // evaluateWhen supports checking top-level nullable groups from sibling groups (e.g. when={"gridfinity": null})
    expect(evaluateWhen({ gridfinity: null }, { gridfinity: null, structure: { wall: 3 } }, { wall: 3 })).toBe(true);
    expect(evaluateWhen({ gridfinity: null }, { gridfinity: { magnets: true }, structure: { wall: 3 } }, { wall: 3 })).toBe(false);
  });
});
