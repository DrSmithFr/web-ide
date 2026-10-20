import { defineConfig } from 'vite'
import solid from 'vite-plugin-solid'

const pod = process.env.POD ?? 'http://127.0.0.1:4433'

export default defineConfig({
  plugins: [solid()],
  // Relative paths: the page also runs under /_lan/pod/<key>/ of a gateway pod, which
  // serves index.html with its paths starting there (pod/server).
  base: './',
  build: {
    outDir: '../pod/webdist/dist',
    emptyOutDir: true,
    target: 'es2022',
    chunkSizeWarningLimit: 1500,
    rolldownOptions: {
      output: {
        // The page module of a built-in plugin is a chunk of its own, loaded when the plugin is
        // first enabled (plugins/registry.tsx): its name stays plugin-<id>. Only the files of
        // its folder go there: what the core shares with it stays with the core.
        advancedChunks: {
          includeDependenciesRecursively: false,
          groups: [{ name: (id: string) => /\/src\/plugins\/([a-z]+)\//.exec(id)?.[1].replace(/^/, 'plugin-') ?? null, test: /\/src\/plugins\/[a-z]+\// }],
        },
      },
    },
  },
  // The speech recognition worker loads the ONNX runtime with dynamic imports.
  worker: { format: 'es' },
  server: {
    // In development the page comes from Vite and talks to the pod through this proxy.
    proxy: {
      '/ws': { target: pod.replace(/^http/, 'ws'), ws: true },
      '/auth': { target: pod },
      '/models/': { target: pod },
      // Pairing links (/…?token=…) go to the pod, which sets the cookie and redirects back.
      '^/[^?]*\\?(?:.*&)?token=': { target: pod },
    },
  },
})
