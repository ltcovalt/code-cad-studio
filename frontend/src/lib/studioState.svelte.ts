import * as api from './api';
import type { CadViewportController } from './cadViewer';
import { deepClone, mergeWithDefaults, setParamByPath } from './params';
import type {
  Config,
  ExportFormat,
  ModelInfo,
  ParamField,
  ParamsRecord,
  Preset,
  StatusState,
} from './types';

const THEME_STORAGE_KEY = 'code-cad-theme';
const RENDER_DEBOUNCE_MS = 80;

class StudioState {
  // Config & environment
  config = $state<Config>({ project_root: '', python_path: '', theme: 'dark' });
  resolvedPython = $state('');
  hasDesktop = $state(false);
  workerRunning = $state(false);
  workerError = $state('');
  theme = $state('dark');
  settingsOpen = $state(false);
  reloadedVisible = $state(false);

  // Models list
  models = $state<ModelInfo[]>([]);
  modelsLoading = $state(false);
  modelsError = $state('');
  searchQuery = $state('');

  filteredModels = $derived.by(() => {
    const q = this.searchQuery.trim().toLowerCase();
    if (!q) return this.models;
    return this.models.filter(
      (m) =>
        m.name.toLowerCase().includes(q) ||
        m.id.toLowerCase().includes(q) ||
        m.description.toLowerCase().includes(q)
    );
  });

  // Active model workspace
  activeModelId = $state<string | null>(null);
  activeModel = $state<ModelInfo | null>(null);
  modelDescription = $state('');
  modelLoading = $state(false);
  modelLoadError = $state('');
  modelLoadTraceback = $state('');
  fields = $state<ParamField[]>([]);
  defaults = $state<ParamsRecord>({});
  params = $state<ParamsRecord>({});
  presets = $state<Preset[]>([]);
  selectedPresetKey = $state('default');
  exportFilename = $state('');

  // Render & status
  rendering = $state(false);
  exporting = $state(false);
  hasMesh = $state(false);
  status = $state<StatusState>({ kind: 'idle' });

  private viewport: CadViewportController | null = null;
  private latestRenderSeq = 0;
  private renderAbort: AbortController | null = null;
  private renderInFlight = false;
  private pendingCoalescedRender = false;
  private debounceTimer: ReturnType<typeof setTimeout> | null = null;
  private reloadBadgeTimer: ReturnType<typeof setTimeout> | null = null;
  private unsubscribeSSE: (() => void) | null = null;
  private initialized = false;

  attachViewport(controller: CadViewportController | null): void {
    this.viewport = controller;
    if (controller) {
      controller.setTheme(this.theme);
    }
  }

  async init(): Promise<void> {
    if (this.initialized) return;
    this.initialized = true;

    try {
      const savedTheme = localStorage.getItem(THEME_STORAGE_KEY);
      if (savedTheme) {
        this.applyThemeDom(savedTheme);
      }
    } catch {
      // ignore storage errors
    }

    this.unsubscribeSSE = api.subscribeEvents({
      onFileChanged: () => {
        void this.handleFileChanged();
      },
      onPresetsChanged: () => {
        void this.handlePresetsChanged();
      },
      onWorkerError: (err) => {
        this.workerError = err;
        if (this.activeModelId) {
          this.status = { kind: 'error', error: err };
        }
      },
    });

    await this.loadConfig();
    await this.refreshModels();
  }

  destroy(): void {
    if (this.debounceTimer) clearTimeout(this.debounceTimer);
    if (this.reloadBadgeTimer) clearTimeout(this.reloadBadgeTimer);
    this.renderAbort?.abort();
    this.unsubscribeSSE?.();
    this.unsubscribeSSE = null;
    this.initialized = false;
  }

  private applyThemeDom(themeName: string): void {
    const t = themeName || 'dark';
    this.theme = t;
    this.viewport?.setTheme(t);
    document.documentElement.setAttribute('data-theme', t);
    document.body.setAttribute('data-theme', t);
    try {
      localStorage.setItem(THEME_STORAGE_KEY, t);
    } catch {
      // ignore
    }
  }

  setTheme(themeName: string): void {
    this.applyThemeDom(themeName);
    this.config = { ...this.config, theme: themeName };
    api.saveTheme(themeName).catch(() => {});
  }

  async loadConfig(): Promise<void> {
    try {
      const resp = await api.getConfig();
      this.config = resp.config;
      this.resolvedPython = resp.resolved_python;
      this.hasDesktop = resp.has_desktop;
      this.workerRunning = resp.worker_running;
      this.workerError = resp.worker_error;
      if (resp.config.theme) {
        this.applyThemeDom(resp.config.theme);
      }
    } catch (err) {
      this.workerError = err instanceof Error ? err.message : String(err);
    }
  }

