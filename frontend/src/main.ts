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

// Native <dialog>.close() restores focus to the trigger button; WebKitGTK marks that
// restored focus as :focus-visible even after mouse clicks, leaving a stuck focus ring.
// Preserve restored focus for keyboard (Tab) navigation, but blur it for pointer clicks.
let keyboardNavOutsideModal = false;
window.addEventListener(
  'pointerdown',
  () => {
    keyboardNavOutsideModal = false;
  },
  true
);
window.addEventListener(
  'keydown',
  (evt) => {
    if (evt.key === 'Tab' && !document.querySelector('dialog[open]')) {
      keyboardNavOutsideModal = true;
    }
  },
  true
);
document.addEventListener(
  'close',
  (evt) => {
    if (!(evt.target instanceof HTMLDialogElement) || keyboardNavOutsideModal) return;
    const blurIfRestored = () => {
      if (document.querySelector('dialog[open]')) return;
      const active = document.activeElement;
      if (active instanceof HTMLElement && active !== document.body) {
        active.blur();
      }
    };
    blurIfRestored();
    queueMicrotask(blurIfRestored);
  },
  true
);

mount(App, { target });
