import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => {
  const disposeOrder: string[] = [];
  const state = {
    lastViewerInstance: null as MockViewer | null,
    lastDisplayInstance: null as MockDisplay | null,
    currentContainerEl: null as HTMLElement | null,
  };

  class MockDisplay {
    lastPlaneState = false;
    cadTool: { container: HTMLElement } | null = null;
    setTheme = vi.fn();
    dispose = vi.fn(() => {
      disposeOrder.push('display');
    });

    constructor(container: HTMLElement, _options: Record<string, unknown>) {
      state.currentContainerEl = container;
      state.lastDisplayInstance = this;
      container.innerHTML = `
        <select class="tcv_studio_environment">
          <optgroup label="Studio">
            <option value="studio">Studio</option>
            <option value="polyhaven_1">Poly Haven 1</option>
          </optgroup>
          <optgroup label="Outdoor">
            <option value="polyhaven_outdoor">Outdoor HDR</option>
          </optgroup>
        </select>
        <div class="tcv_studio_4k_row">4K</div>
      `;
    }
  }

  class MockViewer {
    cadWidth: number;
    height: number;
    treeWidth: number;
    gridSize = 100;
    lastRenderArgs: {
      shapes: unknown;
      renderOptions: Record<string, unknown>;
      viewerOptions: Record<string, unknown>;
    } | null = null;
    canvas: HTMLCanvasElement;
    notifyCallback: ((change: Record<string, { old: unknown; new: unknown }>) => void) | null;
    stateMap = new Map<string, unknown>([
      ['activeTab', 'tree'],
      ['centerGrid', true],
      ['collapse', 1],
    ]);
    state = {
      get: (k: string) => this.stateMap.get(k),
    };
    envManager: {
      _presetUrls: Record<string, string>;
      _resolveUrl: (name: string) => string | null;
      _load: (name: string, renderer: unknown) => Promise<unknown>;
      _loadRoomEnvironment: ReturnType<typeof vi.fn>;
      _currentTexture: object;
      setUse4kEnvMaps: (use4k: boolean) => Promise<unknown>;
      isPreset: (name: string) => boolean;
    };

    cameraSettings = {
      position: [10, 20, 30],
      quaternion: [0, 0, 0, 1],
      target: [1, 2, 3],
      zoom: 1.75,
    };
    states: Record<string, [number, number]> = {
      '/root/partA': [1, 1],
      '/root/partB': [1, 1],
    };
    nextRenderedStates: Record<string, [number, number]> | null = null;
    clipSliders: [number, number, number] = [50, 50, 50];
    clipNormals: [[number, number, number], [number, number, number], [number, number, number]] = [
      [-1, 0, 0],
      [0, -1, 0],
      [0, 0, -1],
    ];
    clipIntersection = false;
    clipObjectColors = false;
    clipPlaneHelpers = false;
    ortho = true;
    axes = true;
    axes0 = true;
    grids: [boolean, boolean, boolean] = [true, false, false];
    transparent = false;
    blackEdges = false;
    edgeColor = 0x555555;
    ambientLight = 1.0;
    directLight = 1.1;
    metalness = 0.3;
    roughness = 0.65;
    opacity = 0.5;
    zebraCount = 9;
    zebraOpacity = 1.0;
    zebraDirection = 0;
    zebraColorScheme = 'blackwhite';
    zebraMappingMode = 'reflection';
    studioEnvIntensity = 0.5;
    studioBackground = 'environment';
    studioToneMapping = 'neutral';
    studioExposure = 1.0;
    studioTextureMapping = 'triplanar';
    studioEnvRotation = 0;
    studioShadowIntensity = 0;
    studioShadowSoftness = 0.3;
    studioAOIntensity = 0;

    clear = vi.fn();
    setTheme = vi.fn();
    resizeCadView = vi.fn((cw: number, tw: number, h: number) => {
      this.cadWidth = cw;
      this.treeWidth = tw;
      this.height = h;
    });
    render = vi.fn(
      (
        shapes: unknown,
        renderOptions: Record<string, unknown>,
        viewerOptions: Record<string, unknown>
      ) => {
        this.lastRenderArgs = { shapes, renderOptions, viewerOptions };
        if (this.nextRenderedStates) {
          this.states = { ...this.nextRenderedStates };
          this.nextRenderedStates = null;
        }
      }
    );
    setStates = vi.fn((s: Record<string, [number, number]>) => {
      this.states = { ...s };
    });
    getStates = vi.fn(() => ({ ...this.states }));
    getCameraLocationSettings = vi.fn(() => ({ ...this.cameraSettings }));
    getClipSlider = vi.fn((i: 0 | 1 | 2) => this.clipSliders[i]);
    getClipNormal = vi.fn((i: 0 | 1 | 2) => this.clipNormals[i]);
    getClipIntersection = vi.fn(() => this.clipIntersection);
    getObjectColorCaps = vi.fn(() => this.clipObjectColors);
    getClipPlaneHelpers = vi.fn(() => this.clipPlaneHelpers);
    getOrtho = vi.fn(() => this.ortho);
    getAxes = vi.fn(() => this.axes);
    getAxes0 = vi.fn(() => this.axes0);
    getGrids = vi.fn(() => this.grids);
    getTransparent = vi.fn(() => this.transparent);
    getBlackEdges = vi.fn(() => this.blackEdges);
    getEdgeColor = vi.fn(() => this.edgeColor);
    getAmbientLight = vi.fn(() => this.ambientLight);
    getDirectLight = vi.fn(() => this.directLight);
    getMetalness = vi.fn(() => this.metalness);
    getRoughness = vi.fn(() => this.roughness);
    getOpacity = vi.fn(() => this.opacity);
    getZebraCount = vi.fn(() => this.zebraCount);
    getZebraOpacity = vi.fn(() => this.zebraOpacity);
    getZebraDirection = vi.fn(() => this.zebraDirection);
    getZebraColorScheme = vi.fn(() => this.zebraColorScheme);
    getZebraMappingMode = vi.fn(() => this.zebraMappingMode);
    getStudioEnvIntensity = vi.fn(() => this.studioEnvIntensity);
    getStudioBackground = vi.fn(() => this.studioBackground);
    getStudioToneMapping = vi.fn(() => this.studioToneMapping);
    getStudioExposure = vi.fn(() => this.studioExposure);
    getStudioTextureMapping = vi.fn(() => this.studioTextureMapping);
    getStudioEnvRotation = vi.fn(() => this.studioEnvRotation);
    getStudioShadowIntensity = vi.fn(() => this.studioShadowIntensity);
    getStudioShadowSoftness = vi.fn(() => this.studioShadowSoftness);
    getStudioAOIntensity = vi.fn(() => this.studioAOIntensity);
    getCanvas = vi.fn(() => this.canvas);
    dispose = vi.fn(() => {
      disposeOrder.push('viewer');
    });

    constructor(
      _display: MockDisplay,
      options: { cadWidth: number; height: number; treeWidth: number },
      notifyCallback: ((change: Record<string, { old: unknown; new: unknown }>) => void) | null
    ) {
      state.lastViewerInstance = this;
      this.cadWidth = options.cadWidth;
      this.height = options.height;
      this.treeWidth = options.treeWidth;
      this.notifyCallback = notifyCallback;
      this.canvas = document.createElement('canvas');
      if (state.currentContainerEl) {
        state.currentContainerEl.appendChild(this.canvas);
      }
      this.envManager = {
        _presetUrls: { polyhaven_1: 'https://dl.polyhaven.org/test.hdr' },
        _resolveUrl: (name: string) => `https://dl.polyhaven.org/${name}.hdr`,
        _load: async () => null,
        _loadRoomEnvironment: vi.fn(async () => ({ room: true })),
        _currentTexture: { tex: true },
        setUse4kEnvMaps: async () => null,
        isPreset: () => true,
      };
    }
  }

  return { MockDisplay, MockViewer, disposeOrder, state };
});

