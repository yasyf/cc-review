import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// TODO: the daemon binds 127.0.0.1:0 (ephemeral port) and writes it to
// ~/.cc-review/http.json. For local dev, run the daemon, read its port, and set
// CC_REVIEW_DEV_PORT before `bun run dev`. Default below is a placeholder.
const devPort = process.env.CC_REVIEW_DEV_PORT ?? '8787';
const devTarget = `http://127.0.0.1:${devPort}`;

export default defineConfig({
  plugins: [react()],
  // Absolute asset URLs (/assets/...) so the SPA loads under deep links like /s/<id>.
  base: '/',
  build: {
    // Output straight into the Go embed target. It lives outside this web root,
    // so emptyOutDir must be explicit.
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  worker: {
    format: 'es',
  },
  server: {
    proxy: {
      '/api': {
        target: devTarget,
        changeOrigin: false,
      },
      '/events': {
        target: devTarget,
        changeOrigin: false,
        // SSE must stream, never buffer.
        configure(proxy) {
          proxy.on('proxyRes', (proxyRes) => {
            proxyRes.headers['x-accel-buffering'] = 'no';
            proxyRes.headers['cache-control'] = 'no-cache';
          });
        },
      },
    },
  },
});
