import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: { proxy: { '/api': 'http://127.0.0.1:8282', '/browser-desktop': { target: 'http://127.0.0.1:8282', ws: true } } },
})
