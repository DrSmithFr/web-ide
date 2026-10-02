import { defineConfig } from 'vite'
import solid from 'vite-plugin-solid'

const pod = process.env.POD ?? 'http://127.0.0.1:4433'

export default defineConfig({
  plugins: [solid()],
  build: {
    outDir: '../pod/webdist/dist',
    emptyOutDir: true,
    target: 'es2022',
    chunkSizeWarningLimit: 1500,
  },
  server: {
    // In development the page comes from Vite and talks to the pod through this proxy.
    proxy: {
      '/ws': { target: pod.replace(/^http/, 'ws'), ws: true },
      '/auth': { target: pod },
      // Pairing links (/…?token=…) go to the pod, which sets the cookie and redirects back.
      '^/[^?]*\\?(?:.*&)?token=': { target: pod },
    },
  },
})
