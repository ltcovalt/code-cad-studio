import 'three-cad-viewer/css';
import { Display, Viewer } from 'three-cad-viewer';
import type {
  ActiveTab,
  ChangeNotification,
  QuaternionTuple,
  RenderOptions,
  Shapes,
  StudioBackground,
  StudioTextureMapping,
  StudioToneMapping,
  Theme,
  Vector3Tuple,
  ViewerOptions,
  VisibilityState,
  ZebraColorScheme,
  ZebraMappingMode,
  ZebraOptions,
} from 'three-cad-viewer';
import { LIGHT_THEMES } from './themes';

const VIEWER_PREFS_STORAGE_KEY = 'code-cad-viewer-prefs';

export interface ViewerDisplayPrefs {
  axes: boolean;
  axes0: boolean;
  grid: [boolean, boolean, boolean];
  centerGrid: boolean;
  ortho: boolean;
  transparent: boolean;
  blackEdges: boolean;
  ambientIntensity: number;
  directIntensity: number;
  metalness: number;
  roughness: number;
  edgeColor: number;
  defaultOpacity: number;
  zebraCount: number;
  zebraOpacity: number;
  zebraDirection: number;
  zebraColorScheme: ZebraColorScheme;
  zebraMappingMode: ZebraMappingMode;
  studioEnvIntensity: number;
  studioBackground: StudioBackground;
  studioToneMapping: StudioToneMapping;
  studioExposure: number;
  studioTextureMapping: StudioTextureMapping;
  studioEnvRotation: number;
  studioShadowIntensity: number;
  studioShadowSoftness: number;
  studioAOIntensity: number;
}

const DEFAULT_DISPLAY_PREFS: ViewerDisplayPrefs = {
  axes: true,
  axes0: true,
  grid: [true, false, false],
  centerGrid: true,
  ortho: true,
  transparent: false,
  blackEdges: false,
  ambientIntensity: 1.0,
  directIntensity: 1.1,
  metalness: 0.3,
  roughness: 0.65,
  edgeColor: 0x555555,
  defaultOpacity: 0.5,
  zebraCount: 9,
  zebraOpacity: 1.0,
  zebraDirection: 0,
  zebraColorScheme: 'blackwhite',
  zebraMappingMode: 'reflection',
  studioEnvIntensity: 0.5,
  studioBackground: 'environment',
  studioToneMapping: 'neutral',
  studioExposure: 1.0,
  studioTextureMapping: 'triplanar',
  studioEnvRotation: 0,
  studioShadowIntensity: 0,
  studioShadowSoftness: 0.3,
  studioAOIntensity: 0,
};

const VALID_ZEBRA_SCHEMES = new Set<ZebraColorScheme>(['blackwhite', 'colorful', 'grayscale']);
const VALID_ZEBRA_MODES = new Set<ZebraMappingMode>(['reflection', 'normal']);
const VALID_STUDIO_BG = new Set<StudioBackground>([
  'grey',
  'darkgrey',
  'white',
  'gradient',
  'gradient-dark',
  'environment',
  'transparent',
]);
const VALID_TONE_MAPPING = new Set<StudioToneMapping>(['neutral', 'ACES', 'none']);
const VALID_TEX_MAPPING = new Set<StudioTextureMapping>(['triplanar', 'parametric']);

