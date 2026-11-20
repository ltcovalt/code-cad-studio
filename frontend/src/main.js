import htmx from 'htmx.org';
import { Display, Viewer } from 'three-cad-viewer';
import 'three-cad-viewer/css';
import './styles.css';

// Expose htmx globally so inline attributes & debugging work seamlessly
window.htmx = htmx;

// Stepper button helper for numeric parameter inputs (handles float precision + min/max + range sync)
window.stepNumberInput = function (btn, direction) {
  const wrap = btn.closest('.join');
  const input = wrap ? wrap.querySelector('input[type="number"]') : null;
  if (!input) return;

  const stepRaw = input.step && input.step !== 'any' ? input.step : '1';
  const step = parseFloat(stepRaw) || 1;
  const decimals = (stepRaw.split('.')[1] || '').length;
  const current = parseFloat(input.value);
  let next = (Number.isFinite(current) ? current : 0) + direction * step;

  if (input.min !== '') {
    const minVal = parseFloat(input.min);
    if (Number.isFinite(minVal)) next = Math.max(minVal, next);
  }
  if (input.max !== '') {
    const maxVal = parseFloat(input.max);
    if (Number.isFinite(maxVal)) next = Math.min(maxVal, next);
  }

  const rounded = decimals > 0 ? Number(next.toFixed(decimals)) : Math.round(next);
  input.value = String(rounded);

  const formControl = input.closest('.form-control');
  const range = formControl ? formControl.querySelector('input[type="range"]') : null;
  if (range) range.value = input.value;

  input.dispatchEvent(new Event('input', { bubbles: true }));
};

// Copy error message + traceback to clipboard
window.copyErrorText = async function (btn) {
  const box = btn.closest('.alert');
  if (!box) return;
  const msg = box.querySelector('.error-copy-msg')?.textContent || '';
  const tb = box.querySelector('.error-copy-tb')?.textContent || '';
  const text = tb ? `${msg}\n\n${tb}` : msg;
  try {
    await navigator.clipboard.writeText(text);
    const prev = btn.textContent;
    btn.textContent = 'Copied!';
    setTimeout(() => { btn.textContent = prev; }, 1500);
  } catch {
    // Fallback selection copy
    const ta = document.createElement('textarea');
    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    document.execCommand('copy');
    ta.remove();
  }
};

const LIGHT_THEMES = new Set(['light', 'cupcake', 'emerald', 'corporate', 'retro']);

class CadViewportController {
  constructor(container) {
    this.container = container;
    this.display = null;
    this.viewer = null;
    this.hasRendered = false;
    this.lastModelId = null;
    this.theme = 'dark';
    this.daisyTheme = 'dark';
    this.treeWidth = 220;
    this._initResizeObserver();
  }

  _getDimensions() {
    const rect = this.container.getBoundingClientRect();
    const width = Math.max(240, Math.floor(rect.width));
    const height = Math.max(200, Math.floor(rect.height));
    // three-cad-viewer sets toolbar & body width to (cadWidth + 2) and body height to (height + 4);
    // toolbar has 2px bottom margin in styles.css
    const toolbarEl = this.display?.cadTool?.container;
    const toolbarH = toolbarEl && toolbarEl.offsetHeight > 0 ? toolbarEl.offsetHeight : 36;
    const cadWidth = Math.max(200, width - 2);
    const cadHeight = Math.max(160, height - toolbarH - 6);
    return {
      cadWidth,
      height: cadHeight,
      treeWidth: Math.min(this.treeWidth, Math.floor(cadWidth * 0.3)),
    };
  }

  _syncSize() {
    if (!this.viewer || !this.hasRendered) return;
    const { cadWidth, height, treeWidth } = this._getDimensions();
    try {
      this.viewer.resizeCadView(cadWidth, treeWidth, height, true);
    } catch (err) {
      console.warn('resizeCadView warning:', err);
    }
  }

  _ensureInitialized() {
    if (this.viewer) return;
    const { cadWidth, height, treeWidth } = this._getDimensions();

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
      externalMeasurementBackend: false,
    };

