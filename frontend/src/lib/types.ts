export interface Config {
  project_root: string;
  python_path: string;
  export_dir?: string;
  theme: string;
  preset_save_scope?: 'user' | 'project' | string;
  default_presets?: Record<string, string>;
}

export interface HealthResponse {
  ok: boolean;
  python: string;
  build123d: boolean;
  build123d_error: string;
  ocp_tessellate: boolean;
  project_root: string;
}

export interface ConfigResponse {
  config: Config;
  resolved_python: string;
  resolved_export_dir: string;
  has_desktop: boolean;
  worker_running: boolean;
  worker_starting?: boolean;
  worker_error: string;
  health?: HealthResponse | null;
  health_error?: string;
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

export interface ModelInfo {
  id: string;
  name: string;
  path: string;
  rel_path: string;
  description: string;
  params_class: string;
  entrypoint: string;
  dir: string;
}

export interface ModelsResponse {
  ok: boolean;
  project_root: string;
  models: ModelInfo[];
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

export interface Preset {
  key: string;
  label: string;
  description: string;
  source: 'default' | 'project' | 'user' | string;
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
  traceback?: string;
}

export interface ExportResult {
  ok: boolean;
  cancelled?: boolean;
  path?: string;
  size?: number;
  error?: string;
  traceback?: string;
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
