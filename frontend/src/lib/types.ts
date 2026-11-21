export interface Config {
  project_root: string;
  python_path: string;
  theme: string;
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

export type ParamFieldType = 'bool' | 'int' | 'float' | 'str' | 'enum' | 'group';

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
}

export type ParamsRecord = Record<string, unknown>;

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
  error?: string;
}

export interface SavePresetResponse {
  ok: boolean;
  selected_key?: string;
  presets?: Preset[];
  error?: string;
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
