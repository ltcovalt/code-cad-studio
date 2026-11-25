export type WindowResizeEdge =
  | 'n-resize'
  | 'ne-resize'
  | 'e-resize'
  | 'se-resize'
  | 's-resize'
  | 'sw-resize'
  | 'w-resize'
  | 'nw-resize';

interface WailsWebviewBridge {
  chrome?: {
    webview?: {
      postMessage?: (message: string) => void;
    };
  };
  webkit?: {
    messageHandlers?: {
      external?: {
        postMessage?: (message: string) => void;
      };
    };
  };
}

/**
 * Ask the Wails v3 desktop host to begin a native OS window resize drag from
 * the given edge or corner (`gtk_window_begin_resize_drag` on Linux,
 * `WM_NCLBUTTONDOWN` on Windows). Returns `true` if a bridge was available.
 */
export function startNativeWindowResize(
  edge: WindowResizeEdge,
  targetWindow: unknown = typeof window !== 'undefined' ? window : undefined
): boolean {
  const bridge = targetWindow as WailsWebviewBridge | undefined;
  const msg = `wails:resize:${edge}`;

  if (bridge?.chrome?.webview?.postMessage) {
    bridge.chrome.webview.postMessage(msg);
    return true;
  }
  if (bridge?.webkit?.messageHandlers?.external?.postMessage) {
    bridge.webkit.messageHandlers.external.postMessage(msg);
    return true;
  }
  return false;
}
