import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from '@playwright/test'

const storageState = join(tmpdir(), 'sing-box-webui-playwright-storage.json')
const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const webURL = `http://127.0.0.1:${process.env.SING_BOX_WEBUI_DEV_PORT ?? '31333'}`

export default defineConfig({
  testDir: resolve(repoRoot, 'web/e2e'),
  timeout: 30_000,
  globalSetup: resolve(repoRoot, 'web/e2e/global-setup.ts'),
  use: {
    baseURL: webURL,
    storageState,
    trace: 'retain-on-failure',
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH }
      : undefined,
  },
  webServer: {
    command: resolve(repoRoot, 'scripts/dev.sh'),
    url: webURL,
    reuseExistingServer: true,
    timeout: 120_000,
  },
})