    this.display = new Display(this.container, displayOptions);
    this.viewer = new Viewer(this.display, displayOptions, null);
    // three-cad-viewer overwrites document.body[data-theme] with "light"|"dark";
    // restore the active daisyUI theme on <body> while keeping #cad-viewer-root on light|dark.
    document.body.setAttribute('data-theme', this.daisyTheme);
  }

  _initResizeObserver() {
    let rafId = null;
    const ro = new ResizeObserver(() => {
      if (!this.viewer || !this.hasRendered) return;
      if (rafId) cancelAnimationFrame(rafId);
      rafId = requestAnimationFrame(() => {
        this._syncSize();
      });
    });
    ro.observe(this.container);
  }

  setTheme(daisyTheme) {
    this.daisyTheme = daisyTheme || 'dark';
    const mode = LIGHT_THEMES.has(this.daisyTheme) ? 'light' : 'dark';
    this.theme = mode;
    if (this.viewer && this.hasRendered) {
      try {
        this.viewer.setTheme(mode);
      } catch (err) {
        console.warn('setTheme warning:', err);
      }
    }
    this.container.setAttribute('data-theme', mode);
    // Restore daisyUI theme on <body> after three-cad-viewer mutates it
    document.body.setAttribute('data-theme', this.daisyTheme);
  }

  async loadMesh(modelId, resetCamera = false) {
    const res = await fetch(`/api/models/${encodeURIComponent(modelId)}/mesh`, {
      cache: 'no-store',
    });
    if (!res.ok) {
      throw new Error(`Failed to fetch mesh (${res.status})`);
    }
    const shapes = await res.json();

    const emptyState = document.getElementById('viewer-empty-state');
    if (emptyState) emptyState.classList.add('hidden');

    this._ensureInitialized();

    const preserveCamera = this.hasRendered && !resetCamera && this.lastModelId === modelId;
    let savedCam = null;
    if (preserveCamera) {
      try {
        savedCam = this.viewer.getCameraLocationSettings();
      } catch {
        savedCam = null;
      }
    }

    const renderOptions = {
      edgeColor: 0x555555,
      ambientIntensity: 1.0,
      directIntensity: 1.1,
      metalness: 0.3,
      roughness: 0.65,
      defaultOpacity: 0.5,
    };

    const viewerOptions = {
      control: 'orbit',
      axes: true,
      axes0: true,
      grid: [true, false, false],
      centerGrid: true,
      ortho: true,
      transparent: false,
      blackEdges: false,
      up: 'Z',
      collapse: 1,
    };

    if (this.hasRendered) {
      try {
        this.viewer.clear();
      } catch (err) {
        console.warn('viewer.clear warning:', err);
      }
    }

    this.viewer.render(shapes, renderOptions, viewerOptions);
    this.hasRendered = true;
    this.lastModelId = modelId;
    requestAnimationFrame(() => this._syncSize());

    if (savedCam && savedCam.position && savedCam.quaternion && savedCam.target) {
      try {
        this.viewer.setCameraLocationSettings(
          savedCam.position,
          savedCam.quaternion,
          savedCam.target,
          savedCam.zoom,
          false
        );
      } catch (err) {
        console.warn('restore camera warning:', err);
      }
    }
  }
}

const viewerContainer = document.getElementById('cad-viewer-root');
const viewport = new CadViewportController(viewerContainer);

// --- Resizable Layout Controller (Single Column, Side-by-Side, Right Parameters) ---
const VALID_LAYOUTS = new Set(['stacked', 'side-by-side', 'right']);

const LAYOUT_ICONS = {
  stacked:
    '<rect x="3" y="4" width="18" height="16" rx="2" /><line x1="10" y1="4" x2="10" y2="20" /><line x1="3" y1="12" x2="10" y2="12" />',
  'side-by-side':
    '<rect x="3" y="4" width="18" height="16" rx="2" /><line x1="8" y1="4" x2="8" y2="20" /><line x1="13" y1="4" x2="13" y2="20" />',
  right:
    '<rect x="3" y="4" width="18" height="16" rx="2" /><line x1="8" y1="4" x2="8" y2="20" /><line x1="16" y1="4" x2="16" y2="20" />',
};

class LayoutController {
  constructor() {
    this.state = this._loadState();
    this._bindOuterSplitter();
    this._bindRightSplitter();
    this._bindGlobalClicks();
    window.addEventListener('resize', () => this.apply());
    this.apply();
  }

