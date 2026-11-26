import * as api from './api';
import { ApiRequestError } from './api';
import type { CadViewportController } from './cadViewer';
import { buildModelTree, collectFolderKeys, type TreeNode } from './modelTree';
import {
  deepClone,
  extractComponentPresetSelections,
  getGroupDefaultsForField,
  getParamByPath,
  isPlainObject,
  mergeWithDefaults,
  normalizeComponentPresetsForSave,
  normalizeParamsWithFields,
  paramsEqual,
  resolveComponentPresetParams,
  resolvePresetParams,
  setParamByPath,
} from './params';
import type {
  ComponentPresetBundle,
  Config,
  ConfigResponse,
  ExportFormat,
  ExportQuality,
  ModelInfo,
  ParamField,
  ParamsRecord,
  ParamUpdateMode,
  Preset,
  PresetVerification,
  ScaffoldRequest,
  ScaffoldResponse,
  SetupLogEvent,
  SetupProgressEvent,
  StatusState,
  UpdateDefaultsResponse,
  VerificationStatus,
} from './types';

const THEME_STORAGE_KEY = 'code-cad-theme';
const COLLAPSED_TREE_STORAGE_KEY = 'code-cad-tree-collapsed';
const RENDER_DEBOUNCE_MS = 180;

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

function normalizeSlash(p: string): string {
  return p.replace(/\\/g, '/');
}

function shouldReloadActiveModel(
  changedPath: string,
  activeModel: ModelInfo | null,
  allModels: ModelInfo[]
): boolean {
  if (!changedPath) return true;
  const target = normalizeSlash(changedPath);
  if (!activeModel) return false;

  if (activeModel.py_path && normalizeSlash(activeModel.py_path) === target) {
    return true;
  }
  if (!activeModel.py_path && activeModel.rel_path && target.endsWith('/' + normalizeSlash(activeModel.rel_path))) {
    return true;
  }
  if (target.includes('/utils/') || target.includes('/components/')) {
    return true;
  }

  const matchesDifferentModel = allModels.some((m) => {
    if (m.id === activeModel.id) return false;
    if (m.py_path && normalizeSlash(m.py_path) === target) return true;
    if (!m.py_path && m.rel_path && target.endsWith('/' + normalizeSlash(m.rel_path))) return true;
    return false;
  });
  return !matchesDifferentModel;
}

export class StudioState {
  // Config & environment
  config = $state<Config>({ libraries: [], python_path: '', export_dir: '', theme: 'dark' });
  hasLibraries = $derived((this.config.libraries?.length ?? 0) > 0);
  resolvedPython = $state('');
  resolvedExportDir = $state('');
  hasDesktop = $state(false);
  os = $state('');
  windowMaximized = $state(false);
  workerRunning = $state(false);
  workerError = $state('');
  theme = $state('dark');
  needsOnboarding = $state(false);
  onboardingOpen = $state(false);
  onboardingInitialStep = $state<1 | 2>(1);
  setupProgress = $state<SetupProgressEvent | null>(null);
  setupLogs = $state<string[]>([]);
  settingsOpen = $state(false);
  presetDetailsModalOpen = $state(false);
  savePresetModalOpen = $state(false);
  managePresetsModalOpen = $state(false);
  exportModalOpen = $state(false);
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
  components = $state<Record<string, ComponentPresetBundle>>({});
  selectedComponentPresets = $state<Record<string, string>>({});
  selectedPresetKey = $state('default');
  defaultPresetKey = $state('');
  defaultExportFilename = $state('');
  exportFilename = $state('');
  exportFilenameEdited = $state(false);
  exportQuality = $state<ExportQuality>('standard');
  lastExportPath = $state('');

  selectedPreset = $derived(
    this.presets.find((p) => p.key === this.selectedPresetKey) ?? null
  );

  presetDefaults = $derived.by<ParamsRecord>(() =>
    resolvePresetParams(this.defaults, this.fields, this.selectedPreset, this.components)
  );

