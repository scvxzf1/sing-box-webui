import { request, type FullConfig } from '@playwright/test'
import { readFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..')

export default async function globalSetup(config: FullConfig) {
  const baseURL = String(config.projects[0]?.use.baseURL ?? 'http://127.0.0.1:33333')
  const storageState = join(tmpdir(), 'sing-box-webui-playwright-storage.json')
  const context = await request.newContext({ baseURL })
  try {
    const session = await context.get('/api/v1/session')
    if (session.status() === 401) {
      const configPath = resolve(repoRoot, process.env.SING_BOX_WEBUI_CONFIG ?? 'var/config.json')
      const stored = JSON.parse(await readFile(configPath, 'utf8')) as { web?: { token?: string } }
      const token = process.env.SING_BOX_WEBUI_E2E_TOKEN ?? stored.web?.token
      if (!token) {
        throw new Error('Web Token is required for authenticated Playwright tests')
      }
      const login = await context.post('/api/v1/auth/login', {
        headers: { Origin: baseURL },
        data: { token },
      })
      if (!login.ok()) {
        throw new Error(`Playwright Web Token login failed with HTTP ${login.status()}`)
      }
    } else if (!session.ok()) {
      throw new Error(`Playwright session check failed with HTTP ${session.status()}`)
    }
    await context.storageState({ path: storageState })
  } finally {
    await context.dispose()
  }
}