function loadDisplayPrefs(): ViewerDisplayPrefs {
  const prefs: ViewerDisplayPrefs = {
    ...DEFAULT_DISPLAY_PREFS,
    grid: [...DEFAULT_DISPLAY_PREFS.grid],
  };
  try {
    const raw = localStorage.getItem(VIEWER_PREFS_STORAGE_KEY);
    if (!raw) return prefs;
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return prefs;
    const obj = parsed as Record<string, unknown>;

    if (typeof obj.axes === 'boolean') prefs.axes = obj.axes;
    if (typeof obj.axes0 === 'boolean') prefs.axes0 = obj.axes0;
    if (
      Array.isArray(obj.grid) &&
      obj.grid.length === 3 &&
      obj.grid.every((v) => typeof v === 'boolean')
    ) {
      prefs.grid = [obj.grid[0], obj.grid[1], obj.grid[2]];
    }
    if (typeof obj.centerGrid === 'boolean') prefs.centerGrid = obj.centerGrid;
    if (typeof obj.ortho === 'boolean') prefs.ortho = obj.ortho;
    if (typeof obj.transparent === 'boolean') prefs.transparent = obj.transparent;
    if (typeof obj.blackEdges === 'boolean') prefs.blackEdges = obj.blackEdges;

    const numKeys = [
      'ambientIntensity',
      'directIntensity',
      'metalness',
      'roughness',
      'edgeColor',
      'defaultOpacity',
      'zebraCount',
      'zebraOpacity',
      'zebraDirection',
      'studioEnvIntensity',
      'studioExposure',
      'studioEnvRotation',
      'studioShadowIntensity',
      'studioShadowSoftness',
      'studioAOIntensity',
    ] as const;
    for (const k of numKeys) {
      if (typeof obj[k] === 'number' && Number.isFinite(obj[k])) {
        prefs[k] = obj[k];
      }
    }

    if (typeof obj.zebraColorScheme === 'string' && VALID_ZEBRA_SCHEMES.has(obj.zebraColorScheme as ZebraColorScheme)) {
      prefs.zebraColorScheme = obj.zebraColorScheme as ZebraColorScheme;
    }
    if (typeof obj.zebraMappingMode === 'string' && VALID_ZEBRA_MODES.has(obj.zebraMappingMode as ZebraMappingMode)) {
      prefs.zebraMappingMode = obj.zebraMappingMode as ZebraMappingMode;
    }
    if (typeof obj.studioBackground === 'string' && VALID_STUDIO_BG.has(obj.studioBackground as StudioBackground)) {
      prefs.studioBackground = obj.studioBackground as StudioBackground;
    }
    if (typeof obj.studioToneMapping === 'string' && VALID_TONE_MAPPING.has(obj.studioToneMapping as StudioToneMapping)) {
      prefs.studioToneMapping = obj.studioToneMapping as StudioToneMapping;
    }
    if (typeof obj.studioTextureMapping === 'string' && VALID_TEX_MAPPING.has(obj.studioTextureMapping as StudioTextureMapping)) {
      prefs.studioTextureMapping = obj.studioTextureMapping as StudioTextureMapping;
    }
  } catch {
    // ignore storage errors
  }
  return prefs;
}

const NOTIFICATION_TO_PREF_KEY: Record<string, keyof ViewerDisplayPrefs> = {
  axes: 'axes',
  axes0: 'axes0',
  grid: 'grid',
  center_grid: 'centerGrid',
  ortho: 'ortho',
  transparent: 'transparent',
  black_edges: 'blackEdges',
  ambient_intensity: 'ambientIntensity',
  direct_intensity: 'directIntensity',
  metalness: 'metalness',
  roughness: 'roughness',
  default_edgecolor: 'edgeColor',
  default_opacity: 'defaultOpacity',
  zebra_count: 'zebraCount',
  zebra_opacity: 'zebraOpacity',
  zebra_direction: 'zebraDirection',
  zebra_color_scheme: 'zebraColorScheme',
  zebra_mapping_mode: 'zebraMappingMode',
  studio_env_intensity: 'studioEnvIntensity',
  studio_background: 'studioBackground',
  studio_tone_mapping: 'studioToneMapping',
  studio_exposure: 'studioExposure',
  studio_texture_mapping: 'studioTextureMapping',
  studio_env_rotation: 'studioEnvRotation',
  studio_shadow_intensity: 'studioShadowIntensity',
  studio_shadow_softness: 'studioShadowSoftness',
  studio_ao_intensity: 'studioAOIntensity',
};

function toVec3(val: unknown): Vector3Tuple | null {
  if (Array.isArray(val) && val.length === 3 && val.every((n) => typeof n === 'number' && Number.isFinite(n))) {
    return [val[0], val[1], val[2]];
  }
  return null;
}

function toQuat(val: unknown): QuaternionTuple | null {
  if (Array.isArray(val) && val.length === 4 && val.every((n) => typeof n === 'number' && Number.isFinite(n))) {
    return [val[0], val[1], val[2], val[3]];
  }
  return null;
}

export interface CadViewportCallbacks {
  onContextLost?: () => void;
  onContextRestored?: () => void;
  onUserChange?: (change: ChangeNotification) => void;
}