  presetComponentPresets = $derived.by<Record<string, string>>(() =>
    extractComponentPresetSelections(this.selectedPreset, this.fields)
  );

  effectiveComponentPresets = $derived.by<Record<string, string>>(() =>
    normalizeComponentPresetsForSave(
      this.selectedComponentPresets,
      this.params,
      this.defaults,
      this.fields
    )
  );

  effectivePresetComponentPresets = $derived.by<Record<string, string>>(() =>
    normalizeComponentPresetsForSave(
      this.presetComponentPresets,
      this.presetDefaults,
      this.defaults,
      this.fields
    )
  );

  paramsModified = $derived(
    !paramsEqual(this.params, this.presetDefaults) ||
      !paramsEqual(this.effectiveComponentPresets, this.effectivePresetComponentPresets)
  );

  // Render & status
  rendering = $state(false);
  buildStartedAt = $state<number | null>(null);
  exporting = $state(false);
  hasMesh = $state(false);
  status = $state<StatusState>({ kind: 'idle' });

  private viewport: CadViewportController | null = null;
  private pendingRender: { modelId: string; shapes: unknown; resetCamera: boolean } | null = null;
  private paramMemory = new Map<
    string,
    {
      params: ParamsRecord;
      selectedPresetKey: string;
      selectedComponentPresets: Record<string, string>;
    }
  >();
  private nullableGroupMemory = new Map<string, ParamsRecord>();
  private latestRenderSeq = 0;
  private renderAbort: AbortController | null = null;
  private lastRenderedKey = '';
  private debounceTimer: ReturnType<typeof setTimeout> | null = null;
  private reloadBadgeTimer: ReturnType<typeof setTimeout> | null = null;
  private unsubscribeSSE: (() => void) | null = null;
  private initialized = false;
  private fileReloadSeq = 0;
  private fileReloadInFlight: Promise<void> | null = null;
  private queuedFileChangedPath: string | null = null;

