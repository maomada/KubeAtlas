// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { setApiBase } from './config'
import { useTrafficConnect } from './traffic'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

afterEach(() => vi.unstubAllGlobals())

describe('useTrafficConnect', () => {
  it.each([403, 404])('does not fall back from the managed route on HTTP %s', async (status) => {
    setApiBase('/api')
    const sent: Array<{ url: RequestInfo | URL; init?: RequestInit }> = []
    const request = vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
      sent.push({ url, init })
      return new Response(JSON.stringify({ error: 'managed endpoint unavailable' }), {
        status,
        headers: { 'Content-Type': 'application/json' },
      })
    })
    vi.stubGlobal('fetch', request)

    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    const host = document.body.appendChild(document.createElement('div'))
    const root = createRoot(host)
    let connect!: ReturnType<typeof useTrafficConnect>
    function Probe() {
      connect = useTrafficConnect()
      return null
    }

    try {
      await act(async () => {
        root.render(<QueryClientProvider client={client}><Probe /></QueryClientProvider>)
      })
      await act(async () => {
        await connect.mutateAsync().catch(() => {})
      })
      expect(request).toHaveBeenCalledTimes(1)
      expect(new URL(String(sent[0]?.url), window.location.origin).pathname).toBe('/api/traffic/connect-managed')
      expect(sent[0]?.init?.method).toBe('POST')
    } finally {
      act(() => root.unmount())
      client.clear()
      host.remove()
    }
  })
})
