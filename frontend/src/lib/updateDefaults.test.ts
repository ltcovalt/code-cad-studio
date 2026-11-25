import { describe, expect, it } from 'vitest';
import type { UnsupportedDefaultChange } from './types';
import { groupUnsupportedDefaults } from './updateDefaults';

describe('groupUnsupportedDefaults', () => {
  it('groups external component fields by file and class while separating local unsupported fields', () => {
    const unsupported: UnsupportedDefaultChange[] = [
      {
        path: 'pull_tab.length',
        reason: 'dataclass is defined outside this model file',
        reason_code: 'external_component',
        owner_class: 'PullTabParams',
        source_file: '/lib/components/pull_tab.py',
        source_rel_path: 'components/pull_tab.py',
        in_library: true,
      },
      {
        path: 'pull_tab.width',
        reason: 'dataclass is defined outside this model file',
        reason_code: 'external_component',
        owner_class: 'PullTabParams',
        source_file: '/lib/components/pull_tab.py',
        source_rel_path: 'components/pull_tab.py',
        in_library: true,
      },
      {
        path: 'core.computed_gap',
        reason: 'uses a non-literal expression',
        reason_code: 'non_literal_expression',
        owner_class: 'CoreParams',
        in_library: false,
      },
      {
        path: 'ext.flag',
        reason: 'dataclass is defined outside this model file',
        reason_code: 'external_component',
        owner_class: 'ThirdPartyParams',
        source_rel_path: 'third_party.py',
        in_library: false,
      },
    ];

    const groups = groupUnsupportedDefaults(unsupported);
    expect(groups).toHaveLength(3);

    expect(groups[0]).toEqual({
      kind: 'external',
      key: 'components/pull_tab.py::PullTabParams',
      ownerClass: 'PullTabParams',
      sourceRelPath: 'components/pull_tab.py',
      sourceFile: '/lib/components/pull_tab.py',
      inLibrary: true,
      items: [unsupported[0], unsupported[1]],
    });

    expect(groups[1]).toEqual({
      kind: 'external',
      key: 'third_party.py::ThirdPartyParams',
      ownerClass: 'ThirdPartyParams',
      sourceRelPath: 'third_party.py',
      sourceFile: undefined,
      inLibrary: false,
      items: [unsupported[3]],
    });

    expect(groups[2]).toEqual({
      kind: 'local',
      key: '__local__',
      items: [unsupported[2]],
    });
  });

  it('returns empty array when there are no unsupported entries', () => {
    expect(groupUnsupportedDefaults([])).toEqual([]);
  });
});