  attachViewport(controller: CadViewportController | null): void {
    this.viewport = controller;
    if (controller) {
      controller.setTheme(this.theme);
      if (this.pendingRender && this.activeModelId === this.pendingRender.modelId) {
        const { modelId, shapes, resetCamera } = this.pendingRender;
        this.pendingRender = null;
        this.hasMesh = true;
        controller.renderShapes(modelId, shapes, resetCamera);
      }
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
      onFileChanged: (path) => {
        void this.handleFileChanged(path);
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
      onReconnect: () => {
        void this.handleSSEReconnect();
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
    if (this.debounceTimer) {
      clearTimeout(this.debounceTimer);
      this.debounceTimer = null;
    }
    if (this.reloadBadgeTimer) {
      clearTimeout(this.reloadBadgeTimer);
      this.reloadBadgeTimer = null;
    }
    this.renderAbort?.abort();
    this.renderAbort = null;
    this.unsubscribeSSE?.();
    this.unsubscribeSSE = null;
    this.initialized = false;
  }

  private async handleSSEReconnect(): Promise<void> {
    await this.loadConfig();
    if (!this.needsOnboarding) {
      await this.refreshModels();
    }
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

  setExportFilename(name: string): void {
    this.exportFilename = name;
    this.exportFilenameEdited = true;
  }

  resetExportFilename(): void {
    this.exportFilename = this.defaultExportFilename;
    this.exportFilenameEdited = false;
  }

  setExportQuality(quality: ExportQuality): void {
    this.exportQuality = quality;
    this.config = { ...this.config, export_quality: quality };
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
      this.os = resp.os ?? '';
      this.workerRunning = resp.worker_running;
      this.workerError = resp.worker_error;
      this.exportQuality = resp.config.export_quality ?? 'standard';
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

  async skipOnboarding(): Promise<void> {
    this.onboardingOpen = false;
    this.needsOnboarding = false;
    this.clearSetupStream();
    try {
      const resp = await api.saveConfig(
        this.config.libraries ?? [],
        this.config.python_path ?? '',
        this.config.export_dir ?? '',
        { onboarding_skipped: true }
      );
      await this.applySavedConfig(resp);
    } catch (err) {
      this.workerError = err instanceof Error ? err.message : String(err);
    }
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

  clearActiveModel(): void {
    this.cancelPendingRender();
    this.latestRenderSeq++;
    this.rendering = false;
    this.buildStartedAt = null;
    this.activeModelId = null;
    this.activeModel = null;
    this.modelDescription = '';
    this.fields = [];
    this.defaults = {};
    this.params = {};
    this.presets = [];
    this.components = {};
    this.selectedComponentPresets = {};
    this.nullableGroupMemory.clear();
    this.defaultPresetKey = '';
    this.selectedPresetKey = 'default';
    this.defaultExportFilename = '';
    this.exportFilename = '';
    this.exportFilenameEdited = false;
    this.presetDetailsModalOpen = false;
    this.exportModalOpen = false;
    this.lastExportPath = '';
    this.modelLoadError = '';
    this.modelLoadTraceback = '';
    this.lastRenderedKey = '';
    this.hasMesh = false;
    this.pendingRender = null;
    this.status = { kind: 'idle' };
    this.viewport?.clear();
  }

  async applySavedConfig(resp: ConfigResponse): Promise<void> {
    const prevLibs = this.config.libraries ?? [];
    const nextLibs = resp.config.libraries ?? [];
    this.config = { ...resp.config, libraries: nextLibs };
    this.resolvedPython = resp.resolved_python;
    this.resolvedExportDir = resp.resolved_export_dir;
    if (typeof resp.needs_onboarding === 'boolean') {
      this.needsOnboarding = resp.needs_onboarding;
    }
    this.hasDesktop = resp.has_desktop;
    if (resp.os !== undefined) {
      this.os = resp.os;
    }
    if (resp.config.export_quality) {
      this.exportQuality = resp.config.export_quality;
    }
    this.workerRunning = resp.worker_running;
    this.workerError = resp.worker_error;

    // Clear param memory for removed or relocated libraries
    for (const prevLib of prevLibs) {
      const nextLib = nextLibs.find((l) => l.id === prevLib.id);
      if (!nextLib || nextLib.path !== prevLib.path) {
        const prefix = `${prevLib.id}/`;
        for (const key of this.paramMemory.keys()) {
          if (key.startsWith(prefix)) {
            this.paramMemory.delete(key);
          }
        }
      }
    }

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
    // Save unsaved edits for the outgoing model before switching
    if (
      this.activeModelId &&
      this.activeModelId !== modelId &&
      this.activeModel &&
      !this.modelLoadError
    ) {
      if (this.paramsModified) {
        this.paramMemory.set(this.activeModelId, {
          params: deepClone(this.params),
          selectedPresetKey: this.selectedPresetKey,
          selectedComponentPresets: { ...this.selectedComponentPresets },
        });
      } else {
        this.paramMemory.delete(this.activeModelId);
      }
    }

    const isDifferentModel = this.activeModelId !== modelId;
    if (isDifferentModel) {
      this.nullableGroupMemory.clear();
    }
    this.activeModelId = modelId;
    this.modelLoading = true;
    this.modelLoadError = '';
    this.modelLoadTraceback = '';
    this.lastExportPath = '';
    this.defaultExportFilename = '';
    this.exportFilename = '';
    this.exportFilenameEdited = false;
    this.pendingRender = null;
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
        this.defaultPresetKey = '';
        this.hasMesh = false;
        this.pendingRender = null;
        this.viewport?.clear();
        this.modelLoadError = resp.error || 'Failed to load model schema';
        this.modelLoadTraceback = resp.traceback || '';
        return;
      }

      if (resp.model.kind === 'error') {
        this.activeModel = resp.model;
        this.fields = [];
        this.defaults = {};
        this.params = {};
        this.presets = [];
        this.defaultPresetKey = '';
        this.hasMesh = false;
        this.pendingRender = null;
        this.viewport?.clear();
        this.modelLoadError =
          resp.error || resp.model.parse_error || 'Failed to parse model script';
        this.modelLoadTraceback = resp.traceback || '';
        return;
      }

      this.activeModel = resp.model;
      this.modelDescription = resp.description ?? '';
      this.fields = resp.fields ?? [];
      this.defaults = deepClone(resp.defaults ?? {});
      this.presets = resp.presets ?? [];
      this.components = resp.components ?? {};
      this.defaultPresetKey = resp.default_preset_key ?? '';

      const mem = this.paramMemory.get(modelId);
      if (mem) {
        const memPreset =
          mem.selectedPresetKey === 'default' ||
          this.presets.some((p) => p.key === mem.selectedPresetKey)
            ? mem.selectedPresetKey
            : this.defaultPresetKey && this.presets.some((p) => p.key === this.defaultPresetKey)
              ? this.defaultPresetKey
              : 'default';
        this.selectedPresetKey = memPreset;
        const foundMemPreset = this.presets.find((p) => p.key === memPreset) ?? null;
        this.selectedComponentPresets = {
          ...extractComponentPresetSelections(foundMemPreset, this.fields),
          ...(mem.selectedComponentPresets ?? {}),
        };
        this.params = normalizeParamsWithFields(
          mergeWithDefaults(this.defaults, mem.params, this.fields),
          this.fields
        );
      } else {
        const pinned = this.defaultPresetKey
          ? this.presets.find((p) => p.key === this.defaultPresetKey)
          : undefined;
        if (pinned) {
          this.selectedPresetKey = pinned.key;
          this.selectedComponentPresets = extractComponentPresetSelections(pinned, this.fields);
          this.params = resolvePresetParams(this.defaults, this.fields, pinned, this.components);
        } else {
          this.selectedPresetKey = 'default';
          this.selectedComponentPresets = extractComponentPresetSelections(null, this.fields);
          this.params = resolvePresetParams(this.defaults, this.fields, null, this.components);
        }
      }

      const stemFallback = resp.model.local_id?.split('/').pop() || modelId;
      this.defaultExportFilename = stemFallback;
      this.exportFilename = stemFallback;
      this.exportFilenameEdited = false;
      this.lastRenderedKey = '';
      this.modelLoading = false;

      await this.triggerRender(isDifferentModel, true);
    } catch (err) {
      if (this.activeModelId !== modelId) return;
      this.activeModel = null;
      this.fields = [];
      this.defaults = {};
      this.params = {};
      this.presets = [];
      this.defaultPresetKey = '';
      this.hasMesh = false;
      this.pendingRender = null;
      this.viewport?.clear();
      this.modelLoadError = err instanceof Error ? err.message : String(err);
      this.modelLoadTraceback = err instanceof ApiRequestError ? (err.traceback ?? '') : '';
    } finally {
      if (this.activeModelId === modelId) {
        this.modelLoading = false;
      }
    }
  }

  selectPreset(presetKey: string): void {
    const found = this.presets.find((p) => p.key === presetKey);
    this.selectedPresetKey = found ? found.key : 'default';
    this.exportFilenameEdited = false;
    this.nullableGroupMemory.clear();
    this.selectedComponentPresets = extractComponentPresetSelections(found ?? null, this.fields);
    this.params = resolvePresetParams(this.defaults, this.fields, found ?? null, this.components);
    if (this.activeModelId) {
      this.paramMemory.delete(this.activeModelId);
    }
    void this.triggerRender(false);
  }

  selectComponentPreset(groupPath: string, componentId: string, presetKey: string): void {
    const normalizedKey = presetKey.trim() || 'default';
    const currentGroup = getParamByPath(this.params, groupPath);
    const fallback = isPlainObject(currentGroup) ? currentGroup : {};
    const resolved = resolveComponentPresetParams(
      componentId,
      normalizedKey,
      fallback,
      this.components
    );
    this.selectedComponentPresets = {
      ...this.selectedComponentPresets,
      [groupPath]: normalizedKey,
    };
    this.params = setParamByPath(this.params, groupPath, resolved);
    if (this.debounceTimer) {
      clearTimeout(this.debounceTimer);
      this.debounceTimer = null;
    }
    void this.triggerRender(false);
  }

  resetComponentGroupToPreset(groupPath: string, componentId: string): void {
    const activeKey = this.selectedComponentPresets[groupPath] || 'default';
    this.selectComponentPreset(groupPath, componentId, activeKey);
  }

  toggleNullableGroup(groupPath: string, field: ParamField, enabled: boolean): void {
    const current = getParamByPath(this.params, groupPath);
    if (!enabled) {
      if (isPlainObject(current)) {
        this.nullableGroupMemory.set(groupPath, deepClone(current));
      }
      this.params = setParamByPath(this.params, groupPath, null);
    } else {
      const remembered = this.nullableGroupMemory.get(groupPath);
      let nextVal: ParamsRecord;
      if (remembered) {
        nextVal = deepClone(remembered);
      } else if (field.component_id) {
        const compKey = this.selectedComponentPresets[groupPath] || 'default';
        nextVal = resolveComponentPresetParams(
          field.component_id,
          compKey,
          getGroupDefaultsForField(field),
          this.components
        );
      } else {
        nextVal = getGroupDefaultsForField(field);
      }
      this.params = setParamByPath(this.params, groupPath, nextVal);
    }
    if (this.debounceTimer) {
      clearTimeout(this.debounceTimer);
      this.debounceTimer = null;
    }
    void this.triggerRender(false);
  }

  resetParams(): void {
    if (this.activeModelId) {
      this.paramMemory.delete(this.activeModelId);
    }
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

  async cancelActiveBuild(): Promise<void> {
    this.latestRenderSeq++;
    this.cancelPendingRender();
    this.rendering = false;
    this.buildStartedAt = null;
    try {
      await api.cancelRender();
    } catch {
      // ignore cancel errors if worker already stopped
    }
    this.status = { kind: 'message', message: 'Build cancelled' };
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
        this.buildStartedAt = null;
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
    this.buildStartedAt = Date.now();

    try {
      const resp = await api.renderModel(modelId, paramsSnapshot, abort.signal);
      if (seq !== this.latestRenderSeq || resp.superseded) return;
      if (this.activeModelId !== modelId) return;

      // Discard results if the user has already started editing newer params (e.g., slider drag)
      if (this.makeRenderKey(modelId, this.params) !== renderKey) return;

      if (!resp.ok) {
        this.lastRenderedKey = '';
        this.pendingRender = null;
        this.hasMesh = false;
        this.viewport?.clear();
        this.status = {
          kind: 'error',
          error: resp.error || 'Model build failed',
          traceback: resp.traceback,
        };
        return;
      }

      this.lastRenderedKey = renderKey;
      if (resp.export_filename) {
        this.defaultExportFilename = resp.export_filename;
        if (!this.exportFilenameEdited) {
          this.exportFilename = resp.export_filename;
        }
      }

      if (resp.shapes) {
        if (this.viewport) {
          this.pendingRender = null;
          this.hasMesh = true;
          this.viewport.renderShapes(modelId, resp.shapes, resetCamera);
        } else {
          this.pendingRender = { modelId, shapes: resp.shapes, resetCamera };
        }
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
      this.pendingRender = null;
      this.hasMesh = false;
      this.viewport?.clear();
      this.status = {
        kind: 'error',
        error: err instanceof Error ? err.message : String(err),
        traceback: err instanceof ApiRequestError ? err.traceback : undefined,
      };
    } finally {
      if (seq === this.latestRenderSeq) {
        this.renderAbort = null;
        this.rendering = false;
        this.buildStartedAt = null;
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

    const componentPresets = this.effectiveComponentPresets;
    const resp = await api.savePreset(modelId, {
      label,
      description,
      scope,
      full_snapshot: fullSnapshot,
      params: this.params,
      ...(Object.keys(componentPresets).length > 0
        ? { component_presets: componentPresets }
        : {}),
      ...(verification ? { verification } : {}),
    });
    if (!resp.ok) {
      throw new Error(resp.error || 'Failed to save preset');
    }
    if (resp.components) {
      this.components = resp.components;
    }
    if (resp.presets) {
      this.presets = resp.presets;
    }
    if (resp.selected_key) {
      this.selectedPresetKey = resp.selected_key;
      const savedPreset = this.presets.find((p) => p.key === resp.selected_key) ?? null;
      this.selectedComponentPresets = extractComponentPresetSelections(savedPreset, this.fields);
    }
    if (resp.preset_save_scope) {
      this.config = { ...this.config, preset_save_scope: resp.preset_save_scope };
    }
    this.paramMemory.delete(modelId);
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
        this.selectedComponentPresets = extractComponentPresetSelections(stillPresent, this.fields);
        this.params = resolvePresetParams(
          this.defaults,
          this.fields,
          stillPresent,
          this.components
        );
      } else {
        this.selectedPresetKey = 'default';
        this.selectedComponentPresets = extractComponentPresetSelections(null, this.fields);
        this.params = resolvePresetParams(this.defaults, this.fields, null, this.components);
      }
      this.paramMemory.delete(modelId);
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
      this.components = schemaResp.components ?? {};
      this.presets = schemaResp.presets ?? [];
      this.defaultPresetKey = schemaResp.default_preset_key ?? '';
      this.selectedPresetKey = 'default';
      this.selectedComponentPresets = extractComponentPresetSelections(null, this.fields);
      this.params = resolvePresetParams(this.defaults, this.fields, null, this.components);
      this.lastRenderedKey = '';
      this.paramMemory.delete(modelId);
      void this.triggerRender(false, true);
    }
    return resp;
  }

  async exportActiveModel(
    format: ExportFormat,
    options?: {
      quality?: ExportQuality;
      linear_deflection?: number;
      angular_deflection?: number;
      filename?: string;
    }
  ): Promise<{
    ok: boolean;
    cancelled?: boolean;
    path?: string;
    size?: number;
    error?: string;
    traceback?: string;
  }> {
    const modelId = this.activeModelId;
    if (!modelId) {
      return { ok: false, error: 'No active model selected' };
    }
    this.exporting = true;
    try {
      const quality = options?.quality ?? this.exportQuality ?? this.config.export_quality ?? 'standard';
      const filename = options?.filename ?? (this.exportFilename.trim() || undefined);
      const resp = await api.exportModel(modelId, this.params, format, this.hasDesktop, {
        ...options,
        quality,
        ...(filename ? { filename } : {}),
      });
      if (!resp.ok) {
        this.lastExportPath = '';
        const error = resp.error || 'Export failed';
        this.status = {
          kind: 'error',
          error,
          traceback: resp.traceback,
        };
        return { ok: false, error, traceback: resp.traceback };
      }
      if (resp.cancelled) {
        return { ok: true, cancelled: true };
      }
      if (this.hasDesktop && resp.path) {
        this.lastExportPath = resp.path;
      } else {
        this.lastExportPath = '';
      }
      const name = resp.path ? resp.path.split(/[\\/]/).pop() || resp.path : `${modelId}.${format}`;
      const kb = Math.ceil((resp.size ?? 0) / 1024);
      this.status = { kind: 'message', message: `Exported ${name} (${kb} KB)` };
      return { ok: true, cancelled: false, path: resp.path, size: resp.size };
    } catch (err) {
      this.lastExportPath = '';
      const error = err instanceof Error ? err.message : String(err);
      const traceback = err instanceof ApiRequestError ? err.traceback : undefined;
      this.status = {
        kind: 'error',
        error,
        traceback,
      };
      return { ok: false, error, traceback };
    } finally {
      this.exporting = false;
    }
  }

  async scaffoldDemo(
    targetDir?: string,
    options?: { selectModel?: boolean }
  ): Promise<ScaffoldResponse> {
    const resp = await api.scaffoldModel({
      mode: 'demo',
      ...(targetDir ? { target_dir: targetDir } : {}),
    });
    if (!resp.ok) {
      throw new Error(resp.error || 'Failed to scaffold demo project');
    }
    if (resp.config) {
      await this.applySavedConfig(resp.config);
    } else {
      await this.refreshModels();
    }
    const shouldSelect = options?.selectModel ?? true;
    if (shouldSelect) {
      if (resp.model_id) {
        await this.selectModel(resp.model_id);
      } else if (!this.activeModelId && this.models.length > 0) {
        await this.selectModel(this.models[0].id);
      }
    }
    return resp;
  }

  async scaffoldNewModel(payload: Omit<ScaffoldRequest, 'mode'>): Promise<ScaffoldResponse> {
    const resp = await api.scaffoldModel({
      mode: 'new',
      ...payload,
    });
    if (!resp.ok) {
      throw new Error(resp.error || 'Failed to create new model');
    }
    if (resp.config) {
      await this.applySavedConfig(resp.config);
    } else {
      await this.refreshModels();
    }
    if (resp.model_id) {
      await this.selectModel(resp.model_id);
    }
    return resp;
  }

  async openModelInEditor(path?: string): Promise<void> {
    const targetPath = path || this.activeModel?.py_path || this.activeModel?.path;
    if (!this.hasDesktop || !targetPath) return;
    try {
      const resp = await api.openInEditor(targetPath);
      if (!resp.ok) {
        this.status = { kind: 'error', error: resp.error || 'Failed to open model in editor' };
      }
    } catch (err) {
      this.status = {
        kind: 'error',
        error: err instanceof Error ? err.message : String(err),
      };
    }
  }

  async revealPath(path?: string): Promise<void> {
    const targetPath = path || this.activeModel?.py_path || this.activeModel?.path;
    if (!this.hasDesktop || !targetPath) return;
    try {
      const resp = await api.revealFile(targetPath);
      if (!resp.ok) {
        this.status = { kind: 'error', error: resp.error || 'Failed to reveal file' };
      }
    } catch (err) {
      this.status = {
        kind: 'error',
        error: err instanceof Error ? err.message : String(err),
      };
    }
  }

  async openActiveModelInEditor(): Promise<void> {
    return this.openModelInEditor();
  }

  async revealActiveModel(): Promise<void> {
    return this.revealPath();
  }

  async revealLastExport(): Promise<void> {
    if (!this.hasDesktop || !this.lastExportPath) return;
    try {
      const resp = await api.revealFile(this.lastExportPath);
      if (!resp.ok) {
        this.status = { kind: 'error', error: resp.error || 'Failed to reveal exported file' };
      }
    } catch (err) {
      this.status = {
        kind: 'error',
        error: err instanceof Error ? err.message : String(err),
      };
    }
  }

  async openLastExportInSlicer(): Promise<void> {
    if (!this.hasDesktop || !this.lastExportPath) return;
    try {
      const resp = await api.openInSlicer(this.lastExportPath);
      if (!resp.ok) {
        this.status = { kind: 'error', error: resp.error || 'Failed to open in slicer' };
      }
    } catch (err) {
      this.status = {
        kind: 'error',
        error: err instanceof Error ? err.message : String(err),
      };
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

  handleFileChanged(changedPath = ''): Promise<void> {
    this.flashReloadBadge();

    if (this.fileReloadInFlight) {
      if (this.queuedFileChangedPath === null) {
        this.queuedFileChangedPath = changedPath;
      } else if (changedPath === '' || this.queuedFileChangedPath === '') {
        this.queuedFileChangedPath = '';
      } else if (this.queuedFileChangedPath !== changedPath) {
        this.queuedFileChangedPath = '';
      }
      return this.fileReloadInFlight;
    }

    let promise!: Promise<void>;
    promise = (async () => {
      try {
        await this.runFileChangedLoop(changedPath);
      } finally {
        if (this.fileReloadInFlight === promise) {
          this.fileReloadInFlight = null;
        }
      }
    })();
    this.fileReloadInFlight = promise;
    return promise;
  }

  private async runFileChangedLoop(initialPath: string): Promise<void> {
    let nextPath: string | null = initialPath;

    while (nextPath !== null) {
      const currentPath = nextPath;
      const seq = ++this.fileReloadSeq;

      await this.refreshModels();
      if (seq !== this.fileReloadSeq) {
        nextPath = this.queuedFileChangedPath;
        this.queuedFileChangedPath = null;
        continue;
      }

      const modelId = this.activeModelId;
      if (modelId && shouldReloadActiveModel(currentPath, this.activeModel, this.models)) {
        const previousParams = deepClone(this.params);
        try {
          const resp = await api.getModel(modelId);
          if (this.activeModelId === modelId && seq === this.fileReloadSeq) {
            if (!resp.ok || !resp.model) {
              this.status = {
                kind: 'error',
                error: resp.error || 'Failed to reload model schema',
                traceback: resp.traceback,
              };
            } else if (resp.model.kind === 'error') {
              this.activeModel = resp.model;
              this.status = {
                kind: 'error',
                error: resp.error || resp.model.parse_error || 'Failed to parse model script',
                traceback: resp.traceback,
              };
            } else {
              this.activeModel = resp.model;
              this.modelDescription = resp.description ?? '';
              this.fields = resp.fields ?? [];
              this.defaults = deepClone(resp.defaults ?? {});
              this.components = resp.components ?? {};
              this.params = normalizeParamsWithFields(
                mergeWithDefaults(this.defaults, previousParams, this.fields),
                this.fields
              );
              this.presets = resp.presets ?? [];
              this.defaultPresetKey = resp.default_preset_key ?? '';
              if (
                this.selectedPresetKey !== 'default' &&
                !this.presets.some((p) => p.key === this.selectedPresetKey)
              ) {
                this.selectedPresetKey = 'default';
              }
              const activePreset =
                this.presets.find((p) => p.key === this.selectedPresetKey) ?? null;
              this.selectedComponentPresets = {
                ...extractComponentPresetSelections(activePreset, this.fields),
                ...this.selectedComponentPresets,
              };
              this.modelLoadError = '';
              this.modelLoadTraceback = '';
              this.lastRenderedKey = '';

              await this.triggerRender(false, true);
            }
          }
        } catch (err) {
          if (this.activeModelId === modelId && seq === this.fileReloadSeq) {
            this.status = {
              kind: 'error',
              error: err instanceof Error ? err.message : String(err),
              traceback: err instanceof ApiRequestError ? err.traceback : undefined,
            };
          }
        }
      }

      nextPath = this.queuedFileChangedPath;
      this.queuedFileChangedPath = null;
    }
  }

  private async handlePresetsChanged(): Promise<void> {
    const modelId = this.activeModelId;
    if (!modelId) return;
    const wasModified = this.paramsModified;
    try {
      const resp = await api.getPresets(modelId);
      if (this.activeModelId === modelId && resp.ok) {
        this.presets = resp.presets;
        if (resp.components) {
          this.components = resp.components;
        }
        if (resp.default_preset_key !== undefined) {
          this.defaultPresetKey = resp.default_preset_key;
        }
        // If user has no local unsaved edits, re-resolve active preset so updated component presets apply immediately.
        if (!wasModified) {
          const activePreset =
            this.presets.find((p) => p.key === this.selectedPresetKey) ?? null;
          const nextParams = resolvePresetParams(
            this.defaults,
            this.fields,
            activePreset,
            this.components
          );
          if (!paramsEqual(this.params, nextParams)) {
            this.params = nextParams;
            void this.triggerRender(false);
          }
        }
      }
    } catch {
      // ignore transient errors
    }
  }
}

export const studio = new StudioState();