  async applySavedConfig(resp: {
    config: Config;
    resolved_python: string;
    has_desktop: boolean;
    worker_running: boolean;
    worker_error: string;
  }): Promise<void> {
    const projectChanged = resp.config.project_root !== this.config.project_root;
    this.config = resp.config;
    this.resolvedPython = resp.resolved_python;
    this.hasDesktop = resp.has_desktop;
    this.workerRunning = resp.worker_running;
    this.workerError = resp.worker_error;

    if (projectChanged) {
      this.activeModelId = null;
      this.activeModel = null;
      this.fields = [];
      this.defaults = {};
      this.params = {};
      this.presets = [];
      this.modelLoadError = '';
      this.modelLoadTraceback = '';
      this.status = { kind: 'idle' };
    }
    await this.refreshModels();
  }

  async refreshModels(): Promise<void> {
    if (!this.config.project_root) {
      this.models = [];
      this.modelsError = '';
      return;
    }
    this.modelsLoading = true;
    this.modelsError = '';
    try {
      const resp = await api.listModels();
      this.config = { ...this.config, project_root: resp.project_root };
      if (!resp.ok) {
        this.models = [];
        this.modelsError = resp.error || 'Failed to list models';
      } else {
        this.models = resp.models ?? [];
      }
    } catch (err) {
      this.models = [];
      this.modelsError = err instanceof Error ? err.message : String(err);
    } finally {
      this.modelsLoading = false;
    }
  }

  async selectModel(modelId: string): Promise<void> {
    const isDifferentModel = this.activeModelId !== modelId;
    this.activeModelId = modelId;
    this.modelLoading = true;
    this.modelLoadError = '';
    this.modelLoadTraceback = '';
    this.cancelPendingRender();

    try {
      const resp = await api.getModel(modelId);
      if (this.activeModelId !== modelId) return;
      if (!resp.ok || !resp.model) {
        this.activeModel = null;
        this.fields = [];
        this.defaults = {};
        this.params = {};
        this.presets = [];
        this.modelLoadError = resp.error || 'Failed to load model schema';
        this.modelLoadTraceback = resp.traceback || '';
        return;
      }

      this.activeModel = resp.model;
      this.modelDescription = resp.description ?? '';
      this.fields = resp.fields ?? [];
      this.defaults = deepClone(resp.defaults ?? {});
      this.params = deepClone(resp.defaults ?? {});
      this.presets = resp.presets ?? [];
      this.selectedPresetKey = 'default';
      this.exportFilename = modelId;
      this.modelLoading = false;

      await this.triggerRender(isDifferentModel);
    } catch (err) {
      if (this.activeModelId !== modelId) return;
      this.modelLoadError = err instanceof Error ? err.message : String(err);
    } finally {
      if (this.activeModelId === modelId) {
        this.modelLoading = false;
      }
    }
  }

  selectPreset(presetKey: string): void {
    const found = this.presets.find((p) => p.key === presetKey);
    this.selectedPresetKey = found ? found.key : 'default';
    this.params = found
      ? mergeWithDefaults(this.defaults, found.params)
      : deepClone(this.defaults);
    void this.triggerRender(false);
  }

  resetParams(): void {
    this.selectedPresetKey = 'default';
    this.params = deepClone(this.defaults);
    void this.triggerRender(false);
  }

  updateParam(path: string, value: unknown, immediate = false): void {
    this.params = setParamByPath(this.params, path, value);
    if (immediate) {
      if (this.debounceTimer) {
        clearTimeout(this.debounceTimer);
        this.debounceTimer = null;
      }
      if (this.renderInFlight) {
        this.pendingCoalescedRender = true;
        return;
      }
      void this.triggerRender(false, false);
    } else {
      if (this.debounceTimer) clearTimeout(this.debounceTimer);
      this.debounceTimer = setTimeout(() => {
        this.debounceTimer = null;
        if (this.renderInFlight) {
          this.pendingCoalescedRender = true;
          return;
        }
        void this.triggerRender(false, false);
      }, RENDER_DEBOUNCE_MS);
    }
  }

  private cancelPendingRender(): void {
    if (this.debounceTimer) {
      clearTimeout(this.debounceTimer);
      this.debounceTimer = null;
    }
    this.pendingCoalescedRender = false;
    if (this.renderAbort) {
      this.renderAbort.abort();
      this.renderAbort = null;
    }
    this.renderInFlight = false;
  }