  _loadState() {
    const defaults = {
      mode: 'stacked', // 'stacked' | 'side-by-side' | 'right'
      sidebarWidth: 320,
      modelsHeight: 220,
      modelsWidth: 210,
      paramsWidth: 320,
    };
    try {
      const raw = localStorage.getItem('code-cad-layout');
      if (raw) {
        const parsed = { ...defaults, ...JSON.parse(raw) };
        if (!VALID_LAYOUTS.has(parsed.mode)) parsed.mode = 'stacked';
        return parsed;
      }
    } catch {
      // ignore
    }
    return defaults;
  }

  _saveState() {
    try {
      localStorage.setItem('code-cad-layout', JSON.stringify(this.state));
    } catch {
      // ignore
    }
  }

  setMode(mode) {
    if (!VALID_LAYOUTS.has(mode) || this.state.mode === mode) return;
    this.state.mode = mode;
    this._saveState();
    this.apply();
  }

  apply() {
    const sidebar = document.getElementById('sidebar-slot');
    const sidebarPanes = document.getElementById('sidebar-panes');
    const modelsPane = document.getElementById('models-pane');
    const rightSlot = document.getElementById('right-panel-slot');
    const rightSplitter = document.getElementById('splitter-viewport-right');
    const workspace = document.getElementById('workspace-root');
    if (!sidebar) return;

    const wsWidth = workspace ? workspace.clientWidth : 1200;
    const maxTotal = Math.max(420, Math.floor(wsWidth * 0.72));
    sidebar.setAttribute('data-sidebar-layout', this.state.mode);

    // Locate #model-workspace and reconcile if HTMX swapped #sidebar-slot while docked right
    const inSidebar = sidebarPanes ? sidebarPanes.querySelector('#model-workspace') : null;
    const inRight = rightSlot ? rightSlot.querySelector('#model-workspace') : null;
    if (inSidebar && inRight) {
      inRight.remove();
    }
    const modelWorkspace = inSidebar || inRight;

    if (this.state.mode === 'right') {
      if (rightSlot && modelWorkspace && modelWorkspace.parentElement !== rightSlot) {
        rightSlot.appendChild(modelWorkspace);
      }
      if (rightSlot) rightSlot.classList.remove('hidden');
      if (rightSplitter) rightSplitter.classList.remove('hidden');

      const maxModels = Math.min(420, Math.max(150, Math.floor(wsWidth * 0.35)));
      const mw = Math.max(150, Math.min(maxModels, this.state.modelsWidth));
      const maxParams = Math.min(520, Math.max(220, wsWidth - mw - 260));
      const pw = Math.max(220, Math.min(maxParams, this.state.paramsWidth));

      sidebar.style.width = `${mw}px`;
      if (rightSlot) rightSlot.style.width = `${pw}px`;
      if (modelsPane) {
        modelsPane.style.width = '100%';
        modelsPane.style.height = '100%';
      }
    } else {
      if (sidebarPanes && modelWorkspace && modelWorkspace.parentElement !== sidebarPanes) {
        sidebarPanes.appendChild(modelWorkspace);
      }
      if (rightSlot) rightSlot.classList.add('hidden');
      if (rightSplitter) rightSplitter.classList.add('hidden');

      if (this.state.mode === 'stacked') {
        const w = Math.max(220, Math.min(maxTotal, this.state.sidebarWidth));
        sidebar.style.width = `${w}px`;
        if (modelsPane) {
          modelsPane.style.width = '100%';
          const maxH = workspace ? Math.max(120, Math.floor(workspace.clientHeight * 0.7)) : 500;
          const h = Math.max(100, Math.min(maxH, this.state.modelsHeight));
          modelsPane.style.height = `${h}px`;
        }
      } else {
        const mw = Math.max(150, Math.min(420, this.state.modelsWidth));
        const pw = Math.max(220, Math.min(Math.max(220, maxTotal - mw), this.state.paramsWidth));
        sidebar.style.width = `${mw + 5 + pw}px`;
        if (modelsPane) {
          modelsPane.style.width = `${mw}px`;
          modelsPane.style.height = '100%';
        }
      }
    }

    this._updateLayoutMenu();
    this._bindInnerSplitter();
  }

