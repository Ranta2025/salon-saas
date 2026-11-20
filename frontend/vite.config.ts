import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'

/**
 * Local development talks to the API through this proxy, so the browser only
 * sees one origin and CORS is not needed. `VITE_API_URL` (if set) wins and the
 * build embeds it; the Docker image receives it as a build arg.
 */
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const proxyTarget = env.VITE_API_PROXY_TARGET?.trim() || 'http://localhost:8080'

  return {
    plugins: [react()],
    server: {
      host: true,
      port: 5173,
      proxy: {
        '/api': {
          target: proxyTarget,
          changeOrigin: true,
          ws: true,
        },
      },
    },
    preview: {
      host: true,
      port: 4173,
      // `vite preview` serves the built bundle; proxying /api keeps the same
      // same-origin behaviour as dev so the build can be reviewed against a
      // local API without rebuilding with VITE_API_URL.
      proxy: {
        '/api': {
          target: proxyTarget,
          changeOrigin: true,
          ws: true,
        },
      },
    },
  }
})
