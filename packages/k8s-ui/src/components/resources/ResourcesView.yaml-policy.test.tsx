// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { APIResource } from '../../types'
import { KeyboardShortcutProvider } from '../../hooks/useKeyboardShortcuts'
import { ResourcesView } from './ResourcesView'
import { YamlDraftPolicyProvider } from '../shared/YamlDraftPolicy'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const pods: APIResource = {
  group: '',
  version: 'v1',
  kind: 'Pod',
  name: 'pods',
  namespaced: true,
  isCrd: false,
  verbs: ['get', 'list'],
}

let root: Root | undefined
let host: HTMLDivElement | undefined

beforeEach(() => {
  window.matchMedia = vi.fn().mockReturnValue({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })
  Element.prototype.scrollIntoView = vi.fn()
  host = document.createElement('div')
  document.body.appendChild(host)
  root = createRoot(host)
})

afterEach(() => {
  act(() => root?.unmount())
  host?.remove()
  vi.restoreAllMocks()
})

it('keeps the native create entry point for owners and omits it for Viewers', () => {
  const onCreateResource = vi.fn()
  const render = (readOnly: boolean, scope: string) => act(() => root!.render(
    <YamlDraftPolicyProvider scope={scope} readOnly={readOnly}>
      <KeyboardShortcutProvider>
        <ResourcesView
          namespaces={['test']}
          apiResources={[pods]}
          resourceCounts={{ Pod: 1 }}
          selectedKindQuery={{ isLoading: false, data: [], resourceName: 'pods', group: '' }}
          locationPathname="/resources/pods"
          locationSearch=""
          onNavigate={() => {}}
          onCreateResource={onCreateResource}
        />
      </KeyboardShortcutProvider>
    </YamlDraftPolicyProvider>,
  ))

  render(false, 'org-a:owner:logical-cluster-a')
  const create = host!.querySelector<HTMLButtonElement>('button[aria-label="创建 Pod"]')
  expect(create).not.toBeNull()
  act(() => create!.click())
  expect(onCreateResource).toHaveBeenCalledWith(expect.objectContaining({ kind: 'Pod', name: 'pods' }))

  render(true, 'org-b:viewer:logical-cluster-b')
  expect(host!.querySelector('button[aria-label="创建 Pod"]')).toBeNull()
  expect(onCreateResource).toHaveBeenCalledTimes(1)
})
