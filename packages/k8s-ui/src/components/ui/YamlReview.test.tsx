import { describe, expect, it } from 'vitest'
import { renderToString } from 'react-dom/server'
import { YamlReview, type YamlPreviewResult } from './YamlReview'

const doc = { index: 0, kind: 'ConfigMap', name: 'app', namespace: 'shop', status: 'accepted' as const, action: 'update' as const, reviewedResourceVersion: '42' }

function review(changedSinceReview?: boolean, applyError?: string, force = false, applyLabel?: string) {
  return renderToString(
    <YamlReview
      submittedYaml="kind: ConfigMap"
      documents={[doc]}
      changedSinceReview={changedSinceReview}
      applyError={applyError}
      force={force}
      applyLabel={applyLabel}
      onBack={() => {}}
      onApply={() => {}}
    />,
  )
}

describe('YamlReview after a failed apply', () => {
  it('says the review now shows the latest version when the resource changed under it', () => {
    const html = review(true, 'resource changed after review; review the latest version before applying')
    expect(html).toContain('此资源在审查后已发生变化')
    expect(html).toContain('重新应用')
    // Said once: the raw API message would repeat the notice.
    expect(html).not.toContain('review the latest version before applying')
  })

  it('keeps an unrelated apply error beside the notice', () => {
    const html = review(true, 'admission webhook "policy.example" denied the request')
    expect(html).toContain('此资源在审查后已发生变化')
    expect(html).toContain('denied the request')
  })

  it('says nothing extra when the resource did not change', () => {
    expect(review(false, 'forbidden')).not.toContain('审查后已发生变化')
    expect(review(false, 'forbidden')).toContain('forbidden')
    expect(review()).not.toContain('审查后已发生变化')
  })
})

it('localizes review actions and force explanation while preserving raw apply errors', () => {
  const forceOn = review(false, 'admission webhook "policy.example" denied the request', true)
  expect(forceOn).toContain('返回编辑')
  expect(forceOn).toContain('审查集群准入的更改')
  expect(forceOn).toContain('应用已审查的更改')
  expect(forceOn).toContain('强制覆盖：')
  expect(forceOn).toContain('开启')
  expect(forceOn).toContain('其他管理器拥有的字段可能被覆盖。')
  expect(forceOn).toContain('admission webhook')
  expect(forceOn).toContain('denied the request')

  const forceOff = review(false, 'forbidden', false)
  expect(forceOff).toContain('强制覆盖：')
  expect(forceOff).toContain('关闭')
  expect(forceOff).toContain('与其他管理器字段的冲突会阻止保存。')
  expect(forceOff).toContain('forbidden')
})

it('passes unknown preview status and action values through and preserves a custom apply label', () => {
  const futureStatus = 'future-status' as YamlPreviewResult['status']
  const futureAction = 'scale' as YamlPreviewResult['action']
  const html = renderToString(
    <YamlReview
      submittedYaml="kind: ConfigMap"
      documents={[{ ...doc, status: futureStatus, action: futureAction }]}
      applyLabel="Create resources"
      onBack={() => {}}
      onApply={() => {}}
    />,
  )
  expect(html).toContain('future-status')
  expect(html).toContain('将scale')
  expect(html).toContain('Create resources')

  const hiddenUnknownAction = renderToString(
    <YamlReview
      submittedYaml="kind: ConfigMap"
      documents={[{ ...doc, action: 'unknown' }]}
      onBack={() => {}}
      onApply={() => {}}
    />,
  )
  expect(hiddenUnknownAction).not.toContain('将unknown')
  expect(hiddenUnknownAction).not.toContain('unknown')
})


it('retains pause context when the prediction removes the annotation without claiming confirmed resumption', () => {
  const baselineYaml = 'apiVersion: example.test/v1\nkind: Bucket\nmetadata:\n  name: archive\n  annotations:\n    crossplane.io/paused: "true"\nspec:\n  providerConfigRef:\n    name: default\n'
  const predictedYaml = baselineYaml.replace('    crossplane.io/paused: "true"', '    crossplane.io/paused: "false"')
  const result = renderToString(<YamlReview submittedYaml={predictedYaml} documents={[{ index: 0, status: 'accepted', action: 'update', baselineYaml, predictedYaml }]} onBack={() => {}} onApply={() => {}} />)
  expect(result).toContain('Pause affects reconciliation')
  expect(result).toContain('请求恢复协调')
  expect(result).not.toContain('will resume')
  expect(result.replaceAll('<!-- -->', '')).toContain('Server preview clears the pause request on Bucket archive')
})


