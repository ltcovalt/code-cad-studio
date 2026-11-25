import { describe, expect, it, vi } from 'vitest';
import { startNativeWindowResize } from './windowResize';

describe('startNativeWindowResize', () => {
  it('posts wails:resize message to WebView2 bridge on Windows', () => {
    const postMessage = vi.fn();
    const ok = startNativeWindowResize('se-resize', { chrome: { webview: { postMessage } } });
    expect(ok).toBe(true);
    expect(postMessage).toHaveBeenCalledWith('wails:resize:se-resize');
  });

  it('posts wails:resize message to WebKitGTK bridge on Linux', () => {
    const postMessage = vi.fn();
    const ok = startNativeWindowResize('e-resize', {
      webkit: { messageHandlers: { external: { postMessage } } },
    });
    expect(ok).toBe(true);
    expect(postMessage).toHaveBeenCalledWith('wails:resize:e-resize');
  });

  it('returns false cleanly in plain browser mode', () => {
    expect(startNativeWindowResize('s-resize', {})).toBe(false);
    expect(startNativeWindowResize('se-resize', undefined)).toBe(false);
  });
});
