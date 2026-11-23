import type { Library, ModelInfo } from './types';

export interface TreeFolderNode {
  kind: 'folder';
  key: string;
  nodeType: 'library' | 'category';
  label: string;
  subtitle?: string;
  path?: string;
  libraryId: string;
  error?: string;
  children: TreeNode[];
  modelCount: number;
}

export interface TreeModelNode {
  kind: 'model';
  key: string;
  model: ModelInfo;
}

export type TreeNode = TreeFolderNode | TreeModelNode;

function parentDirLabel(fullPath: string): string {
  const normalized = fullPath.replace(/[\\/]+$/, '');
  const parts = normalized.split(/[\\/]/).filter(Boolean);
  if (parts.length >= 2) {
    return parts[parts.length - 2];
  }
  return '';
}

function modelMatchesTokens(model: ModelInfo, tokens: string[]): boolean {
  if (tokens.length === 0) return true;
  const haystack = [
    model.name,
    model.id,
    model.local_id,
    model.description,
    model.library_name,
    model.library_id,
    ...(model.folders ?? []),
    ...(model.folder_labels ?? []),
  ]
    .join(' ')
    .toLowerCase();
  return tokens.every((tok) => haystack.includes(tok));
}

function sortTreeChildren(nodes: TreeNode[]): void {
  nodes.sort((a, b) => {
    if (a.kind !== b.kind) {
      return a.kind === 'folder' ? -1 : 1;
    }
    const la = a.kind === 'folder' ? a.label.toLowerCase() : a.model.name.toLowerCase();
    const lb = b.kind === 'folder' ? b.label.toLowerCase() : b.model.name.toLowerCase();
    if (la !== lb) return la.localeCompare(lb);
    return a.key.localeCompare(b.key);
  });
  for (const node of nodes) {
    if (node.kind === 'folder') {
      sortTreeChildren(node.children);
    }
  }
}

function countModels(node: TreeFolderNode): number {
  let total = 0;
  for (const child of node.children) {
    if (child.kind === 'model') {
      total += 1;
    } else {
      total += countModels(child);
    }
  }
  node.modelCount = total;
  return total;
}

/**
 * Builds the sidebar Explorer tree from configured libraries and discovered models.
 * - Preserves configured library ordering at the top level.
 * - Supports models at any depth (root, category, nested subcategories).
 * - Hides the top-level library wrapper when only 1 healthy library is configured.
 * - Supports multi-token search across library names, categories, model names, and descriptions.
 */
export function buildModelTree(
  libraries: Library[],
  models: ModelInfo[],
  libraryErrors: Record<string, string>,
  searchQuery: string
): TreeNode[] {
  const tokens = searchQuery
    .trim()
    .toLowerCase()
    .split(/\s+/)
    .filter(Boolean);
  const isSearching = tokens.length > 0;

  // Detect duplicate display names across libraries so we can show a subtle parent-dir hint
  const nameCounts = new Map<string, number>();
  for (const lib of libraries) {
    const key = lib.name.trim().toLowerCase();
    nameCounts.set(key, (nameCounts.get(key) ?? 0) + 1);
  }

  // Group models by library_id (preserving any models whose library isn't in config list as fallback)
  const libOrder: Library[] = [...libraries];
  const knownIds = new Set(libraries.map((l) => l.id));
  for (const m of models) {
    if (!knownIds.has(m.library_id)) {
      knownIds.add(m.library_id);
      libOrder.push({
        id: m.library_id,
        name: m.library_name || m.library_id,
        path: '',
      });
    }
  }

  const libNodes: TreeFolderNode[] = [];

  for (const lib of libOrder) {
    const libModels = models.filter(
      (m) => m.library_id === lib.id && modelMatchesTokens(m, tokens)
    );
    const libErr = libraryErrors[lib.id] ?? '';

    if (isSearching && libModels.length === 0) {
      continue;
    }

    const dupName = (nameCounts.get(lib.name.trim().toLowerCase()) ?? 0) > 1;
    const rootNode: TreeFolderNode = {
      kind: 'folder',
      key: `lib:${lib.id}`,
      nodeType: 'library',
      label: lib.name || lib.id,
      subtitle: dupName && lib.path ? parentDirLabel(lib.path) : undefined,
      path: lib.path,
      libraryId: lib.id,
      error: libErr || undefined,
      children: [],
      modelCount: 0,
    };

    const folderMap = new Map<string, TreeFolderNode>();

    for (const model of libModels) {
      const folders = model.folders ?? [];
      const labels = model.folder_labels ?? [];
      let parent = rootNode;

      for (let i = 0; i < folders.length; i++) {
        const segPath = folders.slice(0, i + 1).join('/');
        const folderKey = `folder:${lib.id}:${segPath}`;
        let existing = folderMap.get(folderKey);
        if (!existing) {
          existing = {
            kind: 'folder',
            key: folderKey,
            nodeType: 'category',
            label: labels[i] || folders[i],
            libraryId: lib.id,
            children: [],
            modelCount: 0,
          };
          folderMap.set(folderKey, existing);
          parent.children.push(existing);
        }
        parent = existing;
      }

      parent.children.push({
        kind: 'model',
        key: model.id,
        model,
      });
    }

    sortTreeChildren(rootNode.children);
    countModels(rootNode);
    libNodes.push(rootNode);
  }

  // Hide the top-level library node when only 1 healthy library is configured
  if (libraries.length === 1 && libNodes.length === 1 && !libNodes[0].error) {
    return libNodes[0].children;
  }

  return libNodes;
}

export function collectFolderKeys(nodes: TreeNode[]): string[] {
  const keys: string[] = [];
  const walk = (list: TreeNode[]) => {
    for (const node of list) {
      if (node.kind === 'folder') {
        keys.push(node.key);
        walk(node.children);
      }
    }
  };
  walk(nodes);
  return keys;
}