vi.mock('three-cad-viewer/css', () => ({}));
vi.mock('three-cad-viewer', () => ({
  Display: mocks.MockDisplay,
  Viewer: mocks.MockViewer,
}));

import { CadViewportController } from './cadViewer';

describe('CadViewportController', () => {
  let container: HTMLDivElement;
  let rectWidth = 800;

  beforeEach(() => {
    vi.useFakeTimers();
    localStorage.clear();
    mocks.disposeOrder.length = 0;
    mocks.state.lastViewerInstance = null;
    mocks.state.lastDisplayInstance = null;
    rectWidth = 800;
    container = document.createElement('div');
    vi.spyOn(container, 'getBoundingClientRect').mockImplementation(() => ({
      width: rectWidth,
      height: 600,
      top: 0,
      left: 0,
      right: rectWidth,
      bottom: 600,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    }));
    document.body.appendChild(container);
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe = vi.fn();
        disconnect = vi.fn();
      }
    );
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
    container.remove();
  });

  it('preserves camera, clip sliders (guarding open sliders on growth), lastPlaneState, and diffed tree states on same-model re-render', () => {
    const ctrl = new CadViewportController(container, 'dark');
    ctrl.renderShapes('lib/box', { v: 1 }, true);

    const v = mocks.state.lastViewerInstance!;
    const d = mocks.state.lastDisplayInstance!;
    expect(v.lastRenderArgs?.viewerOptions).toMatchObject({
      position: null,
      quaternion: null,
      target: null,
      zoom: 1,
      tab: 'tree',
    });
    // Dimensions match constructor: resizeCadView should NOT be called
    expect(v.resizeCadView).not.toHaveBeenCalled();

    // Simulate user inspection state on same model
    v.stateMap.set('activeTab', 'material');
    d.lastPlaneState = true; // remembered while not on 'clip' tab
    v.ortho = false;
    v.blackEdges = true;
    v.gridSize = 100; // prevMid = 50
    v.clipSliders = [12.5, 50, 49.9999995]; // slider0 is closed (< 50), slider1 & 2 are open
    v.clipNormals = [
      [1, 0, 0],
      [0, -1, 0],
      [0, 0, -1],
    ];
    v.clipIntersection = true;
    v.clipObjectColors = true;
    v.states = { '/root/partA': [0, 1], '/root/partB': [1, 1] };
    // After Viewer.render() resets new tree defaults to [1, 1], partA differs -> setStates must run
    v.nextRenderedStates = { '/root/partA': [1, 1], '/root/partB': [1, 1] };

    ctrl.renderShapes('lib/box', { v: 2 }, false);
    expect(v.clear).toHaveBeenCalledTimes(1);
    expect(v.lastRenderArgs?.viewerOptions).toMatchObject({
      position: [10, 20, 30],
      quaternion: [0, 0, 0, 1],
      target: [1, 2, 3],
      zoom: 1.75,
      tab: 'material',
      ortho: false,
      blackEdges: true,
      clipSlider0: 12.5,
      clipSlider1: undefined,
      clipSlider2: undefined,
      clipNormal0: [1, 0, 0],
      clipIntersection: true,
      clipObjectColors: true,
      clipPlaneHelpers: true,
    });
    expect(v.setStates).toHaveBeenCalledTimes(1);
    expect(v.setStates).toHaveBeenCalledWith({ '/root/partA': [0, 1], '/root/partB': [1, 1] });
    expect(v.resizeCadView).not.toHaveBeenCalled();

    // Re-render when container width changed -> resizeCadView IS called
    rectWidth = 1000;
    v.nextRenderedStates = { '/root/partA': [0, 1], '/root/partB': [1, 1] };
    ctrl.renderShapes('lib/box', { v: 3 }, false);
    expect(v.setStates).toHaveBeenCalledTimes(1); // diffed: no change
    expect(v.resizeCadView).toHaveBeenCalledTimes(1);

    // Switching to another model resets camera/zoom/tab cleanly
    ctrl.renderShapes('lib/other', { v: 4 }, false);
    expect(v.lastRenderArgs?.viewerOptions).toMatchObject({
      position: null,
      quaternion: null,
      target: null,
      zoom: 1,
      tab: 'tree',
    });

    ctrl.dispose();
  });

  it('blocks Poly Haven HDRI requests and cleans studio dropdown DOM', async () => {
    const ctrl = new CadViewportController(container, 'dark');
    ctrl.renderShapes('lib/box', { v: 1 }, true);
    const v = mocks.state.lastViewerInstance!;

    expect(v.envManager._presetUrls).toEqual({});
    expect(v.envManager._resolveUrl('studio_small')).toBeNull();
    expect(v.envManager.isPreset('studio_small')).toBe(false);
    await v.envManager._load('polyhaven_1', {});
    expect(v.envManager._loadRoomEnvironment).toHaveBeenCalledWith('studio', {});

    const options = Array.from(container.querySelectorAll('.tcv_studio_environment option')).map(
      (o) => (o as HTMLOptionElement).value
    );
    expect(options).toEqual(['studio']);
    expect(container.querySelectorAll('.tcv_studio_environment optgroup')).toHaveLength(1);
    expect((container.querySelector('.tcv_studio_4k_row') as HTMLElement).style.display).toBe('none');

    ctrl.dispose();
  });

  it('handles clear(), disposes viewer BEFORE display, detaches canvas listeners, and handles webglcontextlost', () => {
    const onContextLost = vi.fn();
    const onContextRestored = vi.fn();
    const ctrl = new CadViewportController(container, 'dark', {
      onContextLost,
      onContextRestored,
    });
    ctrl.renderShapes('lib/box', { v: 1 }, true);
    const v = mocks.state.lastViewerInstance!;

    // Trigger webglcontextlost
    const lostEvt = new Event('webglcontextlost', { cancelable: true });
    v.canvas.dispatchEvent(lostEvt);
    expect(lostEvt.defaultPrevented).toBe(true);
    expect(onContextLost).toHaveBeenCalledTimes(1);

    // Trigger webglcontextrestored
    v.canvas.dispatchEvent(new Event('webglcontextrestored'));
    expect(onContextRestored).toHaveBeenCalledTimes(1);

    // clear() invokes viewer.clear()
    ctrl.clear();
    expect(v.clear).toHaveBeenCalledTimes(1);

    // dispose() calls viewer.dispose() BEFORE display.dispose() and detaches listeners
    ctrl.dispose();
    expect(mocks.disposeOrder).toEqual(['viewer', 'display']);

    v.canvas.dispatchEvent(new Event('webglcontextlost', { cancelable: true }));
    expect(onContextLost).toHaveBeenCalledTimes(1); // detached
  });

  it('persists user display/material prefs via notifyCallback while ignoring studio_environment', () => {
    const ctrl = new CadViewportController(container, 'dark');
    ctrl.renderShapes('lib/box', { v: 1 }, true);
    const v = mocks.state.lastViewerInstance!;

    v.notifyCallback?.({
      ortho: { old: true, new: false },
      metalness: { old: 0.3, new: 0.8 },
      studio_environment: { old: 'studio', new: 'polyhaven_ignored' },
    });

    vi.advanceTimersByTime(200);
    const saved = JSON.parse(localStorage.getItem('code-cad-viewer-prefs') || '{}') as Record<
      string,
      unknown
    >;
    expect(saved.ortho).toBe(false);
    expect(saved.metalness).toBe(0.8);
    expect(saved.studioEnvironment).toBeUndefined();

    // New controller reads persisted prefs on cold start
    ctrl.dispose();
    const ctrl2 = new CadViewportController(container, 'dark');
    ctrl2.renderShapes('lib/other', { v: 2 }, true);
    expect(mocks.state.lastViewerInstance!.lastRenderArgs?.viewerOptions.ortho).toBe(false);
    expect(mocks.state.lastViewerInstance!.lastRenderArgs?.renderOptions.metalness).toBe(0.8);
    ctrl2.dispose();
  });
});
