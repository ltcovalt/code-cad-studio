export interface Library {
  id: string;
  name: string;
  path: string;
}

export type ExportQuality = 'draft' | 'standard' | 'fine' | 'custom';

export interface Config {
  schema_version?: number;
  libraries: Library[];
  python_path: string;
  export_dir?: string;
  theme: string;
  preset_save_scope?: 'user' | 'project' | string;
  default_presets?: Record<string, string>;
  onboarding_completed?: boolean;
  onboarding_skipped?: boolean;
  editor_command?: string;
  slicer_command?: string;
  export_quality?: ExportQuality;
}

export interface HealthResponse {
  ok: boolean;
  python: string;
  build123d: boolean;
  build123d_error: string;
  ocp_tessellate: boolean;
  libraries?: Library[];
}

export interface ConfigResponse {
  config: Config;
  resolved_python: string;
  resolved_export_dir: string;
  needs_onboarding?: boolean;
  has_desktop: boolean;
  os?: string;
  worker_running: boolean;
  worker_starting?: boolean;
  worker_error: string;
  health?: HealthResponse | null;
  health_error?: string;
}

export interface DetectedProject {
  path: string;
  name: string;
  model_count: number;
  has_venv: boolean;
}

export interface ProjectDirInfo {
  path: string;
  exists: boolean;
  is_dir: boolean;
  is_empty: boolean;
  has_models: boolean;
  model_count: number;
  has_venv: boolean;
  venv_python?: string;
  has_requirements: boolean;
}

export interface SetupDefaultsResponse {
  default_download_dir: string;
  studio_venv_dir: string;
  detected_projects: DetectedProject[];
  default_repo_url: string;
  default_archive_url: string;
  os: string;
}

export interface SetupInspectDirResponse {
  ok: boolean;
  info: ProjectDirInfo;
  error?: string;
}

export interface PythonCandidate {
  path: string;
  source: string;
  source_detail?: string;
  version: string;
  version_ok: boolean;
  has_build123d: boolean;
  build123d_version?: string;
  has_ocp_tessellate: boolean;
  has_pip: boolean;
  has_venv: boolean;
  sys_prefix?: string;
  error?: string;
}

export interface SetupProbeResponse {
  candidates: PythonCandidate[];
  recommended?: PythonCandidate | null;
  has_uv: boolean;
  uv_path?: string;
  os: string;
  studio_venv_dir: string;
  requirements_files: string[];
}

export interface SetupDownloadResponse {
  ok: boolean;
  cancelled?: boolean;
  project_info?: ProjectDirInfo;
  files_added?: number;
  files_overwritten?: number;
  merged?: boolean;
  error?: string;
}

export interface SetupInstallEnvResponse {
  ok: boolean;
  cancelled?: boolean;
  venv_dir?: string;
  python_path?: string;
  candidate?: PythonCandidate;
  used_uv?: boolean;
  error?: string;
}

export interface SetupProgressEvent {
  task: 'download' | 'install' | string;
  stage: string;
  message: string;
  bytes_downloaded?: number;
  total_bytes?: number;
  files_extracted?: number;
  total_files?: number;
}

export interface SetupLogEvent {
  task: string;
  stage: string;
  line: string;
}

export interface HealthCheckResponse {
  worker_running: boolean;
  worker_starting?: boolean;
  worker_error: string;
  health?: HealthResponse | null;
  health_error?: string;
}

export interface DialogResponse {
  path: string;
  cancelled: boolean;
  error?: string;
}

export type ModelKind = 'parametric' | 'script' | 'error';

export interface ModelInfo {
  id: string;
  local_id: string;
  library_id: string;
  library_name: string;
  folders: string[];
  folder_labels: string[];
  name: string;
  path: string;
  rel_path: string;
  py_path?: string;
  presets_path?: string;
  kind?: ModelKind;
  parse_error?: string;
  description: string;
  params_class: string;
  entrypoint: string;
  dir: string;
}

export interface ModelsResponse {
  ok: boolean;
  libraries: Library[];
  models: ModelInfo[];
  library_errors?: Record<string, string>;
  error?: string;
}

export type ParamFieldType = 'bool' | 'int' | 'float' | 'str' | 'enum' | 'group' | 'list';