export class CadViewportController {
  private container: HTMLElement;
  private display: Display | null = null;
  private viewer: Viewer | null = null;
  private hasRendered = false;
  private lastModelId: string | null = null;
  private theme: Theme = 'dark';
  private treeWidth = 220;
  private resizeObserver: ResizeObserver | null = null;
  private rafId: number | null = null;
  private prefs: ViewerDisplayPrefs = loadDisplayPrefs();
  private prefsSaveTimer: ReturnType<typeof setTimeout> | null = null;
  private suppressPrefsNotify = false;
  private contextLost = false;
  private canvasEl: Element | null = null;
  private callbacks?: CadViewportCallbacks;

  constructor(
    container: HTMLElement,
    initialDaisyTheme = 'dark',
    callbacks?: CadViewportCallbacks
  ) {
    this.container = container;
    this.callbacks = callbacks;
    this.theme = LIGHT_THEMES.has(initialDaisyTheme || 'dark') ? 'light' : 'dark';
    this.container.setAttribute('data-theme', this.theme);
    this.initResizeObserver();
  }

  private onContextLost = (evt: Event): void => {
    evt.preventDefault();
    this.contextLost = true;
    this.callbacks?.onContextLost?.();
  };

  private onContextRestored = (): void => {
    this.contextLost = false;
    this.callbacks?.onContextRestored?.();
  };

  private attachCanvasListeners(): void {
    if (!this.viewer || typeof this.viewer.getCanvas !== 'function') return;
    const canvas = this.viewer.getCanvas();
    if (!canvas) return;
    this.detachCanvasListeners();
    this.canvasEl = canvas;
    canvas.addEventListener('webglcontextlost', this.onContextLost);
    canvas.addEventListener('webglcontextrestored', this.onContextRestored);
  }

  private detachCanvasListeners(): void {
    if (!this.canvasEl) return;
    this.canvasEl.removeEventListener('webglcontextlost', this.onContextLost);
    this.canvasEl.removeEventListener('webglcontextrestored', this.onContextRestored);
    this.canvasEl = null;
  }

  private scheduleSavePrefs(): void {
    if (this.prefsSaveTimer) clearTimeout(this.prefsSaveTimer);
    this.prefsSaveTimer = setTimeout(() => {
      this.prefsSaveTimer = null;
      this.flushSavePrefs();
    }, 150);
  }

  private flushSavePrefs(): void {
    try {
      localStorage.setItem(VIEWER_PREFS_STORAGE_KEY, JSON.stringify(this.prefs));
    } catch {
      // ignore storage errors
    }
  }

  private handleViewerNotify(change: ChangeNotification): void {
    if (this.suppressPrefsNotify) return;
    let changed = false;
    for (const [notifyKey, entry] of Object.entries(change)) {
      if (!entry || entry.new === undefined) continue;
      // Never persist external HDRI environments or 4K map flags
      if (notifyKey === 'studio_environment' || notifyKey === 'studio_4k_env_maps') continue;
      const prefKey = NOTIFICATION_TO_PREF_KEY[notifyKey];
      if (!prefKey) continue;
      if (prefKey === 'grid') {
        const g = entry.new;
        if (Array.isArray(g) && g.length === 3 && g.every((v) => typeof v === 'boolean')) {
          this.prefs.grid = [g[0], g[1], g[2]];
          changed = true;
        }
      } else {
        (this.prefs as unknown as Record<string, unknown>)[prefKey] = entry.new;
        changed = true;
      }
    }
    if (changed) {
      this.scheduleSavePrefs();
    }
    this.callbacks?.onUserChange?.(change);
  }

