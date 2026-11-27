import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ModelDetailResponse, ModelInfo, RenderResponse } from './types';

const mockApi = vi.hoisted(() => ({
  getConfig: vi.fn(),
  saveConfig: vi.fn(),
  saveTheme: vi.fn(async () => {}),
  listModels: vi.fn(),
  getModel: vi.fn(),
  renderModel: vi.fn(),
  cancelRender: vi.fn(),
  exportModel: vi.fn(),
  getPresets: vi.fn(),
  savePreset: vi.fn(),
  setPresetVerification: vi.fn(),
  deletePreset: vi.fn(),
  renamePreset: vi.fn(),
  setDefaultPreset: vi.fn(),
  updateModelDefaults: vi.fn(),
  scaffoldModel: vi.fn(),
  openInEditor: vi.fn(),
  revealFile: vi.fn(),
  openInSlicer: vi.fn(),
  subscribeEvents: vi.fn(() => () => {}),
}));

vi.mock('./api', () => ({
  ...mockApi,
  ApiRequestError: class ApiRequestError extends Error {
    status: number;
    code?: string;
    traceback?: string;
    constructor(message: string, status: number, code?: string, traceback?: string) {
      super(message);
      this.status = status;
      this.code = code;
      this.traceback = traceback;
    }
  },
}));

import type { CadViewportController } from './cadViewer';
import { StudioState } from './studioState.svelte';

function makeModel(id: string, pyPath: string): ModelInfo {
  const [libId, ...rest] = id.split('/');
  const localId = rest.join('/');
  return {
    id,
    local_id: localId,
    library_id: libId,
    library_name: 'Lib',
    folders: [],
    folder_labels: [],
    name: localId,
    path: pyPath,
    rel_path: `${localId}/${localId}.py`,
    py_path: pyPath,
    kind: 'parametric',
    description: '',
    params_class: 'Params',
    entrypoint: 'create_model',
    dir: `/lib/${localId}`,
  };
}

function makeDetail(model: ModelInfo, widthDefault: number, defaultPresetKey = ''): ModelDetailResponse {
  return {
    ok: true,
    model,
    description: 'Test model',
    fields: [
      {
        name: 'width',
        path: 'width',
        label: 'Width',
        type: 'float',
        default: widthDefault,
        description: '',
      },
    ],
    defaults: { width: widthDefault },
    presets: [
      {
        key: 'default',
        label: 'Default',
        description: '',
        source: 'default',
        params: { width: widthDefault },
      },
    ],
    default_preset_key: defaultPresetKey,
  };
}

