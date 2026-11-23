import type {
  ConfigResponse,
  DeletePresetResponse,
  DialogResponse,
  ExportFormat,
  ExportResult,
  HealthCheckResponse,
  Library,
  ModelDetailResponse,
  ModelsResponse,
  ParamsRecord,
  PresetVerification,
  PresetsResponse,
  RenamePresetResponse,
  RenderResponse,
  SavePresetResponse,
  SetDefaultPresetResponse,
  SetPresetVerificationResponse,
  SetupDefaultsResponse,
  SetupDownloadResponse,
  SetupInspectDirResponse,
  SetupInstallEnvResponse,
  SetupLogEvent,
  SetupProbeResponse,
  SetupProgressEvent,
  UpdateDefaultsResponse,
  VerificationStatus,
} from './types';

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, {
    cache: 'no-store',
    ...init,
    headers: {
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...(init?.headers ?? {}),
    },
  });
  const text = await res.text();
  if (!text) {
    if (!res.ok) {
      throw new Error(`Request failed (${res.status})`);
    }
    return undefined as T;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    throw new Error(res.ok ? `Invalid JSON response from ${url}` : `HTTP ${res.status}: ${text}`);
  }
  if (!res.ok && typeof parsed === 'object' && parsed !== null && 'error' in parsed) {
    throw new Error(String((parsed as { error: unknown }).error));
  }
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}: ${text}`);
  }
  return parsed as T;
}

export function getConfig(): Promise<ConfigResponse> {
  return fetchJSON<ConfigResponse>('/api/config');
}

export function saveConfig(
  libraries: Library[],
  pythonPath: string,
  exportDir: string
): Promise<ConfigResponse> {
  return fetchJSON<ConfigResponse>('/api/config', {
    method: 'POST',
    body: JSON.stringify({
      libraries,
      python_path: pythonPath,
      export_dir: exportDir,
    }),
  });
}

export async function saveTheme(theme: string): Promise<void> {
  await fetch('/api/config/theme', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ theme }),
  });
}

export function getHealth(): Promise<HealthCheckResponse> {
  return fetchJSON<HealthCheckResponse>('/api/health');
}

export function getSetupDefaults(): Promise<SetupDefaultsResponse> {
  return fetchJSON<SetupDefaultsResponse>('/api/setup/defaults');
}

export function inspectSetupDir(path: string): Promise<SetupInspectDirResponse> {
  return fetchJSON<SetupInspectDirResponse>('/api/setup/inspect-dir', {
    method: 'POST',
    body: JSON.stringify({ path }),
  });
}

export function probeSetup(
  libraryRoots: string[],
  pythonPath = ''
): Promise<SetupProbeResponse> {
  return fetchJSON<SetupProbeResponse>('/api/setup/probe', {
    method: 'POST',
    body: JSON.stringify({
      library_roots: libraryRoots,
      python_path: pythonPath,
    }),
  });
}

export function downloadStarterProject(
  targetDir: string,
  archiveUrl = '',
  allowOverwrite = false
): Promise<SetupDownloadResponse> {
  return fetchJSON<SetupDownloadResponse>('/api/setup/download-project', {
    method: 'POST',
    body: JSON.stringify({
      target_dir: targetDir,
      archive_url: archiveUrl,
      allow_overwrite: allowOverwrite,
    }),
  });
}

export function installSetupEnv(payload: {
  library_roots: string[];
  base_python: string;
  use_uv?: boolean;
}): Promise<SetupInstallEnvResponse> {
  return fetchJSON<SetupInstallEnvResponse>('/api/setup/install-env', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export function cancelSetupTask(): Promise<{ ok: boolean; cancelled: boolean }> {
  return fetchJSON<{ ok: boolean; cancelled: boolean }>('/api/setup/cancel', {
    method: 'POST',
  });
}

export function completeSetup(payload: {
  libraries: Library[];
  export_dir?: string;
  python_path?: string;
}): Promise<ConfigResponse> {
  return fetchJSON<ConfigResponse>('/api/setup/complete', {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export function browseProject(current?: string): Promise<DialogResponse> {
  return fetchJSON<DialogResponse>('/api/dialogs/project', {
    method: 'POST',
    body: JSON.stringify({ current: current ?? '' }),
  });
}

export function browsePython(current?: string): Promise<DialogResponse> {
  return fetchJSON<DialogResponse>('/api/dialogs/python', {
    method: 'POST',
    body: JSON.stringify({ current: current ?? '' }),
  });
}

export function browseExportDir(current?: string): Promise<DialogResponse> {
  return fetchJSON<DialogResponse>('/api/dialogs/export-dir', {
    method: 'POST',
    body: JSON.stringify({ current: current ?? '' }),
  });
}

export async function windowMinimize(): Promise<void> {
  await fetch('/api/window/minimize', { method: 'POST' });
}

export function windowToggleMaximize(): Promise<{ maximized: boolean }> {
  return fetchJSON<{ maximized: boolean }>('/api/window/toggle-maximize', {
    method: 'POST',
  });
}

export function windowState(): Promise<{ maximized: boolean }> {
  return fetchJSON<{ maximized: boolean }>('/api/window/state');
}

export async function windowClose(): Promise<void> {
  await fetch('/api/window/close', { method: 'POST' });
}

export function listModels(): Promise<ModelsResponse> {
  return fetchJSON<ModelsResponse>('/api/models');
}

export function getModel(modelId: string): Promise<ModelDetailResponse> {
  return fetchJSON<ModelDetailResponse>(`/api/models/${encodeURIComponent(modelId)}`);
}

export function renderModel(
  modelId: string,
  params: ParamsRecord,
  signal?: AbortSignal
): Promise<RenderResponse> {
  return fetchJSON<RenderResponse>(`/api/models/${encodeURIComponent(modelId)}/render`, {
    method: 'POST',
    body: JSON.stringify({ params }),
    signal,
  });
}

function parseContentDispositionFilename(header: string | null, fallback: string): string {
  if (!header) return fallback;
  const match = /filename="([^"]+)"/i.exec(header) || /filename=([^;\s]+)/i.exec(header);
  return match && match[1] ? match[1] : fallback;
}

export async function exportModel(
  modelId: string,
  params: ParamsRecord,
  format: ExportFormat,
  hasDesktop: boolean
): Promise<ExportResult> {
  const url = `/api/models/${encodeURIComponent(modelId)}/export`;
  if (hasDesktop) {
    return fetchJSON<ExportResult>(url, {
      method: 'POST',
      body: JSON.stringify({ params, format }),
    });
  }

  const res = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ params, format }),
  });
  if (!res.ok) {
    const errText = await res.text();
    return { ok: false, error: errText || `Export failed (${res.status})` };
  }
  const blob = await res.blob();
  const filename = parseContentDispositionFilename(
    res.headers.get('Content-Disposition'),
    `${modelId}.${format}`
  );
  const blobUrl = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = blobUrl;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(blobUrl);
  return { ok: true, cancelled: false, path: filename, size: blob.size };
}

export function getPresets(modelId: string): Promise<PresetsResponse> {
  return fetchJSON<PresetsResponse>(`/api/models/${encodeURIComponent(modelId)}/presets`);
}

export function savePreset(
  modelId: string,
  payload: {
    label: string;
    description: string;
    scope: string;
    full_snapshot?: boolean;
    params: ParamsRecord;
    verification?: PresetVerification | null;
  }
): Promise<SavePresetResponse> {
  return fetchJSON<SavePresetResponse>(`/api/models/${encodeURIComponent(modelId)}/presets`, {
    method: 'POST',
    body: JSON.stringify(payload),
  });
}

export function setPresetVerification(
  modelId: string,
  key: string,
  payload: {
    scope: string;
    status: VerificationStatus;
    note: string;
  }
): Promise<SetPresetVerificationResponse> {
  return fetchJSON<SetPresetVerificationResponse>(
    `/api/models/${encodeURIComponent(modelId)}/presets/${encodeURIComponent(key)}/verification`,
    {
      method: 'PUT',
      body: JSON.stringify(payload),
    }
  );
}

export function deletePreset(
  modelId: string,
  key: string,
  scope: string
): Promise<DeletePresetResponse> {
  const qs = `?scope=${encodeURIComponent(scope)}`;
  return fetchJSON<DeletePresetResponse>(
    `/api/models/${encodeURIComponent(modelId)}/presets/${encodeURIComponent(key)}${qs}`,
    { method: 'DELETE' }
  );
}

export function renamePreset(
  modelId: string,
  key: string,
  payload: { scope: string; label: string; description: string }
): Promise<RenamePresetResponse> {
  return fetchJSON<RenamePresetResponse>(
    `/api/models/${encodeURIComponent(modelId)}/presets/${encodeURIComponent(key)}`,
    {
      method: 'PATCH',
      body: JSON.stringify(payload),
    }
  );
}

export function setDefaultPreset(
  modelId: string,
  key: string
): Promise<SetDefaultPresetResponse> {
  return fetchJSON<SetDefaultPresetResponse>(
    `/api/models/${encodeURIComponent(modelId)}/presets/default`,
    {
      method: 'POST',
      body: JSON.stringify({ key }),
    }
  );
}

export function updateModelDefaults(
  modelId: string,
  payload: { params: ParamsRecord; dry_run: boolean; expected_sha256?: string }
): Promise<UpdateDefaultsResponse> {
  return fetchJSON<UpdateDefaultsResponse>(
    `/api/models/${encodeURIComponent(modelId)}/update-defaults`,
    {
      method: 'POST',
      body: JSON.stringify(payload),
    }
  );
}

export interface SSEHandlers {
  onFileChanged: (path: string) => void;
  onPresetsChanged: (path: string) => void;
  onWorkerError: (error: string) => void;
  onSetupProgress?: (event: SetupProgressEvent) => void;
  onSetupLog?: (event: SetupLogEvent) => void;
}

export function subscribeEvents(handlers: SSEHandlers): () => void {
  let closed = false;
  let es: EventSource | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

  const connect = () => {
    if (closed) return;
    es = new EventSource('/events');

    es.addEventListener('file-changed', (evt) => {
      try {
        const data = JSON.parse((evt as MessageEvent).data) as { path?: string };
        handlers.onFileChanged(data.path ?? '');
      } catch {
        handlers.onFileChanged('');
      }
    });

    es.addEventListener('presets-changed', (evt) => {
      try {
        const data = JSON.parse((evt as MessageEvent).data) as { path?: string };
        handlers.onPresetsChanged(data.path ?? '');
      } catch {
        handlers.onPresetsChanged('');
      }
    });

    es.addEventListener('worker-error', (evt) => {
      try {
        const data = JSON.parse((evt as MessageEvent).data) as { error?: string };
        handlers.onWorkerError(data.error ?? 'Worker reload error');
      } catch {
        handlers.onWorkerError('Worker reload error');
      }
    });

    es.addEventListener('setup-progress', (evt) => {
      try {
        const data = JSON.parse((evt as MessageEvent).data) as SetupProgressEvent;
        handlers.onSetupProgress?.(data);
      } catch {
        // ignore malformed progress payload
      }
    });

    es.addEventListener('setup-log', (evt) => {
      try {
        const data = JSON.parse((evt as MessageEvent).data) as SetupLogEvent;
        handlers.onSetupLog?.(data);
      } catch {
        // ignore malformed log payload
      }
    });

    es.onerror = () => {
      es?.close();
      es = null;
      if (!closed) {
        reconnectTimer = setTimeout(connect, 3000);
      }
    };
  };

  connect();

  return () => {
    closed = true;
    if (reconnectTimer) clearTimeout(reconnectTimer);
    es?.close();
    es = null;
  };
}