  private blockExternalEnvironments(): void {
    if (!this.viewer) return;
    const envMgr = (this.viewer as unknown as {
      envManager?: {
        _presetUrls?: Record<string, string>;
        _resolveUrl?: (name: string) => string | null;
        _load?: (name: string, renderer: unknown) => Promise<unknown>;
        _loadRoomEnvironment?: (name: string, renderer: unknown) => Promise<unknown>;
        _currentTexture?: unknown;
        setUse4kEnvMaps?: (use4k: boolean) => Promise<unknown>;
        isPreset?: (name: string) => boolean;
      };
    }).envManager;

    if (envMgr) {
      envMgr._presetUrls = {};
      envMgr._resolveUrl = () => null;
      envMgr._load = async (_name: string, renderer: unknown) => {
        if (typeof envMgr._loadRoomEnvironment === 'function') {
          return envMgr._loadRoomEnvironment('studio', renderer);
        }
        return null;
      };
      envMgr.setUse4kEnvMaps = async () => envMgr._currentTexture ?? null;
      envMgr.isPreset = () => false;
    }

    const envSelect = this.container.querySelector('.tcv_studio_environment');
    if (envSelect) {
      for (const opt of Array.from(envSelect.querySelectorAll('option'))) {
        if (opt.value !== 'studio') {
          opt.remove();
        }
      }
      for (const group of Array.from(envSelect.querySelectorAll('optgroup'))) {
        if (group.querySelectorAll('option').length === 0) {
          group.remove();
        }
      }
    }

    const row4k = this.container.querySelector('.tcv_studio_4k_row') as HTMLElement | null;
    if (row4k) {
      row4k.style.display = 'none';
    }
  }

  private getDimensions(): { cadWidth: number; height: number; treeWidth: number } {
    const rect = this.container.getBoundingClientRect();
    const width = Math.max(240, Math.floor(rect.width));
    const height = Math.max(200, Math.floor(rect.height));
    const toolbarEl = (this.display as unknown as { cadTool?: { container?: HTMLElement } })?.cadTool
      ?.container;
    const toolbarH = toolbarEl && toolbarEl.offsetHeight > 0 ? toolbarEl.offsetHeight : 36;
    const cadWidth = Math.max(200, width - 2);
    const cadHeight = Math.max(160, height - toolbarH - 6);
    return {
      cadWidth,
      height: cadHeight,
      treeWidth: Math.min(this.treeWidth, Math.floor(cadWidth * 0.3)),
    };
  }

  private syncSize(): void {
    if (!this.viewer || !this.hasRendered) return;
    const { cadWidth, height, treeWidth } = this.getDimensions();
    if (
      this.viewer.cadWidth === cadWidth &&
      this.viewer.height === height &&
      this.viewer.treeWidth === treeWidth
    ) {
      return;
    }
    try {
      this.viewer.resizeCadView(cadWidth, treeWidth, height, true);
    } catch (err) {
      console.warn('resizeCadView warning:', err);
    }
  }

  private ensureInitialized(): void {
    if (this.viewer) return;
    const { cadWidth, height, treeWidth } = this.getDimensions();

    const displayOptions = {
      cadWidth,
      height,
      treeWidth,
      theme: this.theme,
      glass: true,
      tools: true,
      pinning: false,
      measureTools: true,
      selectTool: true,
      explodeTool: true,
      zebraTool: true,
      studioTool: true,
      zscaleTool: false,
      externalMeasurementBackend: false,
    };

    this.display = new Display(this.container, displayOptions);
    // three-cad-viewer's Display.setTheme() writes data-theme="light"|"dark" to
    // document.body (including on every viewer.render()), which shadows the active
    // daisyUI theme on <html>. Keep #cad-viewer-root on light|dark and clear the
    // body override so <html> remains the single source of truth for the app theme.
    const originalSetTheme = this.display.setTheme.bind(this.display);
    this.display.setTheme = (theme) => {
      try {
        return originalSetTheme(theme);
      } finally {
        document.body.removeAttribute('data-theme');
      }
    };
    this.viewer = new Viewer(this.display, displayOptions, (change) =>
      this.handleViewerNotify(change)
    );
    this.blockExternalEnvironments();
    this.attachCanvasListeners();
    document.body.removeAttribute('data-theme');
  }

  private initResizeObserver(): void {
    this.resizeObserver = new ResizeObserver(() => {
      if (!this.viewer || !this.hasRendered) return;
      if (this.rafId !== null) cancelAnimationFrame(this.rafId);
      this.rafId = requestAnimationFrame(() => {
        this.rafId = null;
        this.syncSize();
      });
    });
    this.resizeObserver.observe(this.container);
  }

  setTheme(daisyTheme: string): void {
    const mode: Theme = LIGHT_THEMES.has(daisyTheme || 'dark') ? 'light' : 'dark';
    this.theme = mode;
    if (this.viewer && this.hasRendered) {
      try {
        this.viewer.setTheme(mode);
      } catch (err) {
        console.warn('setTheme warning:', err);
      }
    }
    this.container.setAttribute('data-theme', mode);
    document.body.removeAttribute('data-theme');
  }

