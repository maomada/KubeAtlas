import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { createElement } from 'react'
import {
  podStatusClass,
  podStatusLabel,
  workloadPodDetail,
} from './WorkloadView'
import { PodListToggle } from './PodList'

describe('workload Pod presentation', () => {
  it('surfaces container failure evidence instead of reporting only the Pod phase', () => {
    const pod = {
      name: 'workers-2-abc',
      phase: 'Running',
      ready: false,
      containers: ['worker'],
      restartCount: 3,
      reason: 'CrashLoopBackOff',
      lastTerminationReason: 'OOMKilled',
      healthLevel: 'unhealthy' as const,
    }

    expect(podStatusLabel(pod.healthLevel, pod.ready)).toBe('不健康')
    expect(podStatusClass(pod.healthLevel, pod.ready)).toBe('status-unhealthy')
    expect(workloadPodDetail(pod)).toBe(
      'Running / CrashLoopBackOff · 1 个容器 · 3 次重启 · 最近：OOMKilled',
    )
  })

  it('localizes pod list controls while retaining the resource label', () => {
    expect(renderToStaticMarkup(createElement(PodListToggle, { expanded: false, hiddenCount: 2, label: 'Pods', onToggle: () => {} })))
      .toContain('再显示 2 个 Pods')
    expect(renderToStaticMarkup(createElement(PodListToggle, { expanded: true, hiddenCount: 2, label: 'Pods', onToggle: () => {} })))
      .toContain('收起 Pods')
  })
})