  _updateLayoutMenu() {
    const icon = document.getElementById('layout-menu-icon');
    if (icon && LAYOUT_ICONS[this.state.mode]) {
      icon.innerHTML = LAYOUT_ICONS[this.state.mode];
    }
    const options = document.querySelectorAll('[data-layout-option]');
    options.forEach((btn) => {
      const active = btn.getAttribute('data-layout-option') === this.state.mode;
      btn.classList.toggle('btn-active', active);
    });
  }

  _bindGlobalClicks() {
    document.addEventListener('click', (evt) => {
      const opt = evt.target && evt.target.closest ? evt.target.closest('[data-layout-option]') : null;
      if (opt) {
        this.setMode(opt.getAttribute('data-layout-option'));
        if (document.activeElement && typeof document.activeElement.blur === 'function') {
          document.activeElement.blur();
        }
      }
    });
  }

  _bindOuterSplitter() {
    const splitter = document.getElementById('splitter-sidebar-viewport');
    if (!splitter) return;

    splitter.addEventListener('pointerdown', (evt) => {
      evt.preventDefault();
      splitter.setPointerCapture(evt.pointerId);
      splitter.classList.add('dragging');
      const startX = evt.clientX;
      const startSidebarW = this.state.sidebarWidth;
      const startParamsW = this.state.paramsWidth;
      const startModelsW = this.state.modelsWidth;

      const onMove = (e) => {
        const dx = e.clientX - startX;
        if (this.state.mode === 'stacked') {
          this.state.sidebarWidth = Math.max(220, startSidebarW + dx);
        } else if (this.state.mode === 'side-by-side') {
          this.state.paramsWidth = Math.max(220, startParamsW + dx);
        } else {
          this.state.modelsWidth = Math.max(150, startModelsW + dx);
        }
        this.apply();
      };

      const onUp = () => {
        splitter.classList.remove('dragging');
        splitter.removeEventListener('pointermove', onMove);
        splitter.removeEventListener('pointerup', onUp);
        splitter.removeEventListener('pointercancel', onUp);
        this._saveState();
      };

      splitter.addEventListener('pointermove', onMove);
      splitter.addEventListener('pointerup', onUp);
      splitter.addEventListener('pointercancel', onUp);
    });
  }

  _bindRightSplitter() {
    const splitter = document.getElementById('splitter-viewport-right');
    if (!splitter) return;

    splitter.addEventListener('pointerdown', (evt) => {
      evt.preventDefault();
      splitter.setPointerCapture(evt.pointerId);
      splitter.classList.add('dragging');
      const startX = evt.clientX;
      const startParamsW = this.state.paramsWidth;

      const onMove = (e) => {
        const dx = e.clientX - startX;
        this.state.paramsWidth = Math.max(220, startParamsW - dx);
        this.apply();
      };

      const onUp = () => {
        splitter.classList.remove('dragging');
        splitter.removeEventListener('pointermove', onMove);
        splitter.removeEventListener('pointerup', onUp);
        splitter.removeEventListener('pointercancel', onUp);
        this._saveState();
      };

      splitter.addEventListener('pointermove', onMove);
      splitter.addEventListener('pointerup', onUp);
      splitter.addEventListener('pointercancel', onUp);
    });
  }

  _bindInnerSplitter() {
    const splitter = document.getElementById('splitter-models-params');
    if (!splitter || splitter.dataset.bound === '1') return;
    splitter.dataset.bound = '1';

    splitter.addEventListener('pointerdown', (evt) => {
      evt.preventDefault();
      splitter.setPointerCapture(evt.pointerId);
      splitter.classList.add('dragging');
      const startX = evt.clientX;
      const startY = evt.clientY;
      const startModelsH = this.state.modelsHeight;
      const startModelsW = this.state.modelsWidth;

      const onMove = (e) => {
        if (this.state.mode === 'stacked') {
          this.state.modelsHeight = Math.max(100, startModelsH + (e.clientY - startY));
        } else {
          this.state.modelsWidth = Math.max(150, startModelsW + (e.clientX - startX));
        }
        this.apply();
      };

      const onUp = () => {
        splitter.classList.remove('dragging');
        splitter.removeEventListener('pointermove', onMove);
        splitter.removeEventListener('pointerup', onUp);
        splitter.removeEventListener('pointercancel', onUp);
        this._saveState();
      };

      splitter.addEventListener('pointermove', onMove);
      splitter.addEventListener('pointerup', onUp);
      splitter.addEventListener('pointercancel', onUp);
    });
  }
}