  async triggerRender(resetCamera: boolean, forceAbort = true): Promise<void> {
    const modelId = this.activeModelId;
    if (!modelId) return;

    if (forceAbort) {
      this.cancelPendingRender();
    } else if (this.debounceTimer) {
      clearTimeout(this.debounceTimer);
      this.debounceTimer = null;
    }

    const seq = ++this.latestRenderSeq;
    const abort = new AbortController();
    this.renderAbort = abort;
    this.renderInFlight = true;
    this.rendering = true;

    try {
      const resp = await api.renderModel(modelId, this.params, abort.signal);
      if (seq !== this.latestRenderSeq) return;
      if (resp.superseded) return;

      if (!resp.ok) {
        this.status = {
          kind: 'error',
          error: resp.error || 'Model build failed',
          traceback: resp.traceback,
        };
        return;
      }

      if (resp.export_filename) {
        this.exportFilename = resp.export_filename;
      }

      if (resp.shapes && this.viewport) {
        this.hasMesh = true;
        this.viewport.renderShapes(modelId, resp.shapes, resetCamera);
      }

      this.status = {
        kind: 'ok',
        bbox: resp.bbox ?? null,
        buildTimeMs: resp.build_time_ms ?? 0,
        tessellateTimeMs: resp.tessellate_time_ms ?? 0,
        cached: resp.cached,
      };
    } catch (err) {
      if (abort.signal.aborted || seq !== this.latestRenderSeq) return;
      this.status = {
        kind: 'error',
        error: err instanceof Error ? err.message : String(err),
      };
    } finally {
      if (seq === this.latestRenderSeq) {
        this.renderInFlight = false;
        this.rendering = false;
        if (this.pendingCoalescedRender && this.activeModelId === modelId) {
          this.pendingCoalescedRender = false;
          void this.triggerRender(false, false);
        }
      }
    }
  }

  async savePreset(label: string, description: string, scope: string): Promise<string | null> {
    const modelId = this.activeModelId;
    if (!modelId) return null;

    const resp = await api.savePreset(modelId, {
      label,
      description,
      scope,
      params: this.params,
    });
    if (!resp.ok) {
      throw new Error(resp.error || 'Failed to save preset');
    }
    if (resp.presets) {
      this.presets = resp.presets;
    }
    if (resp.selected_key) {
      this.selectedPresetKey = resp.selected_key;
    }
    return `Saved preset "${label.trim()}"`;
  }

  async exportActiveModel(format: ExportFormat): Promise<void> {
    const modelId = this.activeModelId;
    if (!modelId) return;
    this.exporting = true;
    try {
      const resp = await api.exportModel(modelId, this.params, format, this.hasDesktop);
      if (!resp.ok) {
        this.status = {
          kind: 'error',
          error: resp.error || 'Export failed',
          traceback: resp.traceback,
        };
      } else if (resp.cancelled) {
        this.status = { kind: 'message', message: 'Export cancelled' };
      } else {
        const name = resp.path ? resp.path.split(/[\\/]/).pop() || resp.path : `${modelId}.${format}`;
        const kb = Math.ceil((resp.size ?? 0) / 1024);
        this.status = { kind: 'message', message: `Exported ${name} (${kb} KB)` };
      }
    } catch (err) {
      this.status = {
        kind: 'error',
        error: err instanceof Error ? err.message : String(err),
      };
    } finally {
      this.exporting = false;
    }
  }

  private flashReloadBadge(): void {
    this.reloadedVisible = true;
    if (this.reloadBadgeTimer) clearTimeout(this.reloadBadgeTimer);
    this.reloadBadgeTimer = setTimeout(() => {
      this.reloadedVisible = false;
      this.reloadBadgeTimer = null;
    }, 2000);
  }

  private async handleFileChanged(): Promise<void> {
    this.flashReloadBadge();
    await this.refreshModels();

    const modelId = this.activeModelId;
    if (!modelId) return;

    const previousParams = deepClone(this.params);
    try {
      const resp = await api.getModel(modelId);
      if (this.activeModelId !== modelId) return;
      if (!resp.ok || !resp.model) {
        this.status = {
          kind: 'error',
          error: resp.error || 'Failed to reload model schema',
          traceback: resp.traceback,
        };
        return;
      }

      this.activeModel = resp.model;
      this.modelDescription = resp.description ?? '';
      this.fields = resp.fields ?? [];
      this.defaults = deepClone(resp.defaults ?? {});
      this.params = mergeWithDefaults(this.defaults, previousParams);
      this.presets = resp.presets ?? [];
      this.modelLoadError = '';
      this.modelLoadTraceback = '';

      await this.triggerRender(false);
    } catch (err) {
      if (this.activeModelId !== modelId) return;
      this.status = {
        kind: 'error',
        error: err instanceof Error ? err.message : String(err),
      };
    }
  }

  private async handlePresetsChanged(): Promise<void> {
    const modelId = this.activeModelId;
    if (!modelId) return;
    try {
      const resp = await api.getPresets(modelId);
      if (this.activeModelId === modelId && resp.ok) {
        this.presets = resp.presets;
      }
    } catch {
      // ignore transient errors
    }
  }
}

export const studio = new StudioState();
