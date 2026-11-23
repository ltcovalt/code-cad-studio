import * as api from './api';
import type { CadViewportController } from './cadViewer';
import { buildModelTree, collectFolderKeys, type TreeNode } from './modelTree';
import {
  deepClone,
  mergeWithDefaults,
  normalizeParamsWithFields,
  paramsEqual,
  setParamByPath,
} from './params';
import type {
  Config,
  ConfigResponse,
  ExportFormat,
  ModelInfo,
  ParamField,
  ParamsRecord,
  ParamUpdateMode,
  Preset,
  PresetVerification,
  SetupLogEvent,
  SetupProgressEvent,
  StatusState,
  UpdateDefaultsResponse,
  VerificationStatus,
} from './types';

const THEME_STORAGE_KEY = 'code-cad-theme';
const DESCRIPTIONS_STORAGE_KEY = 'code-cad-show-descriptions';
const COLLAPSED_TREE_STORAGE_KEY = 'code-cad-tree-collapsed';
const RENDER_DEBOUNCE_MS = 180;

function loadShowDescriptions(): boolean {
  try {
    return localStorage.getItem(DESCRIPTIONS_STORAGE_KEY) === 'true';
  } catch {
    return false;
  }
}

function loadCollapsedFolders(): Record<string, boolean> {
  try {
    const raw = localStorage.getItem(COLLAPSED_TREE_STORAGE_KEY);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      const out: Record<string, boolean> = {};
      for (const k of parsed) {
        if (typeof k === 'string' && k) out[k] = true;
      }
      return out;
    }
  } catch {
    // ignore storage errors
  }
  return {};
}

class StudioState {
  // Config & environment
  config = $state<Config>({ libraries: [], python_path: '', export_dir: '', theme: 'dark' });
  hasLibraries = $derived((this.config.libraries?.length ?? 0) > 0);
  resolvedPython = $state('');
  resolvedExportDir = $state('');
  hasDesktop = $state(false);
  workerRunning = $state(false);
  workerError = $state('');
  theme = $state('dark');
  showDescriptions = $state(loadShowDescriptions());
  needsOnboarding = $state(false);
  onboardingOpen = $state(false);
  onboardingInitialStep = $state<1 | 2>(1);
  setupProgress = $state<SetupProgressEvent | null>(null);
  setupLogs = $state<string[]>([]);
  settingsOpen = $state(false);
  savePresetModalOpen = $state(false);
  managePresetsModalOpen = $state(false);
  reloadedVisible = $state(false);

  // Models & Explorer tree
  models = $state<ModelInfo[]>([]);
  libraryErrors = $state<Record<string, string>>({});
  modelsLoading = $state(false);
  modelsError = $state('');
  searchQuery = $state('');
  collapsedFolders = $state<Record<string, boolean>>(loadCollapsedFolders());

  modelTree = $derived.by<TreeNode[]>(() =>
    buildModelTree(
      this.config.libraries ?? [],
      this.models,
      this.libraryErrors,
      this.searchQuery
    )
  );

  expandableFolderKeys = $derived.by<string[]>(() => collectFolderKeys(this.modelTree));

  private saveCollapsedFolders(): void {
    try {
      const keys = Object.keys(this.collapsedFolders).filter((k) => this.collapsedFolders[k]);
      localStorage.setItem(COLLAPSED_TREE_STORAGE_KEY, JSON.stringify(keys));
    } catch {
      // ignore storage errors
    }
  }

  isFolderCollapsed(key: string): boolean {
    if (this.searchQuery.trim() !== '') {
      return false;
    }
    return Boolean(this.collapsedFolders[key]);
  }

  toggleFolder(key: string): void {
    const next = { ...this.collapsedFolders };
    if (next[key]) {
      delete next[key];
    } else {
      next[key] = true;
    }
    this.collapsedFolders = next;
    this.saveCollapsedFolders();
  }

