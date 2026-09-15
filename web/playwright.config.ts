import { defineConfig } from '@playwright/test'
export default defineConfig({
  testDir: './tests',
  testMatch: '*.spec.ts',
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: 'http://127.0.0.1:4178',
    channel: 'chrome',
    viewport: { width: 1440, height: 1024 },
    colorScheme: 'light',
  },
  webServer: {
    command: 'npm run dev -- --port 4178 --strictPort',
    url: 'http://127.0.0.1:4178',
    reuseExistingServer: false,
  },
})
