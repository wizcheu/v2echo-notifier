import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { version } from './package.json'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  define: {
    __APP_VERSION__: JSON.stringify(process.env.VITE_APP_VERSION?.trim() || `${version}-dev`),
    __APP_REVISION__: JSON.stringify(process.env.VITE_APP_REVISION?.trim() || ''),
  },
  resolve: { alias: { '@': new URL('./src', import.meta.url).pathname } },
  server: { proxy: { '/api': 'http://127.0.0.1:8282', '/browser-desktop': { target: 'http://127.0.0.1:8282', ws: true } } },
})
