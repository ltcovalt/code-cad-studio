import { describe, expect, it } from 'vitest';
import { buildModelTree, collectFolderKeys } from './modelTree';
import type { Library, ModelInfo } from './types';

function makeModel(overrides: Partial<ModelInfo> & { id: string; name: string }): ModelInfo {
  const [libId, ...rest] = overrides.id.split('/');
  const localId = rest.join('/') || overrides.id;
  return {
    local_id: localId,
    library_id: libId || 'lib1',
    library_name: 'Lib One',
    folders: [],
    folder_labels: [],
    path: `/tmp/${localId}.py`,
    rel_path: `${localId}.py`,
    description: '',
    params_class: 'Params',
    entrypoint: 'create_model',
    dir: '/tmp',
    ...overrides,
  };
}

describe('modelTree helpers', () => {
  it('hides top-level library node when only 1 healthy library is configured', () => {
    const libs: Library[] = [{ id: 'code-cad', name: 'Code CAD', path: '/work/code-cad' }];
    const models: ModelInfo[] = [
      makeModel({
        id: 'code-cad/enclosures/lid',
        name: 'Container Lid',
        library_id: 'code-cad',
        library_name: 'Code CAD',
        folders: ['enclosures'],
        folder_labels: ['Enclosures'],
      }),
      makeModel({
        id: 'code-cad/bracket',
        name: 'Alpha Bracket',
        library_id: 'code-cad',
        library_name: 'Code CAD',
      }),
    ];

    const tree = buildModelTree(libs, models, {}, '');
    expect(tree).toHaveLength(2);
    // Folders sort before leaf models
    expect(tree[0].kind).toBe('folder');
    expect(tree[0].kind === 'folder' && tree[0].label).toBe('Enclosures');
    expect(tree[1].kind).toBe('model');
    expect(tree[1].kind === 'model' && tree[1].model.name).toBe('Alpha Bracket');
  });

  it('keeps library node when single library has an error or multiple libraries exist with duplicate names', () => {
    const libs: Library[] = [
      { id: 'lib-a', name: 'Models', path: '/users/luke/work/models' },
      { id: 'lib-b', name: 'Models', path: '/users/luke/personal/models' },
    ];
    const models: ModelInfo[] = [
      makeModel({ id: 'lib-a/box', name: 'Box', library_id: 'lib-a', library_name: 'Models' }),
      makeModel({ id: 'lib-b/cap', name: 'Cap', library_id: 'lib-b', library_name: 'Models' }),
    ];

    const tree = buildModelTree(libs, models, { 'lib-b': 'Path unreadable' }, '');
    expect(tree).toHaveLength(2);
    expect(tree[0].kind === 'folder' && tree[0].subtitle).toBe('work');
    expect(tree[1].kind === 'folder' && tree[1].subtitle).toBe('personal');
    expect(tree[1].kind === 'folder' && tree[1].error).toBe('Path unreadable');

    const singleErrTree = buildModelTree([libs[0]], [models[0]], { 'lib-a': 'Broken' }, '');
    expect(singleErrTree).toHaveLength(1);
    expect(singleErrTree[0].kind).toBe('folder');
    expect(singleErrTree[0].kind === 'folder' && singleErrTree[0].nodeType).toBe('library');
  });

  it('supports multi-token search across rel_path, parse_error, and keeps error-kind models visible', () => {
    const libs: Library[] = [{ id: 'demo', name: 'Demo', path: '/demo' }];
    const models: ModelInfo[] = [
      makeModel({
        id: 'demo/broken_part',
        name: 'Broken Part',
        library_id: 'demo',
        rel_path: 'models/broken_part/broken_part.py',
        kind: 'error',
        parse_error: 'SyntaxError: expected ":" on line 12',
      }),
      makeModel({
        id: 'demo/good_part',
        name: 'Good Part',
        library_id: 'demo',
        rel_path: 'models/good_part/good_part.py',
        kind: 'parametric',
      }),
    ];

    const allTree = buildModelTree(libs, models, {}, '');
    expect(allTree).toHaveLength(2);

    const searchByParseError = buildModelTree(libs, models, {}, 'syntaxerror line 12');
    expect(searchByParseError).toHaveLength(1);
    expect(searchByParseError[0].kind === 'model' && searchByParseError[0].model.id).toBe(
      'demo/broken_part'
    );

    const searchByRelPath = buildModelTree(libs, models, {}, 'good_part.py');
    expect(searchByRelPath).toHaveLength(1);
    expect(searchByRelPath[0].kind === 'model' && searchByRelPath[0].model.id).toBe(
      'demo/good_part'
    );
  });

  it('collectFolderKeys gathers nested folder keys recursively', () => {
    const libs: Library[] = [{ id: 'demo', name: 'Demo', path: '/demo' }];
    const models: ModelInfo[] = [
      makeModel({
        id: 'demo/a/b/part',
        name: 'Part',
        library_id: 'demo',
        folders: ['a', 'b'],
        folder_labels: ['A', 'B'],
      }),
    ];
    const tree = buildModelTree(libs, models, {}, '');
    expect(collectFolderKeys(tree)).toEqual(['folder:demo:a', 'folder:demo:a/b']);
  });

  it('groups models and components into symmetrical Models and Components sections when components exist', () => {
    const libs: Library[] = [{ id: 'cad', name: 'CAD', path: '/cad' }];
    const models: ModelInfo[] = [
      makeModel({
        id: 'cad/can_lid',
        name: 'Can Lid',
        library_id: 'cad',
        category: 'model',
      }),
      makeModel({
        id: 'cad/components/pull_tab',
        name: 'Pull Tab',
        library_id: 'cad',
        category: 'component',
      }),
      makeModel({
        id: 'cad/components/hardware/knob',
        name: 'Knob',
        library_id: 'cad',
        category: 'component',
        folders: ['hardware'],
        folder_labels: ['Hardware'],
      }),
    ];

    const tree = buildModelTree(libs, models, {}, '');
    expect(tree).toHaveLength(2);
    expect(tree[0].kind).toBe('folder');
    if (tree[0].kind === 'folder') {
      expect(tree[0].nodeType).toBe('models');
      expect(tree[0].label).toBe('Models');
      expect(tree[0].modelCount).toBe(1);
      expect(tree[0].children.map((c) => (c.kind === 'model' ? c.model.name : c.label))).toEqual([
        'Can Lid',
      ]);
    }
    expect(tree[1].kind).toBe('folder');
    if (tree[1].kind === 'folder') {
      expect(tree[1].nodeType).toBe('components');
      expect(tree[1].label).toBe('Components');
      expect(tree[1].modelCount).toBe(2);
      expect(tree[1].children.map((c) => (c.kind === 'folder' ? c.label : c.model.name))).toEqual([
        'Hardware',
        'Pull Tab',
      ]);
    }

    const compOnly = buildModelTree(libs, models, {}, 'component');
    expect(compOnly).toHaveLength(1);
    expect(compOnly[0].kind === 'folder' && compOnly[0].nodeType).toBe('components');
  });
});