describe('StudioState', () => {
  let state: StudioState;
  let fakeViewport: {
    renderShapes: ReturnType<typeof vi.fn>;
    clear: ReturnType<typeof vi.fn>;
    setTheme: ReturnType<typeof vi.fn>;
  };

  beforeEach(() => {
    vi.clearAllMocks();
    if (typeof localStorage === 'undefined' || typeof localStorage.clear !== 'function') {
      const store = new Map<string, string>();
      vi.stubGlobal('localStorage', {
        getItem: (k: string) => store.get(k) ?? null,
        setItem: (k: string, v: string) => {
          store.set(k, String(v));
        },
        removeItem: (k: string) => {
          store.delete(k);
        },
        clear: () => {
          store.clear();
        },
        key: (i: number) => Array.from(store.keys())[i] ?? null,
        get length() {
          return store.size;
        },
      });
    } else {
      localStorage.clear();
    }
    state = new StudioState();
    state.config = {
      libraries: [{ id: 'lib', name: 'Lib', path: '/lib' }],
      python_path: '/usr/bin/python3',
      theme: 'dark',
    };
    fakeViewport = {
      renderShapes: vi.fn(),
      clear: vi.fn(),
      setTheme: vi.fn(),
    };
  });

  afterEach(() => {
    state?.destroy();
  });

  it('queues pendingRender when viewport attaches after initial render completes', async () => {
    const box = makeModel('lib/box', '/lib/box/box.py');
    mockApi.getModel.mockResolvedValueOnce(makeDetail(box, 50));
    mockApi.renderModel.mockResolvedValueOnce({
      ok: true,
      shapes: { version: 3, id: '/box' },
      build_time_ms: 12,
      tessellate_time_ms: 4,
    } satisfies RenderResponse);

    await state.selectModel('lib/box');
    expect(state.hasMesh).toBe(false);

    state.attachViewport(fakeViewport as unknown as CadViewportController);
    expect(state.hasMesh).toBe(true);
    expect(fakeViewport.renderShapes).toHaveBeenCalledWith(
      'lib/box',
      { version: 3, id: '/box' },
      true
    );
  });

  it('clears viewport and resets hasMesh on clearActiveModel and selectModel failure', async () => {
    state.attachViewport(fakeViewport as unknown as CadViewportController);
    const box = makeModel('lib/box', '/lib/box/box.py');
    mockApi.getModel.mockResolvedValueOnce(makeDetail(box, 50));
    mockApi.renderModel.mockResolvedValueOnce({ ok: true, shapes: { id: '/box' } });

    await state.selectModel('lib/box');
    expect(state.hasMesh).toBe(true);

    // clearActiveModel clears viewport & hasMesh
    state.clearActiveModel();
    expect(state.hasMesh).toBe(false);
    expect(state.activeModelId).toBeNull();
    expect(fakeViewport.clear).toHaveBeenCalledTimes(1);

    // Re-render good model, then select a broken model -> clears stale mesh
    mockApi.getModel.mockResolvedValueOnce(makeDetail(box, 50));
    mockApi.renderModel.mockResolvedValueOnce({ ok: true, shapes: { id: '/box' } });
    await state.selectModel('lib/box');
    expect(state.hasMesh).toBe(true);

    mockApi.getModel.mockResolvedValueOnce({
      ok: false,
      error: 'SyntaxError in broken.py',
      traceback: 'line 1',
    });
    await state.selectModel('lib/broken');
    expect(state.hasMesh).toBe(false);
    expect(state.modelLoadError).toBe('SyntaxError in broken.py');
    expect(fakeViewport.clear).toHaveBeenCalledTimes(2);

    // Re-render good model, then hit a build error during render -> clears stale mesh
    mockApi.getModel.mockResolvedValueOnce(makeDetail(box, 50));
    mockApi.renderModel.mockResolvedValueOnce({ ok: true, shapes: { id: '/box' } });
    await state.selectModel('lib/box');
    expect(state.hasMesh).toBe(true);

    mockApi.renderModel.mockResolvedValueOnce({
      ok: false,
      error: 'RuntimeError: fillet failed',
      traceback: 'Traceback...',
    });
    await state.triggerRender(false, true);
    expect(state.hasMesh).toBe(false);
    expect(state.status.kind).toBe('error');
    expect(fakeViewport.clear).toHaveBeenCalledTimes(3);
  });

  it('handleFileChanged filters unrelated models, sequences/coalesces rapid reloads, and refreshes defaultPresetKey', async () => {
    state.attachViewport(fakeViewport as unknown as CadViewportController);
    const box = makeModel('lib/box', '/lib/box/box.py');
    const other = makeModel('lib/other', '/lib/other/other.py');
    state.models = [box, other];

    mockApi.getModel.mockResolvedValueOnce(makeDetail(box, 50));
    mockApi.renderModel.mockResolvedValueOnce({ ok: true, shapes: { id: '/box' } });
    await state.selectModel('lib/box');

    // 1) Unrelated model change refreshes list ONLY without re-loading or re-rendering active model
    mockApi.listModels.mockResolvedValueOnce({
      ok: true,
      libraries: state.config.libraries,
      models: [box, other],
    });
    await state.handleFileChanged('/lib/other/other.py');
    expect(mockApi.listModels).toHaveBeenCalledTimes(1);
    expect(mockApi.getModel).toHaveBeenCalledTimes(1); // still just initial selectModel

    // 2) Active model / shared utils change reloads active model & refreshes defaultPresetKey
    let resolvePass1List!: (v: unknown) => void;
    mockApi.listModels
      .mockImplementationOnce(
        () =>
          new Promise((res) => {
            resolvePass1List = res;
          })
      )
      .mockResolvedValueOnce({
        ok: true,
        libraries: state.config.libraries,
        models: [box, other],
      });

    mockApi.getModel
      .mockResolvedValueOnce(makeDetail(box, 60, 'preset_a'))
      .mockResolvedValueOnce(makeDetail(box, 75, 'preset_b'));
    mockApi.renderModel
      .mockResolvedValueOnce({ ok: true, shapes: { id: '/box2' } })
      .mockResolvedValueOnce({ ok: true, shapes: { id: '/box3' } });

    const p1 = state.handleFileChanged('/lib/utils/helpers.py');
    const p2 = state.handleFileChanged('/lib/box/box.py');
    resolvePass1List({
      ok: true,
      libraries: state.config.libraries,
      models: [box, other],
    });
    await Promise.all([p1, p2]);

    // Both coalesced passes ran sequentially and latest defaultPresetKey won
    expect(state.defaultPresetKey).toBe('preset_b');
    expect(state.defaults).toEqual({ width: 75 });
  });

  it('applyUpdateDefaults refreshes defaultPresetKey and clears param memory when params updated', async () => {
    state.attachViewport(fakeViewport as unknown as CadViewportController);
    const box = makeModel('lib/box', '/lib/box/box.py');
    mockApi.getModel
      .mockResolvedValueOnce(makeDetail(box, 50, 'old_key'))
      .mockResolvedValueOnce(makeDetail(box, 80, 'new_key'));
    mockApi.renderModel
      .mockResolvedValueOnce({ ok: true, shapes: { id: '/box' } })
      .mockResolvedValueOnce({ ok: true, shapes: { id: '/box2' } });
    mockApi.updateModelDefaults.mockResolvedValueOnce({
      ok: true,
      updated: 1,
      changes: [],
      unsupported: [],
    });

    await state.selectModel('lib/box');
    state.updateParam('width', 80, 'preview');
    await state.applyUpdateDefaults('abc123');
    expect(state.defaultPresetKey).toBe('new_key');
    expect(state.params).toEqual({ width: 80 });
    expect(mockApi.renderModel).toHaveBeenCalledTimes(2);
  });

  it('applyUpdateDefaults metadata-only preserves live params and skips re-render', async () => {
    state.attachViewport(fakeViewport as unknown as CadViewportController);
    const box = makeModel('lib/box', '/lib/box/box.py');
    const detail1 = makeDetail(box, 50);
    const detail2 = {
      ...makeDetail(box, 50),
      presets: [
        {
          key: 'default',
          label: 'Renamed Base',
          description: 'Updated desc',
          source: 'default' as const,
          params: {},
        },
      ],
    };
    mockApi.getModel.mockResolvedValueOnce(detail1).mockResolvedValueOnce(detail2);
    mockApi.renderModel.mockResolvedValueOnce({ ok: true, shapes: { id: '/box' } });
    mockApi.updateModelDefaults.mockResolvedValueOnce({
      ok: true,
      updated: 0,
      default_preset_updated: true,
      changes: [],
      unsupported: [],
    });

    await state.selectModel('lib/box');
    state.updateParam('width', 77, 'preview');
    await state.applyUpdateDefaults('abc123', {
      includeParams: false,
      defaultPreset: { label: 'Renamed Base', description: 'Updated desc' },
    });

    expect(mockApi.updateModelDefaults).toHaveBeenCalledWith('lib/box', {
      params: {},
      default_preset: { label: 'Renamed Base', description: 'Updated desc' },
      dry_run: false,
      expected_sha256: 'abc123',
    });
    expect(state.presets[0]?.label).toBe('Renamed Base');
    expect(state.params).toEqual({ width: 77 });
    expect(mockApi.renderModel).toHaveBeenCalledTimes(1);
  });

  it('openUpdateSourceModal closes preset modals and records intent', () => {
    state.savePresetModalOpen = true;
    state.presetDetailsModalOpen = true;
    state.openUpdateSourceModal('metadata');
    expect(state.savePresetModalOpen).toBe(false);
    expect(state.presetDetailsModalOpen).toBe(false);
    expect(state.updateSourceIntent).toBe('metadata');
    expect(state.updateSourceModalOpen).toBe(true);
  });

  it('preserves unsaved param edits in paramMemory across model switches and supports cancelActiveBuild & skipOnboarding', async () => {
    state.attachViewport(fakeViewport as unknown as CadViewportController);
    const box = makeModel('lib/box', '/lib/box/box.py');
    const cap = makeModel('lib/cap', '/lib/cap/cap.py');

    mockApi.getModel
      .mockResolvedValueOnce(makeDetail(box, 50))
      .mockResolvedValueOnce(makeDetail(cap, 20))
      .mockResolvedValueOnce(makeDetail(box, 50));
    mockApi.renderModel.mockResolvedValue({ ok: true, shapes: { id: '/shape' } });

    await state.selectModel('lib/box');
    state.updateParam('width', 123, 'preview');

    await state.selectModel('lib/cap');
    await state.selectModel('lib/box');
    expect(state.params.width).toBe(123);

    // Reset clears paramMemory
    state.resetParams();
    expect(state.params.width).toBe(50);

    // cancelActiveBuild aborts render & clears buildStartedAt
    let rejectRender!: (err: unknown) => void;
    mockApi.renderModel.mockImplementationOnce(
      (_id: string, _p: unknown, signal?: AbortSignal) =>
        new Promise((_res, rej) => {
          rejectRender = rej;
          signal?.addEventListener('abort', () => rej(new DOMException('Aborted', 'AbortError')));
        })
    );
    mockApi.cancelRender.mockResolvedValueOnce({ ok: true, cancelled: true });

    const renderPromise = state.triggerRender(false, true);
    expect(state.rendering).toBe(true);
    expect(state.buildStartedAt).not.toBeNull();
    await state.cancelActiveBuild();
    await renderPromise.catch(() => rejectRender?.(new Error('aborted')));
    expect(mockApi.cancelRender).toHaveBeenCalledTimes(1);
    expect(state.rendering).toBe(false);
    expect(state.buildStartedAt).toBeNull();
    expect(state.status).toEqual({ kind: 'message', message: 'Build cancelled' });

    // skipOnboarding saves onboarding_skipped: true
    state.onboardingOpen = true;
    state.needsOnboarding = true;
    mockApi.saveConfig.mockResolvedValueOnce({
      config: { ...state.config, onboarding_skipped: true },
      resolved_python: '/usr/bin/python3',
      resolved_export_dir: '/exports',
      needs_onboarding: false,
      has_desktop: true,
      worker_running: true,
      worker_error: '',
    });
    mockApi.listModels.mockResolvedValueOnce({
      ok: true,
      libraries: state.config.libraries,
      models: [],
    });

    await state.skipOnboarding();
    expect(state.onboardingOpen).toBe(false);
    expect(state.needsOnboarding).toBe(false);
    expect(mockApi.saveConfig).toHaveBeenCalledWith(
      state.config.libraries,
      '/usr/bin/python3',
      '',
      { onboarding_skipped: true }
    );
  });

  it('tracks defaultExportFilename and supports resetExportFilename', async () => {
    state.attachViewport(fakeViewport as unknown as CadViewportController);
    const box = makeModel('lib/box', '/lib/box/box.py');
    mockApi.getModel.mockResolvedValueOnce(makeDetail(box, 50));
    mockApi.renderModel
      .mockResolvedValueOnce({
        ok: true,
        shapes: { id: '/box' },
        export_filename: 'box_50mm',
      })
      .mockResolvedValueOnce({
        ok: true,
        shapes: { id: '/box2' },
        export_filename: 'box_80mm',
      });

    await state.selectModel('lib/box');
    expect(state.defaultExportFilename).toBe('box_50mm');
    expect(state.exportFilename).toBe('box_50mm');
    expect(state.exportFilenameEdited).toBe(false);

    // User edits filename; subsequent render updates defaultExportFilename without clobbering user input
    state.setExportFilename('my_custom_box');
    expect(state.exportFilename).toBe('my_custom_box');
    expect(state.exportFilenameEdited).toBe(true);

    state.updateParam('width', 80, 'preview');
    await state.triggerRender(false, true);
    expect(state.defaultExportFilename).toBe('box_80mm');
    expect(state.exportFilename).toBe('my_custom_box');
    expect(state.exportFilenameEdited).toBe(true);

    // Reset restores the latest defaultExportFilename
    state.resetExportFilename();
    expect(state.exportFilename).toBe('box_80mm');
    expect(state.exportFilenameEdited).toBe(false);
  });

  it('scaffoldDemo respects options.selectModel', async () => {
    const demoModel = makeModel('demo/box', '/demo/box/box.py');
    mockApi.scaffoldModel.mockResolvedValue({
      ok: true,
      target_dir: '/demo',
      library_id: 'demo',
      model_id: 'demo/box',
    });
    mockApi.listModels.mockResolvedValue({
      ok: true,
      libraries: [{ id: 'demo', name: 'demo', path: '/demo' }],
      models: [demoModel],
    });

    // selectModel: false skips selecting model (used by onboarding wizard)
    await state.scaffoldDemo('/demo', { selectModel: false });
    expect(mockApi.getModel).not.toHaveBeenCalled();
    expect(state.activeModelId).toBeNull();

    // Default behavior selects the scaffolded model
    mockApi.getModel.mockResolvedValueOnce(makeDetail(demoModel, 40));
    mockApi.renderModel.mockResolvedValueOnce({ ok: true, shapes: { id: '/demo' } });
    await state.scaffoldDemo('/demo');
    expect(mockApi.getModel).toHaveBeenCalledWith('demo/box');
    expect(state.activeModelId).toBe('demo/box');
  });

  it('generalizes openModelInEditor and revealPath with active model defaults', async () => {
    state.hasDesktop = true;
    const box = makeModel('lib/box', '/lib/box/box.py');
    state.activeModel = box;
    mockApi.openInEditor.mockResolvedValue({ ok: true });
    mockApi.revealFile.mockResolvedValue({ ok: true });

    await state.openActiveModelInEditor();
    expect(mockApi.openInEditor).toHaveBeenLastCalledWith('/lib/box/box.py');

    await state.openModelInEditor('/lib/other/other.py');
    expect(mockApi.openInEditor).toHaveBeenLastCalledWith('/lib/other/other.py');

    await state.revealActiveModel();
    expect(mockApi.revealFile).toHaveBeenLastCalledWith('/lib/box/box.py');

    await state.revealPath('/lib/other');
    expect(mockApi.revealFile).toHaveBeenLastCalledWith('/lib/other');
  });

  it('exportActiveModel returns structured results for success, cancel, and error', async () => {
    state.hasDesktop = true;
    const noModel = await state.exportActiveModel('stl');
    expect(noModel).toEqual({ ok: false, error: 'No active model selected' });

    const box = makeModel('lib/box', '/lib/box/box.py');
    mockApi.getModel.mockResolvedValueOnce(makeDetail(box, 50));
    mockApi.renderModel.mockResolvedValueOnce({
      ok: true,
      shapes: { id: '/box' },
      export_filename: 'box_50mm',
      build_time_ms: 10,
      tessellate_time_ms: 5,
    });
    await state.selectModel('lib/box');

    // Success
    mockApi.exportModel.mockResolvedValueOnce({
      ok: true,
      cancelled: false,
      path: '/exports/box_50mm.stl',
      size: 2048,
    });
    const okRes = await state.exportActiveModel('stl', { quality: 'fine' });
    expect(okRes).toEqual({
      ok: true,
      cancelled: false,
      path: '/exports/box_50mm.stl',
      size: 2048,
    });
    expect(state.lastExportPath).toBe('/exports/box_50mm.stl');
    expect(state.status).toEqual({ kind: 'message', message: 'Exported box_50mm.stl (2 KB)' });
    expect(state.exporting).toBe(false);

    // Cancelled preserves previous status & lastExportPath silently
    mockApi.exportModel.mockResolvedValueOnce({ ok: true, cancelled: true });
    const cancelRes = await state.exportActiveModel('step');
    expect(cancelRes).toEqual({ ok: true, cancelled: true });
    expect(state.lastExportPath).toBe('/exports/box_50mm.stl');
    expect(state.status).toEqual({ kind: 'message', message: 'Exported box_50mm.stl (2 KB)' });

    // Error response
    mockApi.exportModel.mockResolvedValueOnce({
      ok: false,
      error: 'Geometry invalid',
      traceback: 'Traceback...',
    });
    const errRes = await state.exportActiveModel('3mf');
    expect(errRes).toEqual({
      ok: false,
      error: 'Geometry invalid',
      traceback: 'Traceback...',
    });
    expect(state.lastExportPath).toBe('');
    expect(state.status).toEqual({
      kind: 'error',
      error: 'Geometry invalid',
      traceback: 'Traceback...',
    });
  });
});
