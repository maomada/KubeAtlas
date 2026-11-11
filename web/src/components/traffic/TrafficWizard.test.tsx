// @vitest-environment jsdom
import { act, type ComponentProps } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { TrafficWizard } from './TrafficWizard'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const installProps = vi.hoisted(() => vi.fn())
vi.mock('../helm/InstallWizard', () => ({
  InstallWizard: (props: unknown) => { installProps(props); return null },
}))

let container: HTMLDivElement
let root: Root
beforeEach(() => {
  installProps.mockClear()
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})
afterEach(() => {
  act(() => root.unmount())
  container.remove()
})

it.each([
  ['kubeatlas-bundled', '', '0.0.16', 'local'],
  ['groundcover', 'https://helm.groundcover.com/', 'latest', 'artifacthub'],
])('uses the correct install source for %s', (repo, repoUrl, version, source) => {
  const props: ComponentProps<typeof TrafficWizard> = {
    state: 'wizard', setState: vi.fn(), sourcesLoading: false, onRefetch: vi.fn(),
    sourcesData: {
      cluster: { platform: 'generic', cni: 'unknown', dataplaneV2: false, clusterName: 'test', k8sVersion: '1.30' },
      active: '', detected: [], notDetected: ['caretta'],
      recommended: { name: 'caretta', reason: 'Traffic monitoring', helmChart: { repo, repoUrl, chartName: 'caretta', version } },
    },
  }
  act(() => root.render(<TrafficWizard {...props} />))
  if (source === 'local') {
    expect(container.textContent).toContain('no external chart download')
    expect(container.textContent).toContain('Container images must be preloaded')
  }
  const button = Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('Install caretta with Helm'))
  expect(button).toBeDefined()
  act(() => button!.click())
  expect(installProps).toHaveBeenCalledWith(expect.objectContaining({ repo, repoUrl, version, source }))
})
