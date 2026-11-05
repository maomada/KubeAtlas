import { fileURLToPath } from 'node:url'
import { loadConfigFromFile } from 'vite'
import { describe, expect, it, vi } from 'vitest'

describe('Portal build output', () => {
  it('cleans stale output and emits the tracked empty embed marker', async () => {
    const configFile = fileURLToPath(new URL('../vite.portal.config.ts', import.meta.url))
    const configRoot = fileURLToPath(new URL('..', import.meta.url))
    const loaded = await loadConfigFromFile(
      { command: 'build', mode: 'production' },
      configFile,
      configRoot,
      'silent',
    )
    if (!loaded) throw new Error('Portal Vite config could not be loaded')
    const config = loaded.config
    expect(config.build?.emptyOutDir).toBe(true)
    const marker = config.plugins?.find(plugin =>
      plugin !== null &&
      typeof plugin === 'object' &&
      !Array.isArray(plugin) &&
      'name' in plugin &&
      plugin.name === 'portal-embed-marker'
    ) as unknown as { generateBundle(this: { emitFile: (asset: unknown) => string }): void }
    expect(marker).toBeDefined()
    const emitFile = vi.fn(() => 'marker')
    marker.generateBundle.call({ emitFile })
    expect(emitFile).toHaveBeenCalledExactlyOnceWith({ type: 'asset', fileName: '.gitkeep', source: '' })
  })
})
