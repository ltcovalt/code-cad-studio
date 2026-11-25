import { describe, expect, it } from 'vitest';
import {
  humanizeExportFilename,
  normalizePresetLabel,
  resolveUniquePresetName,
  slugifyPresetLabel,
  suggestNewPresetName,
} from './presetNaming';
import type { Preset } from './types';

function makePreset(key: string, label: string, source: Preset['source'] = 'project'): Preset {
  return { key, label, description: '', source, params: {} };
}

describe('presetNaming', () => {
  it('normalizes and slugifies preset labels consistently', () => {
    expect(normalizePresetLabel('  54mm TPU  ')).toBe('54mm tpu');
    expect(slugifyPresetLabel('54mm TPU')).toBe('54mm_tpu');
    expect(slugifyPresetLabel('Default')).toBe('custom_preset');
    expect(slugifyPresetLabel('   ')).toBe('custom_preset');
  });

  it('humanizes dynamic export filenames by stripping model stem and formatting tokens', () => {
    expect(humanizeExportFilename('can_lid_54mm_tpu', 'Can Lid', 'models/can_lid/can_lid.py')).toBe(
      '54mm TPU'
    );
    expect(
      humanizeExportFilename('rack_bracket_2u_hex', 'Rack Bracket', 'models/rack_bracket/rack_bracket.py')
    ).toBe('2U Hex');
    expect(
      humanizeExportFilename(
        'spacer_100x100x3mm_corner',
        'Spacer',
        'models/spacer/spacer.py'
      )
    ).toBe('100x100x3mm Corner');
    expect(
      humanizeExportFilename(
        'shaft_end_cap_12.7mm_round.stl',
        'Shaft End Cap',
        'models/shaft_end_cap/shaft_end_cap.py'
      )
    ).toBe('12.7mm Round');
    expect(humanizeExportFilename('bracket_m3_usbc_mount', 'Bracket', 'bracket.py')).toBe(
      'M3 USB-C Mount'
    );
  });

  it('returns empty string when export filename has no model-specific suffix', () => {
    expect(humanizeExportFilename('box', 'Box', 'models/box/box.py')).toBe('');
    expect(humanizeExportFilename('can_lid.step', 'Can Lid', 'models/can_lid/can_lid.py')).toBe('');
    expect(humanizeExportFilename('', 'Box', 'models/box/box.py')).toBe('');
  });

  it('resolves collisions with (Modified) then numeric suffixes without using (User)', () => {
    const existing: Preset[] = [
      makePreset('default', '65mm Cat Food Can', 'default'),
      makePreset('54mm_tpu', '54mm TPU', 'project'),
    ];

    expect(resolveUniquePresetName('75mm TPU', existing)).toBe('75mm TPU');
    expect(resolveUniquePresetName('54mm TPU', existing)).toBe('54mm TPU (Modified)');

    const withModified = [
      ...existing,
      makePreset('54mm_tpu_modified', '54mm TPU (Modified)', 'user'),
    ];
    expect(resolveUniquePresetName('54mm TPU', withModified)).toBe('54mm TPU (2)');

    const withNumbered = [...withModified, makePreset('54mm_tpu_2', '54mm TPU (2)', 'user')];
    expect(resolveUniquePresetName('54mm TPU', withNumbered)).toBe('54mm TPU (3)');
  });

  it('avoids colliding with the canonical default preset display label', () => {
    const existing: Preset[] = [makePreset('default', '54mm TPU', 'default')];
    expect(resolveUniquePresetName('54mm TPU', existing)).toBe('54mm TPU (Modified)');
  });

  it('falls back from dynamic export filename -> custom base label -> model name -> Custom Preset', () => {
    const baseDefault: Preset[] = [makePreset('default', 'Default', 'default')];
    const customDefault: Preset[] = [makePreset('default', '65mm Cat Food Can', 'default')];

    // 1. Dynamic export filename suffix wins
    expect(
      suggestNewPresetName({
        exportFilename: 'can_lid_54mm_tpu',
        modelName: 'Can Lid',
        modelRelPath: 'models/can_lid/can_lid.py',
        basePresetLabel: '65mm Cat Food Can',
        existingPresets: customDefault,
      })
    ).toBe('54mm TPU');

    // 2. Custom @default_preset label when no dynamic suffix exists
    expect(
      suggestNewPresetName({
        exportFilename: 'can_lid',
        modelName: 'Can Lid',
        modelRelPath: 'models/can_lid/can_lid.py',
        basePresetLabel: '65mm Cat Food Can',
        existingPresets: customDefault,
      })
    ).toBe('65mm Cat Food Can (Modified)');

    // 3. Model name when neither dynamic suffix nor custom base label exists
    expect(
      suggestNewPresetName({
        exportFilename: 'spool_cap',
        modelName: 'Spool Cap',
        modelRelPath: 'models/spool_cap/spool_cap.py',
        basePresetLabel: 'Default',
        existingPresets: baseDefault,
      })
    ).toBe('Spool Cap');

    // 4. Colliding model name gets suffix
    expect(
      suggestNewPresetName({
        exportFilename: 'spool_cap',
        modelName: 'Spool Cap',
        modelRelPath: 'models/spool_cap/spool_cap.py',
        basePresetLabel: 'Default',
        existingPresets: [...baseDefault, makePreset('spool_cap', 'Spool Cap', 'project')],
      })
    ).toBe('Spool Cap (Modified)');

    // 5. Final fallback when model name is empty
    expect(
      suggestNewPresetName({
        exportFilename: '',
        existingPresets: baseDefault,
      })
    ).toBe('Custom Preset');
  });
});