  renderShapes(modelId: string, shapes: unknown, resetCamera = false): void {
    this.ensureInitialized();
    if (!this.viewer) return;

    const preserveState =
      this.hasRendered && !resetCamera && this.lastModelId === modelId && !this.contextLost;

    let savedStates: Record<string, VisibilityState> | null = null;
    let renderOptions: RenderOptions;
    let viewerOptions: ViewerOptions & ZebraOptions;

    if (preserveState) {
      let savedCam: ReturnType<Viewer['getCameraLocationSettings']> | null = null;
      try {
        savedCam = this.viewer.getCameraLocationSettings();
      } catch {
        savedCam = null;
      }
      try {
        savedStates = this.viewer.getStates();
      } catch {
        savedStates = null;
      }

      const activeTab =
        (this.viewer.state?.get('activeTab') as ActiveTab | undefined) ?? 'tree';
      const prevMid = this.viewer.gridSize > 0 ? this.viewer.gridSize / 2 : 0;

      const clipSlider = (idx: 0 | 1 | 2): number | undefined => {
        const v = this.viewer!.getClipSlider(idx);
        if (typeof v === 'number' && Number.isFinite(v) && v !== -1 && prevMid > 0 && v < prevMid - 1e-6) {
          return v;
        }
        return undefined;
      };

      const clipPlaneHelpers =
        activeTab === 'clip'
          ? this.viewer.getClipPlaneHelpers()
          : Boolean((this.display as unknown as { lastPlaneState?: boolean } | null)?.lastPlaneState);

      renderOptions = {
        edgeColor: this.viewer.getEdgeColor(),
        ambientIntensity: this.viewer.getAmbientLight(),
        directIntensity: this.viewer.getDirectLight(),
        metalness: this.viewer.getMetalness(),
        roughness: this.viewer.getRoughness(),
        defaultOpacity: this.viewer.getOpacity(),
      };

      const camPos = savedCam ? toVec3(savedCam.position) : null;
      const camQuat = savedCam ? toQuat(savedCam.quaternion) : null;
      const camTarget = savedCam ? toVec3(savedCam.target) : null;
      const camZoom =
        savedCam && typeof savedCam.zoom === 'number' && Number.isFinite(savedCam.zoom)
          ? savedCam.zoom
          : 1;

      viewerOptions = {
        control: 'orbit',
        up: 'Z',
        axes: this.viewer.getAxes(),
        axes0: this.viewer.getAxes0(),
        grid: this.viewer.getGrids(),
        centerGrid: Boolean(this.viewer.state?.get('centerGrid') ?? this.prefs.centerGrid),
        ortho: this.viewer.getOrtho(),
        transparent: this.viewer.getTransparent(),
        blackEdges: this.viewer.getBlackEdges(),
        collapse: Number(this.viewer.state?.get('collapse') ?? 1),
        tab: activeTab,
        position: camPos,
        quaternion: camQuat,
        target: camTarget,
        zoom: camZoom,
        clipNormal0: this.viewer.getClipNormal(0),
        clipNormal1: this.viewer.getClipNormal(1),
        clipNormal2: this.viewer.getClipNormal(2),
        clipSlider0: clipSlider(0),
        clipSlider1: clipSlider(1),
        clipSlider2: clipSlider(2),
        clipIntersection: this.viewer.getClipIntersection(),
        clipObjectColors: this.viewer.getObjectColorCaps(),
        clipPlaneHelpers,
        zebraCount: this.viewer.getZebraCount(),
        zebraOpacity: this.viewer.getZebraOpacity(),
        zebraDirection: this.viewer.getZebraDirection(),
        zebraColorScheme: this.viewer.getZebraColorScheme(),
        zebraMappingMode: this.viewer.getZebraMappingMode(),
        studioEnvironment: 'studio',
        studio4kEnvMaps: false,
        studioEnvIntensity: this.viewer.getStudioEnvIntensity(),
        studioBackground: this.viewer.getStudioBackground(),
        studioToneMapping: this.viewer.getStudioToneMapping(),
        studioExposure: this.viewer.getStudioExposure(),
        studioTextureMapping: this.viewer.getStudioTextureMapping(),
        studioEnvRotation: this.viewer.getStudioEnvRotation(),
        studioShadowIntensity: this.viewer.getStudioShadowIntensity(),
        studioShadowSoftness: this.viewer.getStudioShadowSoftness(),
        studioAOIntensity: this.viewer.getStudioAOIntensity(),
      };
    } else {
      renderOptions = {
        edgeColor: this.prefs.edgeColor,
        ambientIntensity: this.prefs.ambientIntensity,
        directIntensity: this.prefs.directIntensity,
        metalness: this.prefs.metalness,
        roughness: this.prefs.roughness,
        defaultOpacity: this.prefs.defaultOpacity,
      };

      viewerOptions = {
        control: 'orbit',
        up: 'Z',
        axes: this.prefs.axes,
        axes0: this.prefs.axes0,
        grid: [...this.prefs.grid],
        centerGrid: this.prefs.centerGrid,
        ortho: this.prefs.ortho,
        transparent: this.prefs.transparent,
        blackEdges: this.prefs.blackEdges,
        collapse: 1,
        tab: 'tree',
        position: null,
        quaternion: null,
        target: null,
        zoom: 1,
        clipIntersection: false,
        clipPlaneHelpers: false,
        clipObjectColors: false,
        zebraCount: this.prefs.zebraCount,
        zebraOpacity: this.prefs.zebraOpacity,
        zebraDirection: this.prefs.zebraDirection,
        zebraColorScheme: this.prefs.zebraColorScheme,
        zebraMappingMode: this.prefs.zebraMappingMode,
        studioEnvironment: 'studio',
        studio4kEnvMaps: false,
        studioEnvIntensity: this.prefs.studioEnvIntensity,
        studioBackground: this.prefs.studioBackground,
        studioToneMapping: this.prefs.studioToneMapping,
        studioExposure: this.prefs.studioExposure,
        studioTextureMapping: this.prefs.studioTextureMapping,
        studioEnvRotation: this.prefs.studioEnvRotation,
        studioShadowIntensity: this.prefs.studioShadowIntensity,
        studioShadowSoftness: this.prefs.studioShadowSoftness,
        studioAOIntensity: this.prefs.studioAOIntensity,
      };
    }

    this.suppressPrefsNotify = true;
    try {
      if (this.hasRendered) {
        try {
          this.viewer.clear();
        } catch (err) {
          console.warn('viewer.clear warning:', err);
        }
      }

      this.viewer.render(shapes as Shapes, renderOptions, viewerOptions);
      this.hasRendered = true;
      this.lastModelId = modelId;

      if (savedStates) {
        const currentStates = this.viewer.getStates();
        const hasDiff = Object.entries(savedStates).some(([path, st]) => {
          const cur = currentStates[path];
          return cur && (cur[0] !== st[0] || cur[1] !== st[1]);
        });
        if (hasDiff) {
          this.viewer.setStates(savedStates);
        }
      }
    } finally {
      this.suppressPrefsNotify = false;
    }

    if (this.container.getAttribute('data-theme') !== this.theme) {
      try {
        this.viewer.setTheme(this.theme);
      } catch (err) {
        console.warn('setTheme warning:', err);
      }
    }
    document.body.removeAttribute('data-theme');
    this.syncSize();
  }

  clear(): void {
    if (this.viewer && this.hasRendered) {
      try {
        this.viewer.clear();
      } catch (err) {
        console.warn('viewer.clear warning:', err);
      }
    }
    this.hasRendered = false;
    this.lastModelId = null;
  }

  dispose(): void {
    if (this.prefsSaveTimer) {
      clearTimeout(this.prefsSaveTimer);
      this.prefsSaveTimer = null;
      this.flushSavePrefs();
    }
    if (this.rafId !== null) {
      cancelAnimationFrame(this.rafId);
      this.rafId = null;
    }
    this.resizeObserver?.disconnect();
    this.resizeObserver = null;
    this.detachCanvasListeners();
    try {
      this.viewer?.dispose();
    } catch {
      // ignore viewer cleanup errors
    }
    try {
      this.display?.dispose();
    } catch {
      // ignore display cleanup errors
    }
    this.viewer = null;
    this.display = null;
    this.hasRendered = false;
    this.lastModelId = null;
  }
}
