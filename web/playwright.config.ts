import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from '@playwright/test'

const storageState = join(tmpdir(), 'sing-box-webui-playwright-storage.json')
const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')

export default defineConfig({
  testDir: resolve(repoRoot, 'web/e2e'),
  timeout: 30_000,
  globalSetup: resolve(repoRoot, 'web/e2e/global-setup.ts'),
  use: {
    baseURL: 'http://127.0.0.1:33333',
    storageState,
    trace: 'retain-on-failure',
  },
  webServer: {
    command: resolve(repoRoot, 'scripts/dev.sh'),
    url: 'http://127.0.0.1:33333',
    reuseExistingServer: true,
    timeout: 120_000,
  },
})