export interface ParamField {
  name: string;
  path: string;
  label: string;
  type: ParamFieldType;
  default: unknown;
  description: string;
  unit?: string;
  nullable?: boolean;
  when?: Record<string, unknown>;
  choices?: string[];
  min?: number;
  max?: number;
  step?: number;
  children?: ParamField[];
  item?: ParamField;
}

export type ParamsRecord = Record<string, unknown>;

export type ParamUpdateMode = 'debounced' | 'immediate' | 'preview';

export type VerificationStatus = 'unverified' | 'unusable' | 'usable' | 'verified';

export interface PresetVerification {
  status: VerificationStatus;
  note?: string;
}

export interface Preset {
  key: string;
  label: string;
  description: string;
  source: 'default' | 'project' | 'user' | string;
  verification?: PresetVerification | null;
  params: ParamsRecord;
}

export interface ModelDetailResponse {
  ok: boolean;
  model?: ModelInfo;
  description?: string;
  fields?: ParamField[];
  defaults?: ParamsRecord;
  presets?: Preset[];
  default_preset_key?: string;
  error?: string;
  code?: string;
  traceback?: string;
}

export interface BBox {
  xmin: number;
  xmax: number;
  ymin: number;
  ymax: number;
  zmin: number;
  zmax: number;
  dx: number;
  dy: number;
  dz: number;
}

export interface RenderResponse {
  ok: boolean;
  superseded?: boolean;
  cached?: boolean;
  model_id?: string;
  build_time_ms?: number;
  tessellate_time_ms?: number;
  bbox?: BBox | null;
  shapes?: unknown;
  export_filename?: string;
  error?: string;
  code?: string;
  traceback?: string;
}

export interface CancelRenderResponse {
  ok: boolean;
  cancelled?: boolean;
  error?: string;
  code?: string;
}

export interface ExportResult {
  ok: boolean;
  cancelled?: boolean;
  path?: string;
  size?: number;
  error?: string;
  code?: string;
  traceback?: string;
}

export interface ScaffoldRequest {
  mode: 'demo' | 'new';
  target_dir?: string;
  library_id?: string;
  name?: string;
  template?: string;
}

export interface ScaffoldResponse {
  ok: boolean;
  target_dir?: string;
  library_id?: string;
  model_id?: string;
  py_path?: string;
  config?: ConfigResponse;
  error?: string;
  code?: string;
}

export interface DesktopActionResponse {
  ok: boolean;
  error?: string;
  code?: string;
}

export interface PresetsResponse {
  ok: boolean;
  presets: Preset[];
  default_preset_key?: string;
  error?: string;
}

export interface SavePresetResponse {
  ok: boolean;
  selected_key?: string;
  presets?: Preset[];
  preset_save_scope?: string;
  error?: string;
}

export interface DeletePresetResponse {
  ok: boolean;
  presets?: Preset[];
  default_preset_key?: string;
  error?: string;
}

export interface RenamePresetResponse {
  ok: boolean;
  key?: string;
  presets?: Preset[];
  default_preset_key?: string;
  error?: string;
}

export interface SetDefaultPresetResponse {
  ok: boolean;
  default_preset_key?: string;
  error?: string;
}

export interface SetPresetVerificationResponse {
  ok: boolean;
  presets?: Preset[];
  error?: string;
}

export interface DefaultChange {
  path: string;
  line: number;
  old_value: unknown;
  new_value: unknown;
  old_literal: string;
  new_literal: string;
}

export interface UnsupportedDefaultChange {
  path: string;
  reason: string;
  reason_code?: string;
  owner_class?: string;
  source_file?: string;
  source_rel_path?: string;
  in_library?: boolean;
}

export interface UpdateDefaultsResponse {
  ok: boolean;
  file?: string;
  rel_path?: string;
  sha256?: string;
  updated?: number;
  changes: DefaultChange[];
  unsupported: UnsupportedDefaultChange[];
  error?: string;
  code?: string;
  traceback?: string;
}

export type LayoutMode = 'stacked' | 'side-by-side' | 'right';

export interface LayoutState {
  mode: LayoutMode;
  sidebarWidth: number;
  modelsHeight: number;
  modelsWidth: number;
  paramsWidth: number;
}

export type ExportFormat = 'step' | 'stl' | '3mf';

export type StatusState =
  | { kind: 'idle' }
  | { kind: 'ok'; bbox: BBox | null; buildTimeMs: number; tessellateTimeMs: number; cached?: boolean }
  | { kind: 'message'; message: string }
  | { kind: 'error'; error: string; traceback?: string };
