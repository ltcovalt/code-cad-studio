import type { UnsupportedDefaultChange } from './types';

export interface ExternalComponentGroup {
  kind: 'external';
  key: string;
  ownerClass: string;
  sourceRelPath: string;
  sourceFile?: string;
  inLibrary: boolean;
  items: UnsupportedDefaultChange[];
}

export interface LocalUnsupportedGroup {
  kind: 'local';
  key: string;
  items: UnsupportedDefaultChange[];
}

export type UnsupportedGroup = ExternalComponentGroup | LocalUnsupportedGroup;

/**
 * Group unsupported `update-defaults` entries so external shared component
 * dataclasses are presented per-file/class (with an Open in Editor action for
 * in-library files), while local non-literal/unsupported fields are grouped
 * separately.
 */
export function groupUnsupportedDefaults(
  unsupported: readonly UnsupportedDefaultChange[]
): UnsupportedGroup[] {
  const externalMap = new Map<string, ExternalComponentGroup>();
  const localItems: UnsupportedDefaultChange[] = [];

  for (const item of unsupported) {
    if (item.reason_code === 'external_component') {
      const ownerClass = item.owner_class || 'External Component';
      const sourceRelPath = item.source_rel_path || '';
      const key = `${sourceRelPath}::${ownerClass}`;
      let group = externalMap.get(key);
      if (!group) {
        group = {
          kind: 'external',
          key,
          ownerClass,
          sourceRelPath,
          sourceFile: item.in_library ? item.source_file : undefined,
          inLibrary: Boolean(item.in_library && item.source_file),
          items: [],
        };
        externalMap.set(key, group);
      }
      group.items.push(item);
    } else {
      localItems.push(item);
    }
  }

  const groups: UnsupportedGroup[] = [...externalMap.values()];
  if (localItems.length > 0) {
    groups.push({
      kind: 'local',
      key: '__local__',
      items: localItems,
    });
  }
  return groups;
}