it('shows pause context for an unselected later document because Apply submits the whole batch', () => {
  const first = 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: ordinary\n'
  const second = 'apiVersion: example.test/v1\nkind: Bucket\nmetadata:\n  name: later-paused\n  annotations:\n    crossplane.io/paused: "true"\nspec:\n  providerConfigRef:\n    name: default\n'
  const result = renderToString(<YamlReview submittedYaml={first + '\n---\n' + second} documents={[{ index: 0, status: 'accepted', predictedYaml: first }, { index: 1, status: 'accepted', predictedYaml: second }]} onBack={() => {}} onApply={() => {}} />)
  expect(result).toContain('Pause affects reconciliation')
  expect(result).toContain('Bucket later-paused')
})


it('states the actual retained pause in the prediction without inferring who preserved it', () => {
  const baselineYaml = 'apiVersion: example.test/v1\nkind: Bucket\nmetadata:\n  name: archive\n  annotations:\n    crossplane.io/paused: "true"\nspec:\n  providerConfigRef:\n    name: default\n'
  const submittedYaml = baselineYaml.replace('    crossplane.io/paused: "true"', '')
  const result = renderToString(<YamlReview submittedYaml={submittedYaml} documents={[{ index: 0, status: 'accepted', action: 'update', baselineYaml, predictedYaml: baselineYaml }]} onBack={() => {}} onApply={() => {}} />)
  expect(result.replaceAll('<!-- -->', '')).toContain('Server preview keeps the pause request on Bucket archive')
  expect(result).not.toContain('another field manager owns')
  expect(result).not.toContain('Server preview clears')
})

it.each(['apply', 'create'] as const)('handles a not-previewable Crossplane document in %s mode without pretending submitted YAML is live', mode => {
  const submittedYaml = 'apiVersion: example.test/v1\nkind: Bucket\nmetadata:\n  name: archive\nspec:\n  providerConfigRef:\n    name: default\n'
  const result = renderToString(<YamlReview submittedYaml={submittedYaml} mode={mode} documents={[{ index: 0, status: 'unavailable', submittedYaml }]} onBack={() => {}} onApply={() => {}} />)
  expect(result.includes('Existing pause state is unknown')).toBe(mode === 'apply')
  expect(result.includes('Omitting the annotation from server-side apply YAML does not guarantee its removal')).toBe(mode === 'apply')
  expect(result).not.toContain('Server preview clears')
})


it.each(['create', 'unavailable'] as const)('keeps per-document pause evidence paired when the first document has no baseline (%s)', firstState => {
  const firstYaml = 'apiVersion: example.test/v1\nkind: Bucket\nmetadata:\n  name: fresh\nspec:\n  providerConfigRef:\n    name: default\n'
  const pausedYaml = firstYaml.replace('name: fresh', 'name: archive\n  annotations:\n    crossplane.io/paused: "true"')
  const first = firstState === 'create'
    ? { index: 0, status: 'accepted' as const, action: 'create' as const, name: 'fresh', predictedYaml: firstYaml }
    : { index: 0, status: 'unavailable' as const, action: 'unknown' as const, name: 'fresh', submittedYaml: firstYaml }
  const result = renderToString(<YamlReview submittedYaml={firstYaml + '\n---\n' + pausedYaml} documents={[first, { index: 1, status: 'accepted', action: 'update', name: 'archive', baselineYaml: pausedYaml, predictedYaml: pausedYaml }]} onBack={() => {}} onApply={() => {}} />).replaceAll('<!-- -->', '')
  expect(result).toContain('Server preview keeps the pause request on Bucket archive')
  expect(result).not.toContain('Server preview clears the pause request')
  expect(result).not.toContain('Server preview requests a pause on Bucket archive')
  expect(result.includes('Live pause state is unverified for Bucket fresh')).toBe(firstState === 'unavailable')
})
