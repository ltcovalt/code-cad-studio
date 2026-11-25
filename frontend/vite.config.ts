import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

const devPort = Number(process.env.WAILS_VITE_PORT) || 9245;

export default defineConfig({
  server: {
    host: '127.0.0.1',
    port: devPort,
    strictPort: true,
    cors: {
      origin: /^(?:https?|wails):\/\/(?:(?:[^:]+\.)?localhost|127\.0\.0\.1|\[::1\])(?::\d+)?$/,
    },
    hmr: {
      protocol: 'ws',
      host: '127.0.0.1',
      port: devPort,
    },
  },
  plugins: [svelte(), tailwindcss()],
  resolve: process.env.VITEST ? { conditions: ['browser'] } : undefined,
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  test: {
    environment: 'happy-dom',
    include: ['src/**/*.test.ts'],
  },
});
