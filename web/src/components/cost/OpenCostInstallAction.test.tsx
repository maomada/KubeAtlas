// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { OpenCostInstallAction } from './OpenCostInstallAction'
import { installChartWithProgress, useCanHelmAct, useChartDetail, useHelmReleases } from '../../api/client'
import { getApiBase } from '../../api/config'

vi.mock('../../api/client', () => ({
  installChartWithProgress: vi.fn(),
  useCanHelmAct: vi.fn(),
  useChartDetail: vi.fn(),
  useHelmReleases: vi.fn(),
}))
vi.mock('../../api/config', () => ({ getApiBase: vi.fn() }))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

let host: HTMLDivElement
let root: ReturnType<typeof createRoot>
let client: QueryClient

beforeEach(() => {
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
  client = new QueryClient()
  vi.mocked(getApiBase).mockReturnValue('/c/cluster-one/api')
  vi.mocked(useCanHelmAct).mockReturnValue({ allowed: true })
  vi.mocked(useChartDetail).mockReturnValue({ data: { version: '1.2.3' } } as ReturnType<typeof useChartDetail>)
  vi.mocked(useHelmReleases).mockReturnValue({ data: [] } as unknown as ReturnType<typeof useHelmReleases>)
  vi.mocked(installChartWithProgress).mockResolvedValue({ name: 'opencost' } as Awaited<ReturnType<typeof installChartWithProgress>>)
})

afterEach(() => {
  act(() => root.unmount())
  client.clear()
  host.remove()
  vi.resetAllMocks()
})

function render(onInstalled = vi.fn()) {
  act(() => root.render(<QueryClientProvider client={client}>
    <OpenCostInstallAction cluster="Cluster One" onInstalled={onInstalled} />
  </QueryClientProvider>))
  const dialog = host.querySelector('dialog')
  if (dialog) {
    dialog.showModal = () => { dialog.open = true }
    dialog.close = () => { dialog.open = false }
  }
  return dialog
}

it('requires confirmation, binds the request to the reviewed cluster, and waits for metrics after deployment', async () => {
  const onInstalled = vi.fn()
  const dialog = render(onInstalled)!
  const trigger = host.querySelector<HTMLButtonElement>('button')!
  const confirm = Array.from(dialog.querySelectorAll('button')).find(button => button.textContent === '确认安装')!
  act(() => trigger.click())
  expect(dialog.open).toBe(true)
  expect(dialog.textContent).toContain('Cluster One')
  expect(dialog.textContent).toContain('1.2.3')
  expect(document.activeElement?.textContent).toBe('取消')
  expect(installChartWithProgress).not.toHaveBeenCalled()
  vi.mocked(getApiBase).mockReturnValue('/c/cluster-other/api')
  await act(async () => confirm.click())
  expect(installChartWithProgress).not.toHaveBeenCalled()
  vi.mocked(getApiBase).mockReturnValue('/c/cluster-one/api')
  await act(async () => confirm.click())
  expect(installChartWithProgress).toHaveBeenCalledExactlyOnceWith({
    repository: 'kubeatlas-bundled', chartName: 'opencost', version: '1.2.3',
    releaseName: 'opencost', namespace: 'opencost', createNamespace: true,
  }, expect.any(Function))
  expect(onInstalled).toHaveBeenCalledOnce()
  expect(host.querySelector('[role="status"]')?.textContent).toContain('等待 Prometheus 采集')
})

it('withholds the install action for an existing failed release, unavailable package, or missing write permission', () => {
  vi.mocked(useHelmReleases).mockReturnValue({
    data: [{ name: 'opencost', namespace: 'opencost', status: 'failed' }],
  } as unknown as ReturnType<typeof useHelmReleases>)
  expect(render()).toBeNull()
  expect(host.textContent).toContain('已有 OpenCost 部署记录')
  vi.mocked(useHelmReleases).mockReturnValue({ data: undefined } as ReturnType<typeof useHelmReleases>)
  vi.mocked(useChartDetail).mockReturnValue({ isError: true } as ReturnType<typeof useChartDetail>)
  expect(render()).toBeNull()
  expect(host.textContent).toContain('离线安装包不可用')
  vi.mocked(useCanHelmAct).mockReturnValue({ allowed: false })
  expect(render()).toBeNull()
  expect(host.textContent).toBe('')
  expect(installChartWithProgress).not.toHaveBeenCalled()
})
