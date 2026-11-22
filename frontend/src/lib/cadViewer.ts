import { Display, Viewer } from 'three-cad-viewer';
import type { RenderOptions, Shapes, Theme, ViewerOptions } from 'three-cad-viewer';

export const LIGHT_THEMES = new Set(['light', 'cupcake', 'emerald', 'corporate', 'retro']);

export const DAISY_THEMES: ReadonlyArray<{ id: string; label: string }> = [
  { id: 'dark', label: 'Dark' },
  { id: 'light', label: 'Light' },
  { id: 'dracula', label: 'Dracula' },
  { id: 'night', label: 'Night' },
  { id: 'corporate', label: 'Corporate' },
  { id: 'emerald', label: 'Emerald' },
  { id: 'cupcake', label: 'Cupcake' },
  { id: 'retro', label: 'Retro' },
];

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

  constructor(container: HTMLElement, initialDaisyTheme = 'dark') {
    this.container = container;
    this.theme = LIGHT_THEMES.has(initialDaisyTheme || 'dark') ? 'light' : 'dark';
    this.container.setAttribute('data-theme', this.theme);
    this.initResizeObserver();
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
    this.viewer = new Viewer(this.display, displayOptions, null);
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

    const preserveCamera = this.hasRendered && !resetCamera && this.lastModelId === modelId;
    let savedCam: ReturnType<Viewer['getCameraLocationSettings']> | null = null;
    if (preserveCamera) {
      try {
        savedCam = this.viewer.getCameraLocationSettings();
      } catch {
        savedCam = null;
      }
    }

    const renderOptions: RenderOptions = {
      edgeColor: 0x555555,
      ambientIntensity: 1.0,
      directIntensity: 1.1,
      metalness: 0.3,
      roughness: 0.65,
      defaultOpacity: 0.5,
    };

    const viewerOptions: ViewerOptions = {
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

    this.viewer.render(shapes as Shapes, renderOptions, viewerOptions);
    this.hasRendered = true;
    this.lastModelId = modelId;
    if (this.container.getAttribute('data-theme') !== this.theme) {
      try {
        this.viewer.setTheme(this.theme);
      } catch (err) {
        console.warn('setTheme warning:', err);
      }
    }
    document.body.removeAttribute('data-theme');
    requestAnimationFrame(() => this.syncSize());

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

  dispose(): void {
    if (this.rafId !== null) {
      cancelAnimationFrame(this.rafId);
      this.rafId = null;
    }
    this.resizeObserver?.disconnect();
    this.resizeObserver = null;
    try {
      this.viewer?.dispose();
    } catch {
      // ignore cleanup errors
    }
    this.viewer = null;
    this.display = null;
  }
}