  expandAllFolders(): void {
    this.collapsedFolders = {};
    this.saveCollapsedFolders();
  }

  collapseAllFolders(): void {
    const next: Record<string, boolean> = {};
    for (const key of this.expandableFolderKeys) {
      next[key] = true;
    }
    this.collapsedFolders = next;
    this.saveCollapsedFolders();
  }

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
  defaultPresetKey = $state('');
  exportFilename = $state('');

  selectedPreset = $derived(
    this.presets.find((p) => p.key === this.selectedPresetKey) ?? null
  );

  presetDefaults = $derived.by<ParamsRecord>(() => {
    const found = this.selectedPreset;
    const merged = found ? mergeWithDefaults(this.defaults, found.params) : deepClone(this.defaults);
    return normalizeParamsWithFields(merged, this.fields);
  });

  paramsModified = $derived(!paramsEqual(this.params, this.presetDefaults));

  // Render & status
  rendering = $state(false);
  exporting = $state(false);
  hasMesh = $state(false);
  status = $state<StatusState>({ kind: 'idle' });

  private viewport: CadViewportController | null = null;
  private latestRenderSeq = 0;
  private renderAbort: AbortController | null = null;
  private lastRenderedKey = '';
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
      onSetupProgress: (evt: SetupProgressEvent) => {
        this.setupProgress = evt;
      },
      onSetupLog: (evt: SetupLogEvent) => {
        if (!evt.line) return;
        const next = [...this.setupLogs, evt.line];
        this.setupLogs = next.length > 300 ? next.slice(next.length - 300) : next;
      },
    });

    await this.loadConfig();
    if (this.needsOnboarding) {
      this.onboardingOpen = true;
    } else {
      await this.refreshModels();
    }
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
    document.documentElement.setAttribute('data-theme', t);
    document.body.removeAttribute('data-theme');
    this.viewport?.setTheme(t);
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

  setShowDescriptions(show: boolean): void {
    this.showDescriptions = show;
    try {
      localStorage.setItem(DESCRIPTIONS_STORAGE_KEY, String(show));
    } catch {
      // ignore
    }
  }

  toggleShowDescriptions(): void {
    this.setShowDescriptions(!this.showDescriptions);
  }

  async loadConfig(): Promise<void> {
    try {
      const resp = await api.getConfig();
      this.config = {
        ...resp.config,
        libraries: resp.config.libraries ?? [],
      };
      this.resolvedPython = resp.resolved_python;
      this.resolvedExportDir = resp.resolved_export_dir;
      this.needsOnboarding = Boolean(resp.needs_onboarding);
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

  clearSetupStream(): void {
    this.setupProgress = null;
    this.setupLogs = [];
  }

  openOnboarding(initialStep: 1 | 2 = 1): void {
    this.settingsOpen = false;
    this.clearSetupStream();
    this.onboardingInitialStep = initialStep;
    this.onboardingOpen = true;
  }

  async finishOnboarding(resp: ConfigResponse): Promise<void> {
    this.needsOnboarding = Boolean(resp.needs_onboarding);
    this.onboardingOpen = false;
    this.onboardingInitialStep = 1;
    this.clearSetupStream();
    await this.applySavedConfig(resp);
    if (!this.activeModelId && this.models.length > 0) {
      void this.selectModel(this.models[0].id);
    }
  }

  private clearActiveModel(): void {
    this.activeModelId = null;
    this.activeModel = null;
    this.modelDescription = '';
    this.fields = [];
    this.defaults = {};
    this.params = {};
    this.presets = [];
    this.modelLoadError = '';
    this.modelLoadTraceback = '';
    this.lastRenderedKey = '';
    this.status = { kind: 'idle' };
  }

  async applySavedConfig(resp: {
    config: Config;
    resolved_python: string;
    resolved_export_dir: string;
    needs_onboarding?: boolean;
    has_desktop: boolean;
    worker_running: boolean;
    worker_error: string;
  }): Promise<void> {
    const prevLibs = this.config.libraries ?? [];
    const nextLibs = resp.config.libraries ?? [];
    this.config = { ...resp.config, libraries: nextLibs };
    this.resolvedPython = resp.resolved_python;
    this.resolvedExportDir = resp.resolved_export_dir;
    if (typeof resp.needs_onboarding === 'boolean') {
      this.needsOnboarding = resp.needs_onboarding;
    }
    this.hasDesktop = resp.has_desktop;
    this.workerRunning = resp.worker_running;
    this.workerError = resp.worker_error;

    // Clear active model if its owning library was removed or its path changed
    if (this.activeModel) {
      const libId = this.activeModel.library_id;
      const prevLib = prevLibs.find((l) => l.id === libId);
      const nextLib = nextLibs.find((l) => l.id === libId);
      if (!nextLib || (prevLib && prevLib.path !== nextLib.path)) {
        this.clearActiveModel();
      }
    }

    await this.refreshModels();

    if (this.activeModelId && !this.models.some((m) => m.id === this.activeModelId)) {
      this.clearActiveModel();
    }
  }

  async refreshModels(): Promise<void> {
    if (!this.hasLibraries) {
      this.models = [];
      this.libraryErrors = {};
      this.modelsError = '';
      return;
    }
    this.modelsLoading = true;
    this.modelsError = '';
    try {
      const resp = await api.listModels();
      if (resp.libraries) {
        this.config = { ...this.config, libraries: resp.libraries };
      }
      this.libraryErrors = resp.library_errors ?? {};
      if (!resp.ok) {
        this.models = [];
        this.modelsError = resp.error || 'Failed to list models';
      } else {
        this.models = resp.models ?? [];
      }
    } catch (err) {
      this.models = [];
      this.libraryErrors = {};
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
      this.presets = resp.presets ?? [];
      this.defaultPresetKey = resp.default_preset_key ?? '';

      const pinned = this.defaultPresetKey
        ? this.presets.find((p) => p.key === this.defaultPresetKey)
        : undefined;
      if (pinned) {
        this.selectedPresetKey = pinned.key;
        this.params = normalizeParamsWithFields(
          mergeWithDefaults(this.defaults, pinned.params),
          this.fields
        );
      } else {
        this.selectedPresetKey = 'default';
        this.params = normalizeParamsWithFields(deepClone(this.defaults), this.fields);
      }

      const stemFallback = resp.model.local_id?.split('/').pop() || modelId;
      this.exportFilename = stemFallback;
      this.lastRenderedKey = '';
      this.modelLoading = false;

      await this.triggerRender(isDifferentModel, true);
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
    const next = found
      ? mergeWithDefaults(this.defaults, found.params)
      : deepClone(this.defaults);
    this.params = normalizeParamsWithFields(next, this.fields);
    void this.triggerRender(false);
  }

  resetParams(): void {
    this.selectPreset(this.selectedPresetKey);
  }

  private makeRenderKey(modelId: string, params: ParamsRecord): string {
    return `${modelId}:${JSON.stringify(params)}`;
  }

  updateParam(path: string, value: unknown, mode: ParamUpdateMode = 'debounced'): void {
    this.params = setParamByPath(this.params, path, value);
    if (this.debounceTimer) {
      clearTimeout(this.debounceTimer);
      this.debounceTimer = null;
    }
    if (mode === 'preview') {
      return;
    }
    if (mode === 'immediate') {
      void this.triggerRender(false);
      return;
    }
    this.debounceTimer = setTimeout(() => {
      this.debounceTimer = null;
      void this.triggerRender(false);
    }, RENDER_DEBOUNCE_MS);
  }

  commitPreviewIfStale(): void {
    const modelId = this.activeModelId;
    if (!modelId || this.rendering || this.debounceTimer) return;
    const currentKey = this.makeRenderKey(modelId, this.params);
    if (currentKey !== this.lastRenderedKey) {
      void this.triggerRender(false);
    }
  }

  private cancelPendingRender(): void {
    if (this.debounceTimer) {
      clearTimeout(this.debounceTimer);
      this.debounceTimer = null;
    }
    if (this.renderAbort) {
      this.renderAbort.abort();
      this.renderAbort = null;
    }
  }

  async triggerRender(resetCamera: boolean, force = false): Promise<void> {
    const modelId = this.activeModelId;
    if (!modelId) return;

    const paramsSnapshot = deepClone(this.params);
    const renderKey = this.makeRenderKey(modelId, paramsSnapshot);

    if (!force && !resetCamera && renderKey === this.lastRenderedKey) {
      if (this.debounceTimer) {
        clearTimeout(this.debounceTimer);
        this.debounceTimer = null;
      }
      if (this.renderAbort) {
        this.renderAbort.abort();
        this.renderAbort = null;
        this.latestRenderSeq++;
        this.rendering = false;
      }
      return;
    }

    // Always abort any superseded in-flight request and immediately send the latest
    // params so worker.py bumps _render_seq and skips tessellating stale builds.
    this.cancelPendingRender();

    const seq = ++this.latestRenderSeq;
    const abort = new AbortController();
    this.renderAbort = abort;
    this.rendering = true;

    try {
      const resp = await api.renderModel(modelId, paramsSnapshot, abort.signal);
      if (seq !== this.latestRenderSeq || resp.superseded) return;
      if (this.activeModelId !== modelId) return;

      // Discard results if the user has already started editing newer params (e.g., slider drag)
      if (this.makeRenderKey(modelId, this.params) !== renderKey) return;

      if (!resp.ok) {
        this.lastRenderedKey = '';
        this.status = {
          kind: 'error',
          error: resp.error || 'Model build failed',
          traceback: resp.traceback,
        };
        return;
      }

      this.lastRenderedKey = renderKey;
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
      if (this.activeModelId !== modelId) return;
      if (this.makeRenderKey(modelId, this.params) !== renderKey) return;
      this.lastRenderedKey = '';
      this.status = {
        kind: 'error',
        error: err instanceof Error ? err.message : String(err),
      };
    } finally {
      if (seq === this.latestRenderSeq) {
        this.renderAbort = null;
        this.rendering = false;
      }
    }
  }

  async savePreset(
    label: string,
    description: string,
    scope: string,
    fullSnapshot = false,
    verification?: PresetVerification | null
  ): Promise<{ key: string; message: string } | null> {
    const modelId = this.activeModelId;
    if (!modelId) return null;

    const resp = await api.savePreset(modelId, {
      label,
      description,
      scope,
      full_snapshot: fullSnapshot,
      params: this.params,
      ...(verification ? { verification } : {}),
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
    if (resp.preset_save_scope) {
      this.config = { ...this.config, preset_save_scope: resp.preset_save_scope };
    }
    return {
      key: resp.selected_key ?? '',
      message: `Saved preset "${label.trim()}"`,
    };
  }

  async updatePresetVerification(
    key: string,
    scope: string,
    status: VerificationStatus,
    note: string
  ): Promise<void> {
    const modelId = this.activeModelId;
    if (!modelId) return;

    const resp = await api.setPresetVerification(modelId, key, {
      scope,
      status,
      note: note.trim(),
    });
    if (!resp.ok) {
      throw new Error(resp.error || 'Failed to update preset verification');
    }
    if (resp.presets) {
      this.presets = resp.presets;
    }
  }

  async deletePreset(key: string, scope: string): Promise<void> {
    const modelId = this.activeModelId;
    if (!modelId) return;

    const resp = await api.deletePreset(modelId, key, scope);
    if (!resp.ok) {
      throw new Error(resp.error || 'Failed to delete preset');
    }
    if (resp.presets) {
      this.presets = resp.presets;
    }
    this.defaultPresetKey = resp.default_preset_key ?? '';
    if (this.selectedPresetKey === key) {
      const stillPresent = this.presets.find((p) => p.key === key);
      if (stillPresent) {
        this.params = normalizeParamsWithFields(
          mergeWithDefaults(this.defaults, stillPresent.params),
          this.fields
        );
      } else {
        this.selectedPresetKey = 'default';
        this.params = normalizeParamsWithFields(deepClone(this.defaults), this.fields);
      }
      void this.triggerRender(false);
    }
  }

  async renamePreset(
    oldKey: string,
    scope: string,
    label: string,
    description: string
  ): Promise<string> {
    const modelId = this.activeModelId;
    if (!modelId) return oldKey;

    const resp = await api.renamePreset(modelId, oldKey, { scope, label, description });
    if (!resp.ok) {
      throw new Error(resp.error || 'Failed to rename preset');
    }
    if (resp.presets) {
      this.presets = resp.presets;
    }
    const newKey = resp.key || oldKey;
    if (this.selectedPresetKey === oldKey) {
      this.selectedPresetKey = newKey;
    }
    if (resp.default_preset_key !== undefined) {
      this.defaultPresetKey = resp.default_preset_key;
      const nextMap = { ...(this.config.default_presets ?? {}) };
      if (this.defaultPresetKey) {
        nextMap[modelId] = this.defaultPresetKey;
      } else {
        delete nextMap[modelId];
      }
      this.config = { ...this.config, default_presets: nextMap };
    }
    return newKey;
  }

  async setDefaultPreset(key: string): Promise<void> {
    const modelId = this.activeModelId;
    if (!modelId) return;

    const resp = await api.setDefaultPreset(modelId, key);
    if (!resp.ok) {
      throw new Error(resp.error || 'Failed to set default preset');
    }
    this.defaultPresetKey = resp.default_preset_key ?? '';
    const nextMap = { ...(this.config.default_presets ?? {}) };
    if (this.defaultPresetKey) {
      nextMap[modelId] = this.defaultPresetKey;
    } else {
      delete nextMap[modelId];
    }
    this.config = { ...this.config, default_presets: nextMap };
  }

  async previewUpdateDefaults(): Promise<UpdateDefaultsResponse> {
    const modelId = this.activeModelId;
    if (!modelId) {
      throw new Error('No active model');
    }
    return api.updateModelDefaults(modelId, {
      params: this.params,
      dry_run: true,
    });
  }

  async applyUpdateDefaults(expectedSha256: string): Promise<UpdateDefaultsResponse> {
    const modelId = this.activeModelId;
    if (!modelId) {
      throw new Error('No active model');
    }
    const resp = await api.updateModelDefaults(modelId, {
      params: this.params,
      dry_run: false,
      expected_sha256: expectedSha256,
    });
    if (!resp.ok) {
      throw new Error(resp.error || 'Failed to update model defaults');
    }
    // Refresh schema/defaults immediately so UI matches updated Python file
    const schemaResp = await api.getModel(modelId);
    if (this.activeModelId === modelId && schemaResp.ok && schemaResp.model) {
      this.activeModel = schemaResp.model;
      this.modelDescription = schemaResp.description ?? '';
      this.fields = schemaResp.fields ?? [];
      this.defaults = deepClone(schemaResp.defaults ?? {});
      this.params = deepClone(schemaResp.defaults ?? {});
      this.presets = schemaResp.presets ?? [];
      this.selectedPresetKey = 'default';
      this.lastRenderedKey = '';
      void this.triggerRender(false, true);
    }
    return resp;
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
      this.params = normalizeParamsWithFields(
        mergeWithDefaults(this.defaults, previousParams),
        this.fields
      );
      this.presets = resp.presets ?? [];
      this.modelLoadError = '';
      this.modelLoadTraceback = '';
      this.lastRenderedKey = '';

      await this.triggerRender(false, true);
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
