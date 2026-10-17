import { readFileSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// The Go server; it serves the API and console websockets. Its certificate
// isn't checked, so a self-signed one works.
const backend = process.env.QUICKSAND_BACKEND ?? 'http://127.0.0.1:8484'

// QUICKSAND_DEV_CERT and QUICKSAND_DEV_KEY serve the dev server over HTTPS,
// e.g. for single sign-on providers that only redirect to https.
const cert = process.env.QUICKSAND_DEV_CERT
const key = process.env.QUICKSAND_DEV_KEY
const https = cert && key ? { cert: readFileSync(cert), key: readFileSync(key) } : undefined

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    https,
    proxy: {
      '/api': { target: backend, ws: true, secure: false },
    },
  },
})
