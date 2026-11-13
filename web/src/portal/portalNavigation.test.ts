// @vitest-environment node
import { expect, it, vi } from 'vitest'
import { replacePortalLocation } from './portalNavigation'

it('uses a hard location replacement for Portal reloads', () => {
  const replace = vi.fn()
  vi.stubGlobal('window', { location: { replace } })
  try {
    replacePortalLocation('/')
    expect(replace).toHaveBeenCalledWith('/')
  } finally {
    vi.unstubAllGlobals()
  }
})
