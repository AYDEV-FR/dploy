// @ts-check
import { defineConfig } from 'astro/config';

// Single-page Astro app (vanilla TS, no UI framework). The Go API serves the
// build output from ./web/dist (assets under /static) and falls back to
// index.html for every client route. This dev middleware mirrors that so a
// hard refresh on /catalog or /run/<env> also works locally.
/** @type {import('vite').Plugin} */
const spaFallback = {
  name: 'dploy-spa-fallback',
  configureServer(server) {
    server.middlewares.use((req, _res, next) => {
      const url = req.url || '/';
      const accepts = (req.headers.accept || '').includes('text/html');
      const isInternal =
        url.startsWith('/api') ||
        url.startsWith('/auth') ||
        url.startsWith('/static') ||
        url.startsWith('/@') ||
        url.startsWith('/node_modules') ||
        url.startsWith('/src') ||
        url.includes('.');
      if (req.method === 'GET' && accepts && !isInternal && url !== '/') {
        req.url = '/';
      }
      next();
    });
  },
};

export default defineConfig({
  build: {
    assets: 'static',
  },
  vite: {
    plugins: [spaFallback],
    server: {
      proxy: {
        '/api': 'http://localhost:8080',
        '/auth': 'http://localhost:8080',
      },
    },
  },
});