const layout = new LayoutController();

function applyTheme(theme) {
  const t = theme || 'dark';
  viewport.setTheme(t);
  document.documentElement.setAttribute('data-theme', t);
  document.body.setAttribute('data-theme', t);
  const ctrl = document.querySelector('[data-current-theme]');
  if (ctrl) {
    ctrl.setAttribute('data-current-theme', t);
  }
  try {
    localStorage.setItem('code-cad-theme', t);
  } catch {
    // ignore storage errors
  }
}

// Apply saved theme immediately on startup
try {
  const savedTheme = localStorage.getItem('code-cad-theme');
  if (savedTheme) applyTheme(savedTheme);
} catch {
  // ignore
}

// Sync server-persisted theme & resizable layout whenever HTMX swaps partials into the DOM
document.body.addEventListener('htmx:afterSwap', (evt) => {
  layout.apply();
  const target = evt.detail && evt.detail.target;
  const ctrl = target && target.matches && target.matches('[data-current-theme]')
    ? target
    : (target && target.querySelector ? target.querySelector('[data-current-theme]') : null);
  if (ctrl) {
    const serverTheme = ctrl.getAttribute('data-current-theme');
    if (serverTheme) {
      applyTheme(serverTheme);
    }
  }
});

// Listen for HX-Trigger: {"meshUpdated": { modelId, version, resetCamera }}
document.body.addEventListener('meshUpdated', (evt) => {
  const detail = evt.detail || {};
  if (!detail.modelId) return;
  viewport.loadMesh(detail.modelId, Boolean(detail.resetCamera)).catch((err) => {
    console.error('Failed to render CAD mesh:', err);
  });
});

// Listen for web-mode export downloads (when native Save-As dialog is unavailable)
document.body.addEventListener('downloadExport', async (evt) => {
  const { modelId, format } = evt.detail || {};
  const form = document.getElementById('param-form');
  if (!modelId || !form) return;

  const body = new URLSearchParams(new FormData(form));
  body.set('format', format || 'step');

  try {
    const resp = await fetch(`/api/models/${encodeURIComponent(modelId)}/export`, {
      method: 'POST',
      body,
    });
    if (!resp.ok) throw new Error(await resp.text());
    const blob = await resp.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${modelId}.${format || 'step'}`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  } catch (err) {
    console.error('Export download failed:', err);
  }
});

// Sync daisyUI theme-controller changes with <html> data-theme, three-cad-viewer, and config.json
document.addEventListener('change', (evt) => {
  const target = evt.target;
  if (target && target.classList && target.classList.contains('theme-controller')) {
    const theme = target.value || 'dark';
    applyTheme(theme);
    const body = new URLSearchParams();
    body.set('theme', theme);
    fetch('/hx/theme', { method: 'POST', body }).catch(() => {});
  }
});

// SSE hot-reload connection
function connectSSE() {
  const es = new EventSource('/events');

  es.addEventListener('file-changed', () => {
    const badge = document.getElementById('hot-reload-badge');
    if (badge) {
      badge.classList.remove('hidden');
      setTimeout(() => badge.classList.add('hidden'), 2000);
    }
    // Re-render active model with current UI params
    htmx.trigger(document.body, 'rerender');
  });

  es.addEventListener('presets-changed', () => {
    const activeWrap = document.querySelector('[data-active-model]');
    const presetBar = document.getElementById('preset-bar');
    if (activeWrap && presetBar) {
      const modelId = activeWrap.getAttribute('data-active-model');
      const presetSelect = document.getElementById('preset-select');
      const cur = presetSelect ? presetSelect.value : 'default';
      htmx.ajax('GET', `/hx/models/${encodeURIComponent(modelId)}/presets?preset=${encodeURIComponent(cur)}`, {
        target: '#preset-bar',
        swap: 'outerHTML',
      });
    }
  });

  es.onerror = () => {
    es.close();
    setTimeout(connectSSE, 3000);
  };
}

connectSSE();
