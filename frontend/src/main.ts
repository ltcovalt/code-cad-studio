import { mount } from 'svelte';
import './styles.css';
import App from './App.svelte';

// Load Wails desktop runtime when running inside a native WebView so
// --wails-draggable titlebar dragging and frameless edge resizing work on Linux & macOS.
const wailsWin = window as unknown as {
  webkit?: { messageHandlers?: { external?: { postMessage?: unknown } } };
  chrome?: { webview?: { postMessage?: unknown } };
};
if (wailsWin.webkit?.messageHandlers?.external?.postMessage || wailsWin.chrome?.webview?.postMessage) {
  const runtimeScript = document.createElement('script');
  runtimeScript.type = 'module';
  runtimeScript.src = '/wails/runtime.js';
  document.head.appendChild(runtimeScript);
}

const target = document.getElementById('app');
if (!target) {
  throw new Error('Root #app element not found');
}

mount(App, { target });
